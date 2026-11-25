package v3

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// flowFromYAML parses a flow through the real startup path: the YAML reader and
// R1–R17 validation are the ones the process runs, so a test cannot pin a flow
// the platform would refuse to start with.
func flowFromYAML(t *testing.T, body string) platform.FlowConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echuan.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := platform.Load(path)
	if err != nil {
		t.Fatalf("platform.Load: %v", err)
	}
	return cfg.Flow
}

// approveTask drives one seeded task through claim → report → approve.
func approveTask(t *testing.T, svc *Service, st *storedb.Store, taskID, workerID string, kind enum.TaskKind) storev3.Task {
	t.Helper()
	ctx := context.Background()
	seedWorker(t, st, workerID)
	materialID := createMaterial(t, svc, "m_"+taskID)
	claimed, err := svc.Claim(ctx, workerID, kind)
	if err != nil {
		t.Fatalf("Claim %s: %v", taskID, err)
	}
	if claimed.TaskID != taskID {
		t.Fatalf("claimed %s, want %s", claimed.TaskID, taskID)
	}
	if _, err := svc.Report(ctx, taskID, *claimed.LeaseID, materialID); err != nil {
		t.Fatalf("Report %s: %v", taskID, err)
	}
	decided, err := svc.Decide(ctx, DecideRequest{
		TaskID: taskID, Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	})
	if err != nil {
		t.Fatalf("Decide %s: %v", taskID, err)
	}
	return decided
}

// defaultPlusSafetyReview is the AC1/AC3/AC4 configuration: the built-in chain
// with dev handing over to a new stage whose templates name the upstream card.
const defaultPlusSafetyReview = `flow:
  auto: true
  stages:
    - { id: 1, name: requirement, label: 需求, material: requirement, derive: { to: design, mode: single } }
    - { id: 2, name: design, label: 方案, material: design, title: "技术方案", description: "", derive: { to: breakdown, mode: single } }
    - { id: 3, name: breakdown, label: 拆解, material: plan, title: "任务拆分", description: "", derive: { to: dev, mode: list, source: artifact, format: json } }
    - id: 4
      name: dev
      label: 开发
      material: report
      open_cards: multiple
      closing: true
      derive: { to: safety_review, mode: single }
    - id: 5
      name: safety_review
      label: 安全评审
      material: report
      title: "安全评审：{上游卡标题}"
      description: "针对 {上游卡号} 做安全评审"
      derive: { mode: none }
`

