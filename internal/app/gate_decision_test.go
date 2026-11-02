package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// gateDecisionRow is the raw persisted form used to assert what was written;
// it deliberately bypasses the read model so a mapping bug cannot hide.
type gateDecisionRow struct {
	SubjectType     string
	SubjectID       string
	Gate            string
	MaterialID      *string
	MaterialVersion *int
	Decision        string
	DecidedBy       string
	Reason          *string
	CreateTime      string
}

func readGateDecisions(t *testing.T, st *store.Store) []gateDecisionRow {
	t.Helper()
	rows, err := st.Read().QueryContext(context.Background(), `
		SELECT gd.subject_type, gd.subject_id, gd.gate, m.material_id, m.version,
		       gd.decision, gd.decided_by, gd.reason, gd.create_time
		  FROM gate_decisions gd
		  LEFT JOIN materials m ON m.id = gd.material_id
		 ORDER BY gd.id ASC`)
	if err != nil {
		t.Fatalf("read gate decisions: %v", err)
	}
	defer rows.Close()

	var out []gateDecisionRow
	for rows.Next() {
		var row gateDecisionRow
		if err := rows.Scan(&row.SubjectType, &row.SubjectID, &row.Gate, &row.MaterialID,
			&row.MaterialVersion, &row.Decision, &row.DecidedBy, &row.Reason, &row.CreateTime); err != nil {
			t.Fatalf("scan gate decision: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate gate decisions: %v", err)
	}
	return out
}

func requireMaterial(t *testing.T, row gateDecisionRow, wantID string, wantVersion int) {
	t.Helper()
	if row.MaterialID == nil || *row.MaterialID != wantID {
		t.Fatalf("material_id = %v, want %q", row.MaterialID, wantID)
	}
	if row.MaterialVersion == nil || *row.MaterialVersion != wantVersion {
		t.Fatalf("material version = %v, want %d", row.MaterialVersion, wantVersion)
	}
}

// TestProjectGateDecisionsBindVersions covers acceptance 1, 2 and 3 for project
// gates: a reject and an approve each write exactly one row with the reviewed
// material version, and a later requirement v2 does not rewrite the v1 record.
func TestProjectGateDecisionsBindVersions(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         "门禁记录",
		RequirementMD: "# 需求 v1\n",
		UploadedBy:    "human:pm",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	projectID := detail.Project.ProjectID
	v1 := detail.Requirement.MaterialID

	if _, err := env.service.SubmitProject(context.Background(), projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject: %v", err)
	}
	state := env.projectState(t, projectID)
	rejected, err := env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionReject,
		Reason:        "需求缺少验收标准",
		ExpectVersion: &state.Version,
		Actor:         "human:cto",
	})
	if err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	if rejected.Material == nil || rejected.Material.MaterialID != v1 || rejected.Material.Version != 1 {
		t.Fatalf("reject material = %+v, want %s v1", rejected.Material, v1)
	}

	// Still in requirement_review, so a new requirement version is legal. The
	// already written reject must keep pointing at v1.
	rewritten, err := env.service.WriteRequirement(context.Background(), projectID, WriteRequirementInput{
		BodyMD:     "# 需求 v2\n",
		UploadedBy: "human:pm",
	})
	if err != nil {
		t.Fatalf("WriteRequirement: %v", err)
	}
	v2 := rewritten.Requirement.MaterialID
	if v2 == v1 {
		t.Fatalf("requirement v2 material = %q, want a new id", v2)
	}

	// A reject returns the project to the draft; submit the rewritten
	// requirement so the next verdict happens at the same gate.
	if _, err := env.service.SubmitProject(context.Background(), projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject(v2): %v", err)
	}
	state = env.projectState(t, projectID)
	approved, err := env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: &state.Version,
		Actor:         "human:cto",
	})
	if err != nil {
		t.Fatalf("Decide(approve): %v", err)
	}
	if approved.Material == nil || approved.Material.MaterialID != v2 || approved.Material.Version != 2 {
		t.Fatalf("approve material = %+v, want %s v2", approved.Material, v2)
	}

	rows := readGateDecisions(t, env.store)
	if len(rows) != 2 {
		t.Fatalf("gate decisions = %d, want 2", len(rows))
	}
	requireMaterial(t, rows[0], v1, 1)
	if rows[0].SubjectType != store.GateSubjectProject || rows[0].SubjectID != projectID {
		t.Errorf("subject = %s/%s, want project/%s", rows[0].SubjectType, rows[0].SubjectID, projectID)
	}
	if rows[0].Gate != "requirement" || rows[0].Decision != "reject" {
		t.Errorf("gate/decision = %s/%s, want requirement/reject", rows[0].Gate, rows[0].Decision)
	}
	if rows[0].DecidedBy != "human:cto" {
		t.Errorf("decided_by = %q, want human:cto", rows[0].DecidedBy)
	}
	if rows[0].Reason == nil || *rows[0].Reason != "需求缺少验收标准" {
		t.Errorf("reason = %v", rows[0].Reason)
	}
	if rows[0].CreateTime == "" {
		t.Error("create_time is empty")
	}
	requireMaterial(t, rows[1], v2, 2)
	if rows[1].Decision != "approve" {
		t.Errorf("decision = %q, want approve", rows[1].Decision)
	}

	// ---- history: newest first, paginated, filterable --------------------
	page, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListGateDecisions: %v", err)
	}
	if len(page.Items) != 1 || page.NextID == 0 {
		t.Fatalf("first page = %+v, want 1 item and a cursor", page)
	}
	if page.Items[0].MaterialID != v2 || page.Items[0].MaterialVersion != 2 {
		t.Errorf("first page material = %+v, want %s v2", page.Items[0], v2)
	}

	second, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, Limit: 1, AfterID: page.NextID,
	})
	if err != nil {
		t.Fatalf("ListGateDecisions(second): %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].MaterialID != v1 || second.NextID != 0 {
		t.Fatalf("second page = %+v, want the v1 record and no cursor", second)
	}

	bySubject, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, SubjectType: store.GateSubjectProject, Limit: 50,
	})
	if err != nil || len(bySubject.Items) != 2 {
		t.Fatalf("subject_type filter = %+v, %v; want 2 items", bySubject, err)
	}
	none, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, SubjectType: store.GateSubjectWorkItem, Limit: 50,
	})
	if err != nil || len(none.Items) != 0 {
		t.Fatalf("work_item filter = %+v, %v; want empty", none, err)
	}

	if _, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: "NOPE", Limit: 10,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown project error = %v, want not found", err)
	}
	if _, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, Limit: 0,
	}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("limit 0 error = %v, want invalid argument", err)
	}
	if _, err := env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: projectID, SubjectType: "task", Limit: 10,
	}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("subject_type error = %v, want invalid argument", err)
	}
}

