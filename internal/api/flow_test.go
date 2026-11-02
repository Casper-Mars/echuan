package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// TestFullDeliveryFlowHTTP is the end-to-end integration test the test plan
// asks for: real middleware stack + real services + temporary SQLite, walked
// through every stage of the product lifecycle over HTTP:
//
//	产品 → 项目(需求) → 需求提交/通过 → 方案提交/通过 → 批量加卡
//	→ 批次审批 → 领取 → 报告 → 验收 → 第二张 → 验收 → 项目自动完成
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
	if detail.ProjectID != "APP-1" || detail.Status != domain.StatusRequirementDraft || detail.Version != 1 {
		t.Fatalf("project = %s/%d/v%d, want APP-1/requirement_draft/v1", detail.ProjectID, detail.Status, detail.Version)
	}
	if detail.Requirement == nil || detail.Requirement.MaterialID == "" {
		t.Errorf("requirement = %+v, want metadata with a material_id", detail.Requirement)
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

	// 3. 需求提交 → 需求评审 --------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id=APP-1", nil,
		map[string]string{HeaderActor: "pm"})
	requireStatus("submit requirement", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusRequirementReview {
		t.Fatalf("status = %d, want requirement_review", state.Status)
	}

	// 3b. 需求通过 → 方案草稿 -------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(state.Version)})
	requireStatus("approve requirement", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", state.Status)
	}

	// 4. 提交技术方案 ----------------------------------------------------
	const specMD = "# 方案\n\n1. 改路由\n2. 补测试\n"
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"body_md": specMD}, map[string]string{HeaderActor: "agent:cto"})
	requireStatus("submit spec", rec, http.StatusOK)
	spec := decodeSpecSubmit(t, rec.Body)
	if spec.Spec.Version != 1 || spec.Status != domain.StatusSpecDraft {
		t.Errorf("spec = v%d status %d, want v1/spec_draft", spec.Spec.Version, spec.Status)
	}

	// 4b. 方案提交 → 方案评审 --------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id=APP-1", nil,
		map[string]string{HeaderActor: "pm"})
	requireStatus("submit spec review", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusSpecReview {
		t.Fatalf("status = %d, want spec_review", state.Status)
	}

	// 5. 方案通过 → 任务草稿 --------------------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(state.Version)})
	requireStatus("approve spec", rec, http.StatusOK)
	state = decodeProjectFromResponse(t, rec)
	if state.Status != domain.StatusTasksDraft {
		t.Fatalf("status = %d, want tasks_draft", state.Status)
	}

	// 6. 批量添加任务卡（第一张卡开门 4 → 5） ---------------------------
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
	if plan.Status != domain.StatusTasksReview || plan.StatusName != "tasks_review" {
		t.Errorf("plan status = %d (%s), want tasks_review", plan.Status, plan.StatusName)
	}
	if len(plan.Tasks[1].DependsOn) != 1 || plan.Tasks[1].DependsOn[0] != "APP-1-T0001" {
		t.Errorf("T0002 depends_on = %v", plan.Tasks[1].DependsOn)
	}

	// 7. 批次审批 → 待完成，卡转 ready ----------------------------------
	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id=APP-1",
		map[string]any{"decision": "approve"}, map[string]string{HeaderActor: "cto"})
	requireStatus("approve task batch", rec, http.StatusOK)
	review := decodeReviewTasks(t, rec.Body)
	if review.Status != domain.StatusPendingDone || review.StatusName != "pending_done" {
		t.Errorf("review status = %d (%s), want pending_done", review.Status, review.StatusName)
	}
	if review.ReadyTasks != 2 {
		t.Errorf("ready tasks = %d, want 2", review.ReadyTasks)
	}
	for _, row := range taskRowsByProject(t, st, "APP-1") {
		if row.Status != domain.TaskReady {
			t.Errorf("%s status = %d, want ready after batch approval", row.TaskID, row.Status)
		}
	}

	// 7b. 重复审批：409 且任务/项目不再变化 ------------------------------
	dupReview := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id=APP-1",
		map[string]any{"decision": "approve"}, nil)
	if dupReview.Code != http.StatusConflict {
		t.Fatalf("duplicate review status = %d, want 409 (body %s)", dupReview.Code, dupReview.Body.String())
	}
	if code := decodeError(t, dupReview).Code; code != "invalid_state" {
		t.Errorf("duplicate review code = %q, want invalid_state", code)
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

	// The project must still be pending_done: one card is left.
	if got := getProjectHTTP(t, h, "APP-1"); got.Status != domain.StatusPendingDone {
		t.Errorf("project status = %d, want pending_done with one card left", got.Status)
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

	// 12. 最后一张卡验收后，项目自动完成 --------------------------------
	state = getProjectHTTP(t, h, "APP-1")
	if state.Status != domain.StatusCompleted || state.StatusName != "completed" {
		t.Fatalf("project status = %d (%s), want completed", state.Status, state.StatusName)
	}
	if state.TaskStats.Total != 2 || state.TaskStats.Done != 2 {
		t.Errorf("task_stats = %+v, want total/done 2/2", state.TaskStats)
	}

	// 12b. 已完成项目不再接受 decide：409 invalid_state -----------------
	dupDecide := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(state.Version)})
	if dupDecide.Code != http.StatusConflict {
		t.Fatalf("decide on completed status = %d, want 409 (body %s)",
			dupDecide.Code, dupDecide.Body.String())
	}
	if code := decodeError(t, dupDecide).Code; code != "invalid_state" {
		t.Errorf("decide on completed code = %q, want invalid_state", code)
	}
	final := getProjectHTTP(t, h, "APP-1")
	if final.Status != domain.StatusCompleted || final.Version != state.Version {
		t.Errorf("project after duplicate = %d v%d, want completed v%d",
			final.Status, final.Version, state.Version)
	}

	// 13. 材料链与字节如实落库 ------------------------------------------
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

// decodeReviewTasks decodes the 200 body of POST /v1/tasks/review.
func decodeReviewTasks(t *testing.T, rec interface{ Bytes() []byte }) reviewTasksResponse {
	t.Helper()
	var response reviewTasksResponse
	if err := json.Unmarshal(rec.Bytes(), &response); err != nil {
		t.Fatalf("decode review response %q: %v", rec.Bytes(), err)
	}
	return response
}