// TestDefaultFlowDerivationMatchesSnapshot pins AC2/AC7 field by field: with the
// built-in default flow (no flow section) every derived card keeps the exact
// title, description, context, dependency and kind the previous release
// produced (the 2db5b993 snapshot).
func TestDefaultFlowDerivationMatchesSnapshot(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaterialReader(fakeReader{bodies: map[string][]byte{
		"m_plan": planBody(
			planItem{SeqNo: 1, Title: "A", BodyMD: "a", Priority: 1},
			planItem{SeqNo: 2, Title: "B", BodyMD: "b", DependsOn: []int{1}},
		),
	}})
	projectID := seedProject(t, st, "APP")

	// requirement → design
	createReadyTask(t, svc, projectID, "t_req", enum.Requirement)
	approveTask(t, svc, st, "t_req", "w_req", enum.Requirement)
	designs := listKind(t, svc, projectID, enum.Design)
	if len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
	design := designs[0]
	if design.Title != "技术方案" {
		t.Errorf("design title = %q, want 技术方案", design.Title)
	}
	if design.DescriptionMD != "" {
		t.Errorf("design description_md = %q, want empty", design.DescriptionMD)
	}
	if design.ContextMD != "由 t_req 自动派生（requirement->design）" {
		t.Errorf("design context_md = %q", design.ContextMD)
	}
	if design.Kind != enum.Design || design.KindParallel != 0 {
		t.Errorf("design kind=%d kind_parallel=%d, want 2/0", design.Kind, design.KindParallel)
	}
	if deps := taskDeps(t, st, design.TaskID); len(deps) != 1 || deps[0] != "t_req" {
		t.Errorf("design depends_on = %v, want [t_req]", deps)
	}
	// AC11: the audit rule string of the default flow stays the historical
	// "requirement->design" (stage names, not ids, not labels).
	events, err := svc.ListEvents(context.Background(), design.TaskID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var rule string
	for _, event := range events {
		if event.Event != enum.EventFlowAutoCreate {
			continue
		}
		var detail flowEventDetailJSON
		if err := json.Unmarshal([]byte(event.DetailJSON), &detail); err != nil {
			t.Fatalf("event detail %q: %v", event.DetailJSON, err)
		}
		rule = detail.Rule
	}
	if rule != "requirement->design" {
		t.Errorf("flow.auto_create rule = %q, want requirement->design", rule)
	}

	// design → breakdown
	approveTask(t, svc, st, design.TaskID, "w_design", enum.Design)
	breaks := listKind(t, svc, projectID, enum.Breakdown)
	if len(breaks) != 1 {
		t.Fatalf("breakdown tasks = %d, want 1", len(breaks))
	}
	breakdown := breaks[0]
	if breakdown.Title != "任务拆分" || breakdown.DescriptionMD != "" {
		t.Errorf("breakdown title/description = %q/%q, want 任务拆分/empty", breakdown.Title, breakdown.DescriptionMD)
	}
	if breakdown.ContextMD != "由 "+design.TaskID+" 自动派生（design->breakdown）" {
		t.Errorf("breakdown context_md = %q", breakdown.ContextMD)
	}

	// breakdown → N dev cards, one per plan item
	seedWorker(t, st, "w_break")
	planID, err := svc.CreateMaterial(context.Background(), CreateMaterialRequest{
		MaterialID: "m_plan", Kind: enum.MaterialPlan, Version: 1, Title: "方案",
		Sha256: "sha", Size: 1, Path: "/tmp/m_plan", UploadedBy: "cto",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}
	claimed, err := svc.Claim(context.Background(), "w_break", enum.Breakdown)
	if err != nil {
		t.Fatalf("Claim breakdown: %v", err)
	}
	if _, err := svc.Report(context.Background(), breakdown.TaskID, *claimed.LeaseID, planID); err != nil {
		t.Fatalf("Report breakdown: %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: breakdown.TaskID, Decision: enum.Approve, MaterialID: planID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide breakdown: %v", err)
	}

	devs := listKind(t, svc, projectID, enum.Dev)
	if len(devs) != 2 {
		t.Fatalf("dev tasks = %d, want 2", len(devs))
	}
	byTitle := map[string]storev3.Task{}
	for _, dev := range devs {
		byTitle[dev.Title] = dev
	}
	a := byTitle["A"]
	// The previous release's breakdown rule created its dev cards without a
	// context line and AC7 compares those cards field for field, so a
	// list-derived card keeps context_md empty.
	if a.DescriptionMD != "a" || a.ContextMD != "" {
		t.Errorf("dev A description/context = %q/%q, want a/empty", a.DescriptionMD, a.ContextMD)
	}
	if a.Kind != enum.Dev || a.KindParallel != 1 || a.Priority != 1 {
		t.Errorf("dev A kind=%d kind_parallel=%d priority=%d, want 4/1/1", a.Kind, a.KindParallel, a.Priority)
	}
	b := byTitle["B"]
	if b.DescriptionMD != "b" {
		t.Errorf("dev B description_md = %q, want b", b.DescriptionMD)
	}
	if deps := taskDeps(t, st, b.TaskID); len(deps) != 2 || !containsString(deps, breakdown.TaskID) || !containsString(deps, a.TaskID) {
		t.Errorf("dev B depends_on = %v, want breakdown + A", deps)
	}
}

// TestCreateTaskStampsKindParallel covers the integration point of §12: the
// manual creation path and the derivation path take open_cards from the same
// flow snapshot, so a stage that allows several cards can actually hold them.
func TestCreateTaskStampsKindParallel(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, defaultPlusSafetyReview))
	projectID := seedProject(t, st, "APP")

	ctx := context.Background()
	dev, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_dev1", Kind: enum.Dev, Title: "一",
	})
	if err != nil {
		t.Fatalf("CreateTask dev: %v", err)
	}
	if dev.KindParallel != 1 {
		t.Errorf("dev kind_parallel = %d, want 1 (open_cards multiple)", dev.KindParallel)
	}
	second, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_dev2", Kind: enum.Dev, Title: "二",
	})
	if err != nil {
		t.Fatalf("second dev card: %v", err)
	}
	if second.KindParallel != 1 {
		t.Errorf("second dev kind_parallel = %d, want 1", second.KindParallel)
	}

	tests, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_test1", Kind: enum.TaskKind(5), Title: "评审",
	})
	if err != nil {
		t.Fatalf("CreateTask safety_review: %v", err)
	}
	if tests.KindParallel != 0 {
		t.Errorf("safety_review kind_parallel = %d, want 0 (open_cards single)", tests.KindParallel)
	}
	if _, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_test2", Kind: enum.TaskKind(5), Title: "评审 2",
	}); StatusOf(err) != 409 {
		t.Fatalf("second single-open card = %d (%v), want 409", StatusOf(err), err)
	}
}