// TestWorkItemGateDecisions covers acceptance 1 and 2 for single-card verdicts:
// approve and reject each append one row bound to the card's result material.
func TestWorkItemGateDecisions(t *testing.T) {
	f := newWorkItemFixture(t)

	var (
		specPhysical int64
		specCode     string
		specVersion  int
	)
	if err := f.env.store.Read().QueryRowContext(context.Background(), `
		SELECT m.id, m.material_id, m.version
		  FROM projects p JOIN materials m ON m.id = p.spec_material_id
		 WHERE p.project_id = ?`, f.projectID).Scan(&specPhysical, &specCode, &specVersion); err != nil {
		t.Fatalf("read spec material: %v", err)
	}

	submitCard := func(workID string, seq int) {
		t.Helper()
		id := f.env.insertWorkItem(t, f.projectID, workID, seq, store.WorkItemStateSubmitted, 2)
		if _, err := f.env.store.Write().ExecContext(context.Background(),
			`UPDATE work_items SET result_material_id = ? WHERE id = ?`, specPhysical, id); err != nil {
			t.Fatalf("bind result material: %v", err)
		}
	}
	submitCard("w_gate_approve", 10)
	submitCard("w_gate_reject", 11)

	approved, err := f.items.DecideWorkItem(context.Background(), WorkItemDecisionInput{
		WorkID:   "w_gate_approve",
		Decision: "approve",
		Actor:    "human:qa",
	})
	if err != nil {
		t.Fatalf("DecideWorkItem(approve): %v", err)
	}
	if approved.Material == nil || approved.Material.MaterialID != specCode {
		t.Fatalf("approve material = %+v, want %s", approved.Material, specCode)
	}
	if approved.Detail.Item.State != store.WorkItemStateAccepted {
		t.Errorf("state = %q, want accepted", approved.Detail.Item.State)
	}

	rejected, err := f.items.DecideWorkItem(context.Background(), WorkItemDecisionInput{
		WorkID:   "w_gate_reject",
		Decision: "reject",
		Reason:   "返回体字段缺失",
		Actor:    "human:qa",
	})
	if err != nil {
		t.Fatalf("DecideWorkItem(reject): %v", err)
	}
	if rejected.Material == nil || rejected.Material.MaterialID != specCode {
		t.Fatalf("reject material = %+v, want %s", rejected.Material, specCode)
	}

	var rows []gateDecisionRow
	for _, row := range readGateDecisions(t, f.env.store) {
		if row.SubjectType == store.GateSubjectWorkItem {
			rows = append(rows, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("work-item gate decisions = %d, want 2", len(rows))
	}
	for i, wantDecision := range []string{"approve", "reject"} {
		row := rows[i]
		requireMaterial(t, row, specCode, specVersion)
		if row.SubjectType != store.GateSubjectWorkItem {
			t.Errorf("row %d subject_type = %q, want work_item", i, row.SubjectType)
		}
		if row.Gate != "dev" {
			t.Errorf("row %d gate = %q, want dev", i, row.Gate)
		}
		if row.Decision != wantDecision {
			t.Errorf("row %d decision = %q, want %q", i, row.Decision, wantDecision)
		}
		if row.DecidedBy != "human:qa" {
			t.Errorf("row %d decided_by = %q, want human:qa", i, row.DecidedBy)
		}
	}
	if rows[1].Reason == nil || *rows[1].Reason != "返回体字段缺失" {
		t.Errorf("reject reason = %v", rows[1].Reason)
	}

	history, err := f.env.service.ListGateDecisions(context.Background(), ListGateDecisionsInput{
		ProjectID: f.projectID, SubjectType: store.GateSubjectWorkItem, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListGateDecisions: %v", err)
	}
	if len(history.Items) != 2 || history.Items[0].SubjectID != "w_gate_reject" {
		t.Fatalf("work-item history = %+v, want 2 newest-first rows", history.Items)
	}
}

// TestPlanGateDecisionUsesBreakdownMaterial covers the plan batch: approve and
// reject each record one project-level row bound to the breakdown card's kind=4
// plan material, and the automatic phase settlement adds no work-item row.
func TestPlanGateDecisionUsesBreakdownMaterial(t *testing.T) {
	f := newBreakdownFixture(t)

	if _, err := f.reportPlan(oneItemPlan(f.projectID, "实现登录"), ""); err != nil {
		t.Fatalf("reportPlan: %v", err)
	}
	breakdown := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
	if breakdown.ResultMaterialID == nil {
		t.Fatal("breakdown result_material_id is nil after the plan report")
	}

	var (
		planCode    string
		planVersion int
	)
	if err := f.env.store.Read().QueryRowContext(context.Background(), `
		SELECT material_id, version FROM materials WHERE id = ?`, *breakdown.ResultMaterialID).
		Scan(&planCode, &planVersion); err != nil {
		t.Fatalf("read plan material: %v", err)
	}

	if _, err := f.work.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: f.projectID,
		Decision:  domain.DecisionReject,
		Reason:    "漏了回归测试卡",
		Actor:     "human:pm",
	}); err != nil {
		t.Fatalf("ReviewWorkItems(reject): %v", err)
	}
	if _, err := f.work.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: f.projectID,
		Decision:  domain.DecisionApprove,
		Actor:     "human:cto",
	}); err != nil {
		t.Fatalf("ReviewWorkItems(approve): %v", err)
	}

	rows := readGateDecisions(t, f.env.store)
	if len(rows) != 4 {
		t.Fatalf("gate decisions = %d, want 4 (2 project gates + 2 plan reviews)", len(rows))
	}
	for i, wantDecision := range []string{"reject", "approve"} {
		row := rows[2+i]
		requireMaterial(t, row, planCode, planVersion)
		if row.SubjectType != store.GateSubjectProject || row.SubjectID != f.projectID {
			t.Errorf("plan row %d subject = %s/%s, want project/%s", i, row.SubjectType, row.SubjectID, f.projectID)
		}
		if row.Gate != "plan" || row.Decision != wantDecision {
			t.Errorf("plan row %d = %s/%s, want plan/%s", i, row.Gate, row.Decision, wantDecision)
		}
	}
	if rows[2].DecidedBy != "human:pm" || rows[3].DecidedBy != "human:cto" {
		t.Errorf("decided_by = %q/%q, want human:pm/human:cto", rows[2].DecidedBy, rows[3].DecidedBy)
	}

	// Approving the batch accepted the breakdown card automatically. That is a
	// settlement, not a human verdict, so no work_item row may exist.
	for _, row := range rows {
		if row.SubjectType == store.GateSubjectWorkItem {
			t.Fatalf("automatic settlement wrote a work-item decision: %+v", row)
		}
	}
}
