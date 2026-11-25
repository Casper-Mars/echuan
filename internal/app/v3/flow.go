package v3

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// MaterialReader reads a material body by business id. The flow engine needs
// the breakdown plan; wiring the real object store is the API layer's job, so
// this package only depends on the one-method interface.
type MaterialReader interface {
	Read(ctx context.Context, materialID string) ([]byte, error)
}

// WithMaterialReader injects the object reader used by a list derivation.
func (s *Service) WithMaterialReader(reader MaterialReader) *Service {
	s.materials = reader
	return s
}

// planDocument mirrors the persisted breakdown plan artifact. The same shape is
// accepted as JSON and as YAML because a list derivation declares its format
// (R6); the entries are identical, only the encoding differs.
type planDocument struct {
	Items []planItem `json:"items" yaml:"items"`
}

type planItem struct {
	SeqNo     int    `json:"seq_no" yaml:"seq_no"`
	ProjectID string `json:"project_id" yaml:"project_id"`
	Title     string `json:"title" yaml:"title"`
	BodyMD    string `json:"body_md" yaml:"body_md"`
	Priority  int    `json:"priority" yaml:"priority"`
	DependsOn []int  `json:"depends_on" yaml:"depends_on"`
}

// deriveEntry is one card a derivation creates. The entry model is shared by
// every mode (D3″): single uses one empty entry, list turns every plan item into
// one, fanout uses the entries written in the configuration. Sibling
// dependencies are entry keys, resolved to the cards of the same batch.
type deriveEntry struct {
	key       string
	title     string
	bodyMD    string
	priority  int
	dependsOn []string
	seqNo     int
}

// derivation is the transaction-free plan of one approval's successors. It is
// built before the approval transaction opens (a plan reading failure therefore
// aborts with the card untouched in in_review), then executed inside it.
type derivation struct {
	src         platform.Stage
	dst         platform.Stage
	mode        platform.DeriveMode
	entries     []deriveEntry
	fingerprint string

	// upstream and created are filled while the hook runs, for the post-commit
	// log line: the log names stages and the upstream card, never ids.
	upstream string
	created  int
	skipped  bool
}

// rule is the audit rule string of this derivation: "<src name>-><dst name>",
// for example "requirement->design". AC7 requires the default flow's string to
// stay byte-for-byte compatible, which is why the stage names are the rule.
func (d *derivation) rule() string { return d.src.Name + "->" + d.dst.Name }

// planDerivation resolves what an approval of task creates. A nil plan means
// "no successor": the stage derives nothing, its derive switch is off, or the
// task's kind is not in the configured flow (the card is history from a removed
// stage).
//
// Everything that can fail on operator data — the plan artifact of a list
// derivation — is read here, outside the approval transaction, so a broken plan
// answers 409 while the card stays untouched in in_review (§8.5).
func (s *Service) planDerivation(ctx context.Context, task storev3.Task) (*derivation, error) {
	src, ok := s.flow.ByID(int(task.Kind))
	if !ok {
		s.logger.Warn("flow derivation skipped(unknown_kind)",
			"kind", int(task.Kind), "task_id", task.TaskID)
		return nil, nil
	}
	// D1: the switch that decides whether this approval derives anything sits on
	// the stage the approved card belongs to. There is no flow-level switch, so a
	// stage that forgot derive.auto stays silent instead of inheriting one.
	if !src.Derive.Auto {
		return nil, nil
	}
	mode := src.Derive.Mode
	if mode == "" || mode == platform.DeriveNone {
		return nil, nil
	}
	dst, ok := s.flow.ByName(src.Derive.To)
	if !ok {
		// Startup validation R7 rejects this; the guard keeps a process that
		// somehow holds a half-valid flow from failing an approval that the
		// operator cannot fix from the card.
		s.logger.Warn("flow derivation skipped(unknown_stage)",
			"stage", src.Derive.To, "from", src.Name, "task_id", task.TaskID)
		return nil, nil
	}

	entries, err := s.deriveEntries(ctx, task, src, mode)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return &derivation{
		src:         src,
		dst:         dst,
		mode:        mode,
		entries:     entries,
		fingerprint: s.flow.Fingerprint(),
	}, nil
}

