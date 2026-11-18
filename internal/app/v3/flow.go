package v3

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// MaterialReader reads a material body by business id. The flow engine needs
// the breakdown plan; wiring the real object store is the API layer's job, so
// this package only depends on the one-method interface.
type MaterialReader interface {
	Read(ctx context.Context, materialID string) ([]byte, error)
}

// WithAutoFlow turns the four derivation rules on or off (default on).
func (s *Service) WithAutoFlow(enabled bool) *Service {
	s.flowEnabled = enabled
	return s
}

// WithMaterialReader injects the object reader used by the breakdown rule.
func (s *Service) WithMaterialReader(reader MaterialReader) *Service {
	s.materials = reader
	return s
}

// planDocument mirrors the persisted breakdown plan artifact.
type planDocument struct {
	Items []planItem `json:"items"`
}

type planItem struct {
	SeqNo     int    `json:"seq_no"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Priority  int    `json:"priority"`
	DependsOn []int  `json:"depends_on"`
}

// approveHook builds the in-transaction hook for one approval. A nil hook means
// "no successor" (dev approval, flow disabled, or an empty plan).
func (s *Service) approveHook(ctx context.Context, task storev3.Task) (func(context.Context, *sql.Tx, storev3.Task) error, error) {
	if !s.flowEnabled {
		return nil, nil
	}
	switch task.Kind {
	case enum.Requirement:
		return s.ensureHook(enum.Design, "技术方案", "requirement->design"), nil
	case enum.Design:
		return s.ensureHook(enum.Breakdown, "任务拆分", "design->breakdown"), nil
	case enum.Breakdown:
		items, err := s.readPlan(ctx, task.TaskID)
		if err != nil {
			return nil, err
		}
		return s.breakdownHook(items), nil
	default:
		return nil, nil
	}
}

// ensureHook creates one successor task unless an active task of that kind
// already exists in the project.
func (s *Service) ensureHook(kind enum.TaskKind, title, rule string) func(context.Context, *sql.Tx, storev3.Task) error {
	return func(ctx context.Context, tx *sql.Tx, approved storev3.Task) error {
		var active int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM tasks
			 WHERE project_id = ? AND kind = ? AND status IN (?, ?, ?, ?)`,
			approved.ProjectID, kind, enum.Pending, enum.Ready, enum.InProgress, enum.InReview).
			Scan(&active); err != nil {
			return fmt.Errorf("flow: count active %s tasks: %w", kind, err)
		}
		if active > 0 {
			return nil
		}

		now := s.nowString()
		taskID, err := s.newTaskID(now)
		if err != nil {
			return err
		}
		created, err := storev3.CreateTaskTx(ctx, tx, storev3.CreateTaskInput{
			ProjectID:    approved.ProjectCode,
			TaskID:       taskID,
			ParentTaskID: approved.TaskID,
			Kind:         kind,
			Title:        title,
			ContextMD:    fmt.Sprintf("由 %s 自动派生（%s）", approved.TaskID, rule),
			DependsOn:    []string{approved.TaskID},
			Now:          now,
		})
		if err != nil {
			return err
		}
		return storev3.AppendEventTx(ctx, tx, created.ID, enum.EventFlowAutoCreate, "flow",
			fmt.Sprintf(`{"rule":%q,"upstream":%q}`, rule, approved.TaskID), now)
	}
}

// breakdownHook materialises the approved plan into dev tasks. A breakdown that
// was already materialised is left alone, so a retried approval cannot double
// the children.
func (s *Service) breakdownHook(items []planItem) func(context.Context, *sql.Tx, storev3.Task) error {
	return func(ctx context.Context, tx *sql.Tx, approved storev3.Task) error {
		var existing int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE parent_task_id = ? AND kind = ?`,
			approved.ID, enum.Dev).Scan(&existing); err != nil {
			return fmt.Errorf("flow: count dev children: %w", err)
		}
		if existing > 0 {
			return nil
		}

		now := s.nowString()
		sorted := append([]planItem(nil), items...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].SeqNo < sorted[j].SeqNo })

		bySeq := make(map[int]string, len(sorted))
		for _, item := range sorted {
			deps := []string{approved.TaskID}
			for _, seq := range item.DependsOn {
				depID, ok := bySeq[seq]
				if !ok {
					return conflict("flow: plan item %d depends on unknown or later item %d", item.SeqNo, seq)
				}
				deps = append(deps, depID)
			}

			taskID, err := s.newTaskID(now)
			if err != nil {
				return err
			}
			created, err := storev3.CreateTaskTx(ctx, tx, storev3.CreateTaskInput{
				ProjectID:     approved.ProjectCode,
				TaskID:        taskID,
				ParentTaskID:  approved.TaskID,
				Kind:          enum.Dev,
				Title:         item.Title,
				DescriptionMD: item.BodyMD,
				Priority:      item.Priority,
				DependsOn:     deps,
				Now:           now,
			})
			if err != nil {
				return err
			}
			if err := storev3.AppendEventTx(ctx, tx, created.ID, enum.EventFlowAutoCreate, "flow",
				fmt.Sprintf(`{"rule":"breakdown->dev","upstream":%q,"seq_no":%d}`, approved.TaskID, item.SeqNo), now); err != nil {
				return err
			}
			bySeq[item.SeqNo] = created.TaskID
		}
		return nil
	}
}

// readPlan loads and validates the breakdown output artifact.
func (s *Service) readPlan(ctx context.Context, taskID string) ([]planItem, error) {
	attachment, err := storev3.LatestOutput(ctx, s.st.Read(), taskID)
	if err != nil {
		return nil, conflict("breakdown %s has no plan output: %v", taskID, err)
	}
	material, err := storev3.GetMaterialByID(ctx, s.st.Read(), attachment.MaterialID)
	if err != nil {
		return nil, Translate(err)
	}
	if s.materials == nil {
		return nil, conflict("material reader is not configured; cannot materialise breakdown %s", taskID)
	}
	body, err := s.materials.Read(ctx, material.MaterialID)
	if err != nil {
		return nil, conflict("read plan of %s: %v", taskID, err)
	}

	var document planDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, conflict("plan of %s is not valid JSON: %v", taskID, err)
	}
	if len(document.Items) == 0 {
		return nil, nil
	}

	seen := make(map[int]bool, len(document.Items))
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
	}
	return document.Items, nil
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