// TestSecurityReviewDerivation covers AC3/AC4/AC11: a new stage's title and
// description templates are rendered into complete strings, the card lands under
// the new kind id, and the audit event names the stage and the configuration.
func TestSecurityReviewDerivation(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	flow := flowFromYAML(t, defaultPlusSafetyReview)
	svc.WithFlow(flow)
	projectID := seedProject(t, st, "APP")

	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_dev", Kind: enum.Dev, Title: "实现登录",
	}); err != nil {
		t.Fatalf("CreateTask dev: %v", err)
	}
	approveTask(t, svc, st, "t_dev", "w_dev", enum.Dev)

	cards := listKind(t, svc, projectID, enum.TaskKind(5))
	if len(cards) != 1 {
		t.Fatalf("safety_review tasks = %d, want 1", len(cards))
	}
	card := cards[0]
	// AC3/AC4 are verbatim comparisons: the whole rendered string, not a
	// substring and not just "non-empty".
	if card.Title != "安全评审：实现登录" {
		t.Errorf("title = %q, want 安全评审：实现登录", card.Title)
	}
	if card.DescriptionMD != "针对 t_dev 做安全评审" {
		t.Errorf("description_md = %q, want 针对 t_dev 做安全评审", card.DescriptionMD)
	}
	if card.ContextMD != "由 t_dev 自动派生（dev->safety_review）" {
		t.Errorf("context_md = %q", card.ContextMD)
	}
	if card.KindParallel != 0 {
		t.Errorf("kind_parallel = %d, want 0 (single)", card.KindParallel)
	}
	if deps := taskDeps(t, st, card.TaskID); len(deps) != 1 || deps[0] != "t_dev" {
		t.Errorf("depends_on = %v, want [t_dev]", deps)
	}
	if card.ParentCode == nil || *card.ParentCode != "t_dev" {
		t.Errorf("parent_task_id = %v, want t_dev", card.ParentCode)
	}

	events, err := svc.ListEvents(context.Background(), card.TaskID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var detail flowEventDetailJSON
	found := false
	for _, event := range events {
		if event.Event != enum.EventFlowAutoCreate {
			continue
		}
		found = true
		if err := json.Unmarshal([]byte(event.DetailJSON), &detail); err != nil {
			t.Fatalf("event detail %q: %v", event.DetailJSON, err)
		}
	}
	if !found {
		t.Fatal("flow.auto_create event missing")
	}
	if detail.Rule != "dev->safety_review" || detail.Stage != "safety_review" || detail.Upstream != "t_dev" {
		t.Errorf("event detail = %+v, want rule/stage/upstream", detail)
	}
	if detail.Config != flow.Fingerprint() {
		t.Errorf("event config = %q, want fingerprint %q", detail.Config, flow.Fingerprint())
	}
}