// deriveEntries builds the card entries of one derivation.
func (s *Service) deriveEntries(ctx context.Context, task storev3.Task, src platform.Stage, mode platform.DeriveMode) ([]deriveEntry, error) {
	switch mode {
	case platform.DeriveSingle:
		return []deriveEntry{{}}, nil
	case platform.DeriveFanout:
		entries := make([]deriveEntry, 0, len(src.Derive.Items))
		for _, item := range src.Derive.Items {
			entries = append(entries, deriveEntry{
				key:       item.Key,
				title:     item.Title,
				bodyMD:    item.BodyMD,
				priority:  item.Priority,
				dependsOn: item.DependsOn,
			})
		}
		return entries, nil
	case platform.DeriveList:
		return s.readPlanEntries(ctx, task.TaskID, src)
	default:
		return nil, nil
	}
}

// readPlanEntries loads and validates the output artifact a list derivation
// expands. Every validation the hard-coded breakdown rule had is kept: a missing
// output, an unreadable artifact, an unknown format, a non-positive or duplicate
// seq_no and an empty title all answer 409 before the approval runs, so the
// approved card stays in in_review and a retry starts from the same state.
func (s *Service) readPlanEntries(ctx context.Context, taskID string, src platform.Stage) ([]deriveEntry, error) {
	attachment, err := storev3.LatestOutput(ctx, s.st.Read(), taskID)
	if err != nil {
		return nil, conflict("%s %s has no plan output: %v", src.Name, taskID, err)
	}
	material, err := storev3.GetMaterialByID(ctx, s.st.Read(), attachment.MaterialID)
	if err != nil {
		return nil, Translate(err)
	}
	if s.materials == nil {
		return nil, conflict("material reader is not configured; cannot materialise %s %s", src.Name, taskID)
	}
	body, err := s.materials.Read(ctx, material.MaterialID)
	if err != nil {
		return nil, conflict("read plan of %s: %v", taskID, err)
	}

	var document planDocument
	switch src.Derive.Format {
	case "", "json":
		if err := json.Unmarshal(body, &document); err != nil {
			return nil, conflict("plan of %s is not valid JSON: %v", taskID, err)
		}
	case "yaml":
		if err := yaml.Unmarshal(body, &document); err != nil {
			return nil, conflict("plan of %s is not valid YAML: %v", taskID, err)
		}
	default:
		return nil, conflict("%s derives mode list with unsupported format %q", src.Name, src.Derive.Format)
	}
	if len(document.Items) == 0 {
		return nil, nil
	}

	seen := make(map[int]bool, len(document.Items))
	entries := make([]deriveEntry, 0, len(document.Items))
	for _, item := range document.Items {
		if item.SeqNo <= 0 {
			return nil, conflict("plan item has invalid seq_no %d", item.SeqNo)
		}
		if seen[item.SeqNo] {
			return nil, conflict("plan has duplicate seq_no %d", item.SeqNo)
		}
		seen[item.SeqNo] = true
		if strings.TrimSpace(item.Title) == "" {
			return nil, conflict("plan item %d has an empty title", item.SeqNo)
		}
		entry := deriveEntry{
			key:      strconv.Itoa(item.SeqNo),
			title:    item.Title,
			bodyMD:   item.BodyMD,
			priority: item.Priority,
			seqNo:    item.SeqNo,
		}
		for _, dependency := range item.DependsOn {
			entry.dependsOn = append(entry.dependsOn, strconv.Itoa(dependency))
		}
		entries = append(entries, entry)
	}
	// The plan is materialised in seq_no order, which is what makes a sibling
	// dependency resolvable: it may only point at an earlier entry.
	sort.Slice(entries, func(i, j int) bool { return entries[i].seqNo < entries[j].seqNo })
	return entries, nil
}

