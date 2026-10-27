package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// TestFullDeliveryFlowHTTP is the end-to-end integration test the test plan
// asks for: real middleware stack + real services + temporary SQLite, walked
// through every stage of the product lifecycle over HTTP:
//
//	产品 → 项目(需求) → 提交方案 → 方案通过 → 提交清单 → 清单通过
//	→ 领取 → 交报告 → 验收 → 领取第二张 → 验收 → 项目交付验收 → accepted
//
// Every repeated write along the way is asserted to be a 409 that leaves the
// persisted state and the material chain untouched.
func TestFullDeliveryFlowHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	ctx := context.Background()

	requireStatus := func(name string, rec interface {
		Result() *http.Response
	}, want int) {
		t.Helper()
		if got := rec.Result().StatusCode; got != want {
			t.Fatalf("%s: status = %d, want %d", name, got, want)
		}
	}

	// 1. 建产品 ---------------------------------------------------------
	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/products", map[string]any{
		"product_id": "APP",
		"name":       "驿传",
		"git_url":    "git@example.com:APP.git",
	}, map[string]string{HeaderActor: "pm"})
	requireStatus("create product", rec, http.StatusCreated)
	if loc := rec.Header().Get("Location"); loc != "/v1/products?id=APP" {
		t.Errorf("product Location = %q", loc)
	}

	// 2. 建项目（需求作为第一份材料） -----------------------------------
	const requirementMD = "# 需求\n\n- 登录页改版 🚀\n"
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "登录页改版",
		"requirement_md": requirementMD,
	}, map[string]string{HeaderActor: "pm"})
	requireStatus("create project", rec, http.StatusCreated)
	detail := decodeProjectDetail(t, rec)
	if detail.ProjectID != "APP-1" || detail.Status != domain.StatusDraft || detail.Version != 1 {
		t.Fatalf("project = %s/%d/v%d, want APP-1/draft/v1", detail.ProjectID, detail.Status, detail.Version)
	}
	if detail.RequirementMD != requirementMD {
		t.Errorf("requirement_md = %q, want byte-for-byte %q", detail.RequirementMD, requirementMD)
	}
	if detail.Requirement == nil || detail.Requirement.Kind != domain.MaterialKindRequirement ||
		detail.Requirement.Version != 1 {
		t.Errorf("requirement material = %+v, want kind 1 version 1", detail.Requirement)
	}

	// The requirement chain has exactly one version.
	reqChain := decodeChain(t, doJSON(t, h, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=1", nil))
	if reqChain.Count != 1 || len(reqChain.Items) != 1 || reqChain.Items[0].Version != 1 {
		t.Errorf("requirement chain = %+v, want a single v1", reqChain)
	}

	var state projectResponse

	// 3. 进入方案评审 ----------------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/accept?id=APP-1", nil,
		map[string]string{HeaderActor: "pm"})
	requireStatus("accept project", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusSpecReview {
		t.Fatalf("status = %d, want spec_review", state.Status)
	}

	// 4. 提交技术方案 ----------------------------------------------------
	const specMD = "# 方案\n\n1. 改路由\n2. 补测试\n"
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"body_md": specMD}, map[string]string{HeaderActor: "agent:cto"})
	requireStatus("submit spec", rec, http.StatusOK)
	spec := decodeSpecSubmit(t, rec.Body)
	if spec.Spec.Version != 1 || spec.Status != domain.StatusSpecReview {
		t.Errorf("spec = v%d status %d, want v1/spec_review", spec.Spec.Version, spec.Status)
	}

	// 5. 方案通过 --------------------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(spec.ProjectVersion)})
	requireStatus("approve spec", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusPlanning {
		t.Fatalf("status = %d, want planning", state.Status)
	}

	// 6. 提交任务清单 ----------------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id=APP-1",
		planBody(
			planTask("实现登录页", "# 实现\n", map[string]any{"priority": 10}),
			planTask("回归验证", "# 验证\n", map[string]any{"priority": 5, "depends_on": []int{1}}),
		), map[string]string{HeaderActor: "agent:cto"})
	requireStatus("submit plan", rec, http.StatusCreated)
	plan := decodeSubmitPlan(t, rec)
	if len(plan.Tasks) != 2 || plan.Tasks[0].ID != "APP-1-T0001" || plan.Tasks[1].ID != "APP-1-T0002" {
		t.Fatalf("plan tasks = %+v", plan.Tasks)
	}
	if len(plan.Tasks[1].DependsOn) != 1 || plan.Tasks[1].DependsOn[0] != "APP-1-T0001" {
		t.Errorf("T0002 depends_on = %v", plan.Tasks[1].DependsOn)
	}

	// 6b. 重复提交清单：409 且不产生第二批任务 -------------------------
	dup := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id=APP-1",
		planBody(planTask("重复清单", "# 重复\n")), map[string]string{HeaderActor: "agent:cto"})
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate plan status = %d, want 409 (body %s)", dup.Code, dup.Body.String())
	}
	if code := decodeError(t, dup).Code; code != "invalid_state" {
		t.Errorf("duplicate plan code = %q, want invalid_state", code)
	}
	if rows := taskRowsByProject(t, st, "APP-1"); len(rows) != 2 {
		t.Errorf("tasks after duplicate plan = %d, want 2", len(rows))
	}

	// 7. 清单通过 --------------------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(plan.ProjectVersion)})
	requireStatus("approve plan", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusDeveloping {
		t.Fatalf("status = %d, want developing", state.Status)
	}
	for _, row := range taskRowsByProject(t, st, "APP-1") {
		if row.Status != domain.TaskReady {
			t.Errorf("%s status = %d, want ready after plan approval", row.TaskID, row.Status)
		}
	}

	// 8. 领取第一张卡 ----------------------------------------------------
	claim := decodeClaim(t, doJSON(t, h, http.MethodPost, "/v1/tasks/claim",
		map[string]any{"product": "APP"}))
	if claim.Task.ID != "APP-1-T0001" {
		t.Fatalf("claimed %s, want APP-1-T0001 (the dependency-free card)", claim.Task.ID)
	}
	if claim.Lease.LeaseID == "" || claim.Task.Attempt != 1 {
		t.Errorf("lease/attempt = %q/%d", claim.Lease.LeaseID, claim.Task.Attempt)
	}

	// 9. 交报告 ----------------------------------------------------------
	const summary = "改了登录页\n\n- 自检：go test ./...\n"
	rec = postReport(t, h, claim.Task.ID, claim.Lease.LeaseID, summary)
	requireStatus("report", rec, http.StatusOK)
	reported := decodeTaskDetail(t, rec)
	if reported.Status != domain.TaskPendingAcceptance || reported.Report == nil ||
		reported.Report.Version != 1 {
		t.Fatalf("report detail = status %d report %+v", reported.Status, reported.Report)
	}

	// 9b. 重复交报告：409 lease_lost，且不会写出第二份报告材料 ---------
	expectLeaseLost(t, postReport(t, h, claim.Task.ID, claim.Lease.LeaseID, "第二次"))
	if n := countReportMaterials(t, st); n != 1 {
		t.Errorf("report materials after replay = %d, want 1", n)
	}

	// 10. 验收第一张卡 ---------------------------------------------------
	rec = postTaskDecision(t, h, claim.Task.ID, reported.Version, "accept", "")
	requireStatus("accept task", rec, http.StatusOK)
	done := decodeTaskDetail(t, rec)
	if done.Status != domain.TaskDone || done.Version != reported.Version+1 {
		t.Fatalf("accepted task = status %d v%d", done.Status, done.Version)
	}

	// 10b. 重复验收（同一 If-Match）：409 invalid_version，状态不变 -------
	dupAccept := postTaskDecision(t, h, claim.Task.ID, reported.Version, "accept", "")
	if dupAccept.Code != http.StatusConflict {
		t.Fatalf("duplicate accept status = %d, want 409 (body %s)", dupAccept.Code, dupAccept.Body.String())
	}
	if code := decodeError(t, dupAccept).Code; code != "invalid_version" {
		t.Errorf("duplicate accept code = %q, want invalid_version", code)
	}
	if again := getTaskHTTP(t, h, claim.Task.ID); again.Status != domain.TaskDone || again.Version != done.Version {
		t.Errorf("task after duplicate accept = status %d v%d, want done v%d", again.Status, again.Version, done.Version)
	}

	// The project must still be developing: one card is left.
	if got := getProjectHTTP(t, h, "APP-1"); got.Status != domain.StatusDeveloping {
		t.Errorf("project status = %d, want developing with one card left", got.Status)
	}

	// 11. 领取第二张卡（依赖已完成） ------------------------------------
	claim2 := decodeClaim(t, doJSON(t, h, http.MethodPost, "/v1/tasks/claim",
		map[string]any{"product": "APP"}))
	if claim2.Task.ID != "APP-1-T0002" {
		t.Fatalf("claimed %s, want APP-1-T0002", claim2.Task.ID)
	}
	rec = postReport(t, h, claim2.Task.ID, claim2.Lease.LeaseID, "回归通过")
	requireStatus("second report", rec, http.StatusOK)
	reported2 := decodeTaskDetail(t, rec)
	rec = postTaskDecision(t, h, claim2.Task.ID, reported2.Version, "accept", "")
	requireStatus("accept second task", rec, http.StatusOK)
	if detail := decodeTaskDetail(t, rec); detail.Status != domain.TaskDone {
		t.Errorf("second task status = %d, want done", detail.Status)
	}

	// 12. 最后一张卡验收后，项目自动进入交付验收 ------------------------
	state = getProjectHTTP(t, h, "APP-1")
	if state.Status != domain.StatusDeliveryReview {
		t.Fatalf("project status = %d, want delivery_review after the last acceptance", state.Status)
	}
	if state.TaskStats.Total != 2 || state.TaskStats.Done != 2 {
		t.Errorf("task_stats = %+v, want total/done 2/2", state.TaskStats)
	}

	// 13. 项目验收 -------------------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(state.Version)})
	requireStatus("accept delivery", rec, http.StatusOK)
	accepted := decodeProjectFromResponse(t, rec)
	if accepted.Status != domain.StatusAccepted || accepted.StatusName != "accepted" {
		t.Fatalf("project = %d (%s), want accepted", accepted.Status, accepted.StatusName)
	}

	// 13b. 重复项目验收：409 且终态不被二次推进 -------------------------
	dupDelivery := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(state.Version)})
	if dupDelivery.Code != http.StatusConflict {
		t.Fatalf("duplicate delivery accept status = %d, want 409 (body %s)",
			dupDelivery.Code, dupDelivery.Body.String())
	}
	final := getProjectHTTP(t, h, "APP-1")
	if final.Status != domain.StatusAccepted || final.Version != accepted.Version {
		t.Errorf("project after duplicate = %d v%d, want accepted v%d",
			final.Status, final.Version, accepted.Version)
	}

	// 14. 材料链与字节如实落库 ------------------------------------------
	specChain := decodeChain(t, doJSON(t, h, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=2", nil))
	if specChain.Count != 1 || specChain.Items[0].Version != 1 {
		t.Errorf("spec chain = %+v, want a single v1", specChain)
	}
	if n := countReportMaterials(t, st); n != 2 {
		t.Errorf("report materials = %d, want 2 (one per card)", n)
	}
	materialRec := doRequest(t, h, http.MethodGet, "/v1/materials?id="+reported.Report.MaterialID, nil)
	requireStatus("download report", materialRec, http.StatusOK)
	if got := materialRec.Body.String(); got != summary {
		t.Errorf("report bytes = %q, want %q", got, summary)
	}

	// Nothing is left ready or in progress.
	var unsettled int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		  WHERE p.project_id = 'APP-1' AND t.status NOT IN (4, 7)`).Scan(&unsettled); err != nil {
		t.Fatalf("count unsettled: %v", err)
	}
	if unsettled != 0 {
		t.Errorf("unsettled tasks = %d, want 0", unsettled)
	}
}