// TestFanoutDerivation covers AC4 for a fanout: three cards at once, their
// titles, the sibling dependencies and the target's open_cards snapshot. A
// retried approval must not double the batch.
func TestFanoutDerivation(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, `flow:
  auto: true
  stages:
    - { id: 1, name: requirement, label: 需求, material: requirement, derive: { to: design, mode: single } }
    - id: 2
      name: design
      label: 方案
      material: design
      title: "技术方案"
      description: ""
      derive:
        to: review
        mode: fanout
        items:
          - { key: security, title: "安全评审：{上游卡标题}" }
          - { key: perf, title: "性能评审：{上游卡标题}", depends_on: [security] }
          - { key: compat, title: "兼容性评审（{条目key}）：{上游卡标题}", depends_on: [security] }
    - { id: 5, name: review, label: 评审, material: report, open_cards: multiple, description: "评审 {上游卡号}", derive: { mode: none } }
`))
	projectID := seedProject(t, st, "APP")

	createReadyTask(t, svc, projectID, "t_design", enum.Design)
	approveTask(t, svc, st, "t_design", "w_design", enum.Design)

	cards := listKind(t, svc, projectID, enum.TaskKind(5))
	if len(cards) != 3 {
		t.Fatalf("review tasks = %d, want 3", len(cards))
	}
	byTitle := map[string]storev3.Task{}
	for _, card := range cards {
		byTitle[card.Title] = card
		if card.KindParallel != 1 {
			t.Errorf("%q kind_parallel = %d, want 1 (open_cards multiple)", card.Title, card.KindParallel)
		}
		if card.DescriptionMD != "评审 t_design" {
			t.Errorf("%q description_md = %q, want 评审 t_design", card.Title, card.DescriptionMD)
		}
	}
	security, ok := byTitle["安全评审：t_design"]
	if !ok {
		t.Fatalf("titles = %v, want the three rendered titles", mapKeys(byTitle))
	}
	perf, ok := byTitle["性能评审：t_design"]
	if !ok {
		t.Fatalf("titles = %v, want the three rendered titles", mapKeys(byTitle))
	}
	// An entry-level variable ({条目key}) renders too, which is what makes a
	// fanout entry able to name itself.
	if _, ok := byTitle["兼容性评审（compat）：t_design"]; !ok {
		t.Fatalf("titles = %v, want the three rendered titles", mapKeys(byTitle))
	}

	if deps := taskDeps(t, st, security.TaskID); len(deps) != 1 || deps[0] != "t_design" {
		t.Errorf("security depends_on = %v, want [t_design]", deps)
	}
	// ListDependencies sorts by business id, so the pair is compared as a set:
	// the engine writes [upstream, sibling] and the read path reorders it.
	if deps := taskDeps(t, st, perf.TaskID); len(deps) != 2 || !containsString(deps, "t_design") || !containsString(deps, security.TaskID) {
		t.Errorf("perf depends_on = %v, want t_design + security", deps)
	}

	// A fanout event carries the entry key so an operator can tell the three
	// cards of one batch apart (§8.2 step 6).
	events, err := svc.ListEvents(context.Background(), perf.TaskID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var detail flowEventDetailJSON
	for _, event := range events {
		if event.Event == enum.EventFlowAutoCreate {
			if err := json.Unmarshal([]byte(event.DetailJSON), &detail); err != nil {
				t.Fatalf("event detail %q: %v", event.DetailJSON, err)
			}
		}
	}
	if detail.Key != "perf" || detail.Rule != "design->review" {
		t.Errorf("event detail = %+v, want key=perf rule=design->review", detail)
	}

	// A retried approval is idempotent: it fails as an invalid transition and
	// the batch stays at three cards.
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_design", Decision: enum.Approve, DecidedBy: "cto",
	}); StatusOf(err) != 409 {
		t.Fatalf("second approve = %d (%v), want 409", StatusOf(err), err)
	}
	if again := listKind(t, svc, projectID, enum.TaskKind(5)); len(again) != 3 {
		t.Fatalf("review tasks after retry = %d, want 3", len(again))
	}
}

// TestAutoFalseDerivesNothing covers AC8: the auto switch is the process-wide
// gate, so an approval creates no card at all.
func TestAutoFalseDerivesNothing(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, strings.Replace(defaultPlusSafetyReview, "auto: true", "auto: false", 1)))
	projectID := seedProject(t, st, "APP")

	createReadyTask(t, svc, projectID, "t_req", enum.Requirement)
	approveTask(t, svc, st, "t_req", "w_req", enum.Requirement)

	all, err := svc.ListTasks(context.Background(), storev3.TaskFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("tasks = %d, want 1 with auto: false", len(all))
	}
}