// hook returns the in-transaction half of the derivation. A nil derivation gives
// a nil hook, i.e. the approval commits without creating anything.
//
// The idempotency gate counts the children this upstream already produced
// (parent_task_id, dst kind): a retried approval skips the whole batch instead
// of duplicating it (§8.2 step 5). Any error rolls the approval back, so a
// half-created batch never survives and the card stays in in_review.
func (d *derivation) hook(s *Service) func(context.Context, *sql.Tx, storev3.Task) error {
	if d == nil {
		return nil
	}
	return func(ctx context.Context, tx *sql.Tx, approved storev3.Task) error {
		var existing int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM tasks WHERE parent_task_id = ? AND kind = ?`,
			approved.ID, d.dst.ID).Scan(&existing); err != nil {
			return fmt.Errorf("flow: count %s children of %s: %w", d.dst.Name, approved.TaskID, err)
		}
		if existing > 0 {
			d.skipped = true
			return nil
		}
		// A single-open stage keeps one unfinished card per project (§8.5): the
		// second derivation is skipped instead of colliding with
		// ux_tasks_open_per_kind, which would fail the approval with a 409 the
		// operator cannot act on.
		if d.dst.OpenCards != platform.OpenCardsMultiple {
			var active int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM tasks
				 WHERE project_id = ? AND kind = ? AND status IN (?, ?, ?, ?)`,
				approved.ProjectID, d.dst.ID,
				enum.Pending, enum.Ready, enum.InProgress, enum.InReview).Scan(&active); err != nil {
				return fmt.Errorf("flow: count active %s tasks: %w", d.dst.Name, err)
			}
			if active > 0 {
				d.skipped = true
				return nil
			}
		}

		// The templates may name the project and the product, so both are read
		// once per batch, inside the transaction that creates the batch.
		project, err := storev3.GetProject(ctx, tx, approved.ProjectCode)
		if err != nil {
			return Translate(err)
		}
		product, err := storedb.GetProductByID(ctx, tx, project.ProductID)
		if err != nil {
			return Translate(err)
		}

		d.upstream = approved.TaskID
		now := s.nowString()
		parallel := 0
		if d.dst.OpenCards == platform.OpenCardsMultiple {
			parallel = 1
		}

		// The provenance line is platform-generated and names the stages, not
		// their ids (AC7/AC11). A list derivation is the exception: the previous
		// release's breakdown rule created its dev cards without a context and
		// AC7 compares those cards field for field, so a list-derived card keeps
		// an empty context_md. Its provenance lives in the flow.auto_create
		// event instead (rule + stage + config + key).
		contextMD := ""
		if d.mode != platform.DeriveList {
			contextMD = fmt.Sprintf("由 %s 自动派生（%s）", approved.TaskID, d.rule())
		}

		// created maps an entry key onto the card it became; sibling
		// dependencies resolve through it and only ever point backwards.
		created := make(map[string]string, len(d.entries))
		for _, entry := range d.entries {
			deps := []string{approved.TaskID}
			for _, dependency := range entry.dependsOn {
				depID, ok := created[dependency]
				if !ok {
					return conflict("flow: %s entry %q depends on unknown or later entry %q",
						d.dst.Name, entry.key, dependency)
				}
				deps = append(deps, depID)
			}

			values := renderValues{
				upstreamID:    approved.TaskID,
				upstreamTitle: approved.Title,
				projectName:   project.Name,
				productName:   product.Name,
				stageName:     d.dst.LabelOrName(),
				itemTitle:     entry.title,
				itemKey:       entry.key,
				itemSeq:       entry.seqNo,
			}

			title, err := d.cardTitle(entry)
			if err != nil {
				return err
			}
			title = values.render(title)

			// §8.4: the task description has three levels (item body, stage task
			// template, empty); the flow-level fallback is gone with
			// default_description. The stage's human-readable description is not
			// one of them either (D2). A list entry body is the upstream agent's
			// prose, so it is used verbatim; every configured template is
			// rendered. A list derivation never falls back.
			description, source := s.flow.ResolveDescription(&d.dst, d.mode, &platform.DeriveItem{
				Key: entry.key, Title: entry.title, BodyMD: entry.bodyMD,
			})
			if d.mode != platform.DeriveList || source != platform.DescriptionFromItem {
				description = values.render(description)
			}

			taskID, err := s.newTaskID(now)
			if err != nil {
				return err
			}
			createdCard, err := storev3.CreateTaskTx(ctx, tx, storev3.CreateTaskInput{
				ProjectID:     approved.ProjectCode,
				TaskID:        taskID,
				ParentTaskID:  approved.TaskID,
				Kind:          enum.TaskKind(d.dst.ID),
				KindParallel:  &parallel,
				Title:         title,
				DescriptionMD: description,
				ContextMD:     contextMD,
				Priority:      entry.priority,
				MaxAttempts:   s.attemptLimit(0),
				DependsOn:     deps,
				Now:           now,
			})
			if err != nil {
				return err
			}
			if err := storev3.AppendEventTx(ctx, tx, createdCard.ID, enum.EventFlowAutoCreate, "flow",
				flowEventDetail(d.rule(), approved.TaskID, d.dst.Name, d.fingerprint, entry.key), now); err != nil {
				return err
			}
			if entry.key != "" {
				created[entry.key] = createdCard.TaskID
			}
			d.created++
		}
		return nil
	}
}