// TestListDerivationDoesNotFallBack covers §8.4's list rule: a plan entry with an
// empty body_md keeps an empty description even when the flow declares a
// default_description, while a single/fanout derivation does fall back to it.
func TestListDerivationDoesNotFallBack(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, `flow:
  auto: true
  default_description: "请按上游交付物完成本环节工作"
  stages:
    - { id: 1, name: requirement, label: 需求, material: requirement, derive: { to: design, mode: single } }
    - { id: 2, name: design, label: 方案, material: design, title: "技术方案", derive: { to: breakdown, mode: single } }
    - { id: 3, name: breakdown, label: 拆解, material: plan, title: "任务拆分", derive: { to: dev, mode: list, source: artifact, format: json } }
    - { id: 4, name: dev, label: 开发, material: report, open_cards: multiple, closing: true, derive: { mode: none } }
`))
	// Every card's report material answers the same plan document, so the chain
	// can be driven end to end through the real derivation of each stage.
	svc.WithMaterialReader(fakeReader{fallback: planBody(
		planItem{SeqNo: 1, Title: "有说明", BodyMD: "照这个做"},
		planItem{SeqNo: 2, Title: "没说明"},
	)})
	projectID := seedProject(t, st, "APP")

	// requirement → design: single, no stage description → flow default.
	createReadyTask(t, svc, projectID, "t_req", enum.Requirement)
	approveTask(t, svc, st, "t_req", "w_req", enum.Requirement)
	designs := listKind(t, svc, projectID, enum.Design)
	if len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
	if designs[0].DescriptionMD != "请按上游交付物完成本环节工作" {
		t.Errorf("design description_md = %q, want the flow default", designs[0].DescriptionMD)
	}

	// design → breakdown: single, stage description not configured → default.
	approveTask(t, svc, st, designs[0].TaskID, "w_design", enum.Design)
	breaks := listKind(t, svc, projectID, enum.Breakdown)
	if len(breaks) != 1 {
		t.Fatalf("breakdown tasks = %d, want 1", len(breaks))
	}
	approveTask(t, svc, st, breaks[0].TaskID, "w_break", enum.Breakdown)

	// breakdown → dev: list entries decide, the flow default is not a fallback.
	devs := listKind(t, svc, projectID, enum.Dev)
	if len(devs) != 2 {
		t.Fatalf("dev tasks = %d, want 2", len(devs))
	}
	byTitle := map[string]storev3.Task{}
	for _, dev := range devs {
		byTitle[dev.Title] = dev
	}
	if got := byTitle["有说明"].DescriptionMD; got != "照这个做" {
		t.Errorf("dev 有说明 description_md = %q, want 照这个做", got)
	}
	if got := byTitle["没说明"].DescriptionMD; got != "" {
		t.Errorf("dev 没说明 description_md = %q, want empty (list does not fall back)", got)
	}
}