// cardTitle picks the title template of one derived card: the entry title when
// the entry has one, otherwise the target stage's title template.
//
// §8.2 and §8.4 disagree on this order; the engine follows §8.4 ("标题同构" with
// the description chain: entry first). For a single derivation the entry has no
// title, so both readings produce the same string, which is what AC7 compares.
func (d *derivation) cardTitle(entry deriveEntry) (string, error) {
	if strings.TrimSpace(entry.title) != "" {
		return entry.title, nil
	}
	if strings.TrimSpace(d.dst.Title) != "" {
		return d.dst.Title, nil
	}
	return "", conflict("flow: %s entry %q has no title and stage %s declares no title template",
		d.dst.Name, entry.key, d.dst.Name)
}

// flowEventDetail renders the flow.auto_create detail. Struct order fixes the
// JSON key order, and "rule" stays first so the historical rows and the new rows
// read the same way (§7.6 / AC11).
type flowEventDetailJSON struct {
	Rule     string `json:"rule"`
	Upstream string `json:"upstream"`
	Stage    string `json:"stage"`
	Config   string `json:"config"`
	Key      string `json:"key,omitempty"`
}

func flowEventDetail(rule, upstream, stage, config, key string) string {
	encoded, err := json.Marshal(flowEventDetailJSON{
		Rule: rule, Upstream: upstream, Stage: stage, Config: config, Key: key,
	})
	if err != nil {
		// The struct is a fixed shape of strings; an encoding failure cannot
		// happen and must not fail an approval silently.
		panic("flow: encode flow.auto_create detail: " + err.Error())
	}
	return string(encoded)
}

// logDerivation writes the post-commit line: names and the rule, never ids
// (§8.2 / D2‴).
func (d *derivation) log(s *Service) {
	if d == nil {
		return
	}
	if d.skipped {
		s.logger.Info("flow derivation skipped(existing_children)",
			"rule", d.rule(), "stage", d.dst.Name, "upstream", d.upstream)
		return
	}
	s.logger.Info("flow derived cards",
		"rule", d.rule(), "stage", d.dst.Name, "config", d.fingerprint,
		"upstream", d.upstream, "count", d.created)
}

func (s *Service) newTaskID(now string) (string, error) {
	// platform.NewULID takes a time; parse the service timestamp back so the id
	// and the row share one clock reading.
	t, err := time.Parse("2006-01-02T15:04:05.000Z", now)
	if err != nil {
		return "", fmt.Errorf("flow: parse time %q: %w", now, err)
	}
	ulid, err := platform.NewULID(t)
	if err != nil {
		return "", fmt.Errorf("flow: new task id: %w", err)
	}
	return "t_" + ulid, nil
}