// TestListDerivedContextStaysEmpty pins the one rule AC2 turns on: a list
// derivation does not write the provenance line, while single and fanout still
// do. The baseline (2db5b993) created breakdown→dev cards without a context, so
// their context_md is the empty string, verbatim.
func TestListDerivedContextStaysEmpty(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	// Every report material answers the same plan, so the whole built-in chain
	// runs through the real derivations.
	svc.WithMaterialReader(fakeReader{fallback: planBody(
		planItem{SeqNo: 1, Title: "A", BodyMD: "a"},
		planItem{SeqNo: 2, Title: "B", BodyMD: "b"},
	)})
	projectID := seedProject(t, st, "APP")

	createReadyTask(t, svc, projectID, "t_req", enum.Requirement)
	approveTask(t, svc, st, "t_req", "w_req", enum.Requirement)
	designs := listKind(t, svc, projectID, enum.Design)
	if len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
	// Contrast: a single derivation still writes the line.
	if want := "由 t_req 自动派生（requirement->design）"; designs[0].ContextMD != want {
		t.Errorf("design context_md = %q, want %q", designs[0].ContextMD, want)
	}
	approveTask(t, svc, st, designs[0].TaskID, "w_design", enum.Design)
	breaks := listKind(t, svc, projectID, enum.Breakdown)
	if len(breaks) != 1 {
		t.Fatalf("breakdown tasks = %d, want 1", len(breaks))
	}
	approveTask(t, svc, st, breaks[0].TaskID, "w_break", enum.Breakdown)

	devs := listKind(t, svc, projectID, enum.Dev)
	if len(devs) != 2 {
		t.Fatalf("dev tasks = %d, want 2", len(devs))
	}
	for _, dev := range devs {
		if dev.ContextMD != "" {
			t.Errorf("dev %q context_md = %q, want empty (list writes no provenance line)", dev.Title, dev.ContextMD)
		}
		// The provenance still exists, in the event detail.
		events, err := svc.ListEvents(context.Background(), dev.TaskID, 10)
		if err != nil {
			t.Fatalf("ListEvents %s: %v", dev.TaskID, err)
		}
		var detail flowEventDetailJSON
		found := false
		for _, event := range events {
			if event.Event != enum.EventFlowAutoCreate {
				continue
			}
			found = true
			if err := json.Unmarshal([]byte(event.DetailJSON), &detail); err != nil {
				t.Fatalf("event detail %q: %v", event.DetailJSON, err)
			}
		}
		if !found {
			t.Fatalf("dev %q has no flow.auto_create event", dev.Title)
		}
		if detail.Rule != "breakdown->dev" || detail.Stage != "dev" || detail.Config == "" || detail.Key == "" {
			t.Errorf("dev %q event detail = %+v, want rule/stage/config/key", dev.Title, detail)
		}
	}
}

// TestUnknownKindSkipsDerivation covers §8.5: a card whose stage was removed from
// the configuration still approves, it just derives nothing.
func TestUnknownKindSkipsDerivation(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")

	// Kind 9 is not in the built-in default flow.
	createReadyTask(t, svc, projectID, "t_old", enum.TaskKind(9))
	approveTask(t, svc, st, "t_old", "w_old", enum.TaskKind(9))

	all, err := svc.ListTasks(context.Background(), storev3.TaskFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("tasks = %d, want 1 (unknown kind derives nothing)", len(all))
	}
	if all[0].Status != enum.Done {
		t.Errorf("status = %s, want done (approval still succeeds)", all[0].Status)
	}
}

// TestUnknownSiblingDependencyRollsBackBatch covers §8.5's "多卡派到一半失败":
// an unresolvable sibling dependency rolls the whole approval back, so the
// upstream card stays in in_review and no card of the batch survives.
func TestUnknownSiblingDependencyRollsBackBatch(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, `flow:
  auto: true
  stages:
    - { id: 1, name: requirement, label: 需求, material: requirement, derive: { to: design, mode: single } }
    - id: 2
      name: design
      label: 方案
      material: design
      title: "技术方案"
      description: ""
      derive:
        to: review
        mode: fanout
        items:
          - { key: security, title: "安全评审", depends_on: [missing] }
    - { id: 5, name: review, label: 评审, material: report, open_cards: multiple, description: "评审", derive: { mode: none } }
`))
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_design", enum.Design)

	seedWorker(t, st, "w_design")
	materialID := createMaterial(t, svc, "m_t_design")
	claimed, err := svc.Claim(context.Background(), "w_design", enum.Design)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_design", *claimed.LeaseID, materialID); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_design", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	}); StatusOf(err) != 409 {
		t.Fatalf("approve = %d (%v), want 409", StatusOf(err), err)
	}

	upstream, err := svc.GetTask(context.Background(), "t_design")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if upstream.Status != enum.InReview {
		t.Errorf("upstream status = %s, want in_review (whole batch rolled back)", upstream.Status)
	}
	if cards := listKind(t, svc, projectID, enum.TaskKind(5)); len(cards) != 0 {
		t.Errorf("review tasks = %d, want 0 after rollback", len(cards))
	}
}

func taskDeps(t *testing.T, st *storedb.Store, taskID string) []string {
	t.Helper()
	deps, err := storev3.ListDependencies(context.Background(), st.Read(), taskID)
	if err != nil {
		t.Fatalf("ListDependencies %s: %v", taskID, err)
	}
	return deps
}

func mapKeys(values map[string]storev3.Task) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
