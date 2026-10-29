package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// decodeSpecSubmit decodes one POST /v1/projects/specs response.
func decodeSpecSubmit(t *testing.T, rec interface{ Bytes() []byte }) specSubmitResponse {
	t.Helper()
	var response specSubmitResponse
	if err := json.Unmarshal(rec.Bytes(), &response); err != nil {
		t.Fatalf("decode spec response %q: %v", rec.Bytes(), err)
	}
	return response
}

// acceptHTTPProject moves a freshly created project to spec_review.
func acceptHTTPProject(t *testing.T, env *materialServerEnv, projectID string) projectResponse {
	t.Helper()
	rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/accept?id="+projectID, nil,
		map[string]string{HeaderActor: "pm"})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	return decodeProjectFromResponse(t, rec)
}

// specPointerHTTP returns the business id the project's spec pointer names, or
// "" when it is null.
func specPointerHTTP(t *testing.T, env *materialServerEnv, projectID string) string {
	t.Helper()
	// A LEFT JOIN keeps a null pointer a readable NULL instead of no-row.
	row := env.store.Read().QueryRowContext(t.Context(), `
		SELECT m.material_id
		  FROM projects p
		  LEFT JOIN materials m ON m.id = p.spec_material_id
		 WHERE p.project_id = ?`, projectID)
	var materialID *string
	if err := row.Scan(&materialID); err != nil {
		t.Fatalf("read spec pointer of %s: %v", projectID, err)
	}
	if materialID == nil {
		return ""
	}
	return *materialID
}

func TestSubmitSpecHTTP(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n\n正文\n")
	accepted := acceptHTTPProject(t, env, "APP-1")

	const body = "# 方案\n\n- 接入短信登录\n"
	rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"title": "登录页改版方案", "body_md": body},
		map[string]string{HeaderActor: "agent:cto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	response := decodeSpecSubmit(t, rec.Body)
	if response.ProjectID != "APP-1" {
		t.Errorf("project_id = %q, want APP-1", response.ProjectID)
	}
	if response.Status != 1 || response.StatusName != "spec_review" {
		t.Errorf("status = %d (%s), want 1 (spec_review)", response.Status, response.StatusName)
	}
	if response.ProjectVersion != accepted.Version+1 {
		t.Errorf("project_version = %d, want %d", response.ProjectVersion, accepted.Version+1)
	}
	spec := response.Spec
	if spec.Kind != 2 || spec.Version != 1 {
		t.Errorf("spec kind/version = %d/%d, want 2/1", spec.Kind, spec.Version)
	}
	if spec.Title != "登录页改版方案" {
		t.Errorf("spec title = %q", spec.Title)
	}
	if spec.UploadedBy != "agent:cto" {
		t.Errorf("uploaded_by = %q, want agent:cto", spec.UploadedBy)
	}
	if spec.PreviousMaterialID != nil {
		t.Errorf("previous_material_id = %v, want null for v1", *spec.PreviousMaterialID)
	}
	if spec.MaterialID == "" || spec.SHA256 == "" || spec.Size != int64(len(body)) || spec.CreateTime == "" {
		t.Errorf("incomplete spec metadata: %+v", spec)
	}
	if got := specPointerHTTP(t, env, "APP-1"); got != spec.MaterialID {
		t.Errorf("spec pointer = %q, want %q", got, spec.MaterialID)
	}

	// Second version: chained and pointed at, status untouched.
	rec = doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"body_md": "# 方案 v2\n"}, map[string]string{HeaderActor: "agent:cto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	second := decodeSpecSubmit(t, rec.Body)
	if second.Spec.Version != 2 {
		t.Errorf("version = %d, want 2", second.Spec.Version)
	}
	if second.Spec.PreviousMaterialID == nil || *second.Spec.PreviousMaterialID != spec.MaterialID {
		t.Fatalf("previous = %v, want %q", second.Spec.PreviousMaterialID, spec.MaterialID)
	}
	if second.Spec.Title != "登录页改版" {
		t.Errorf("title = %q, want the project title fallback", second.Spec.Title)
	}
	if second.Status != 1 {
		t.Errorf("status = %d, want 1 (submitting must not move the gate)", second.Status)
	}

	// The detail endpoint exposes the new head metadata (no body).
	detailRec := doJSON(t, env.handler, http.MethodGet, "/v1/projects?id=APP-1", nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detailRec.Code)
	}
	detail := decodeProjectDetail(t, detailRec)
	if detail.Spec == nil {
		t.Fatal("detail.spec = nil, want the current spec")
	}
	if detail.Spec.MaterialID != second.Spec.MaterialID || detail.Spec.Version != 2 {
		t.Errorf("detail.spec = %+v, want v2", detail.Spec)
	}
	if detail.Status != 1 {
		t.Errorf("detail status = %d, want 1", detail.Status)
	}
}

func TestSubmitSpecHTTPNoSpecDetail(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects?id=APP-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// spec must be present as JSON null; the body fields must be gone.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	value, ok := raw["spec"]
	if !ok {
		t.Fatal("spec missing from the detail response")
	}
	if string(value) != "null" {
		t.Errorf("spec = %s, want null", value)
	}
	for _, field := range []string{"requirement_md", "spec_excerpt"} {
		if _, ok := raw[field]; ok {
			t.Errorf("detail response still carries %q", field)
		}
	}
}

func TestSubmitSpecHTTPErrors(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")
	acceptHTTPProject(t, env, "APP-1")

	cases := []struct {
		name       string
		target     string
		body       any
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name: "missing id", target: "/v1/projects/specs",
			body: map[string]any{"body_md": "# 方案"}, wantStatus: http.StatusBadRequest,
			wantCode: "invalid_argument", wantField: "id",
		},
		{
			name: "unknown project", target: "/v1/projects/specs?id=APP-404",
			body: map[string]any{"body_md": "# 方案"}, wantStatus: http.StatusNotFound,
			wantCode: "not_found",
		},
		{
			name: "unknown field", target: "/v1/projects/specs?id=APP-1",
			body: map[string]any{"body_md": "# 方案", "status": 2}, wantStatus: http.StatusBadRequest,
			wantCode: "invalid_argument", wantField: "status",
		},
		{
			name: "blank title", target: "/v1/projects/specs?id=APP-1",
			body: map[string]any{"title": "  ", "body_md": "# 方案"}, wantStatus: http.StatusBadRequest,
			wantCode: "invalid_argument", wantField: "title",
		},
		{
			name: "missing body", target: "/v1/projects/specs?id=APP-1",
			body: map[string]any{"title": "方案"}, wantStatus: http.StatusBadRequest,
			wantCode: "invalid_argument", wantField: "body_md",
		},
		{
			name: "empty body", target: "/v1/projects/specs?id=APP-1",
			body: map[string]any{"body_md": "   "}, wantStatus: http.StatusBadRequest,
			wantCode: "invalid_argument", wantField: "body_md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONHeaders(t, env.handler, http.MethodPost, tc.target, tc.body, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
			}
			if body.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tc.wantCode)
			}
			if tc.wantField != "" {
				if len(body.Details) != 1 || body.Details[0].Field != tc.wantField {
					t.Errorf("details = %+v, want one %q error", body.Details, tc.wantField)
				}
			}
			if body.RequestID == "" {
				t.Error("request_id is empty")
			}
		})
	}

	// Nothing was written by any rejected request.
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials WHERE kind = 2"); got != 0 {
		t.Errorf("kind=2 rows = %d, want 0", got)
	}
}

func TestSubmitSpecHTTPInvalidState(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")

	for _, status := range []int{0, 3, 4, 5, 6, 7} {
		t.Run(statusName(status), func(t *testing.T) {
			if _, err := env.store.Write().ExecContext(t.Context(),
				"UPDATE projects SET status = ?, version = version + 1 WHERE project_id = 'APP-1'", status); err != nil {
				t.Fatalf("force status: %v", err)
			}
			rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
				map[string]any{"body_md": "# 方案"}, nil)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Code != "invalid_state" {
				t.Errorf("code = %q, want invalid_state", body.Code)
			}
		})
	}
}

// statusName is a local mirror so the HTTP test can name subtests without
// importing the domain package for one call.
func statusName(status int) string {
	names := []string{"draft", "spec_review", "planning", "plan_review", "developing", "delivery_review", "accepted", "cancelled"}
	if status < 0 || status >= len(names) {
		return "unknown"
	}
	return names[status]
}

func TestSubmitSpecHTTPTooLarge(t *testing.T) {
	const maxBytes = 512
	env := newMaterialTestServer(t, maxBytes)
	createProjectWithRequirement(t, env, "# 需求\n")
	acceptHTTPProject(t, env, "APP-1")

	rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"body_md": strings.Repeat("超", 600)}, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "too_large" {
		t.Errorf("code = %q, want too_large", body.Code)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "body_md" {
		t.Errorf("details = %+v, want body_md", body.Details)
	}

	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials WHERE kind = 2"); got != 0 {
		t.Errorf("kind=2 rows = %d, want 0 (no row for an oversized body)", got)
	}
	if got := specPointerHTTP(t, env, "APP-1"); got != "" {
		t.Errorf("spec pointer = %q, want it untouched", got)
	}
	// Requirement object only: the oversized body never reached objects/.
	if got := countObjectFiles(t, env.dataDir); got != 1 {
		t.Errorf("object files = %d, want 1", got)
	}
}

// TestSubmitSpecHTTPChain walks the acceptance scenario end to end: v1 → v2,
// reject, v3, then compares the chain endpoint with the pointer.
func TestSubmitSpecHTTPChain(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")
	acceptHTTPProject(t, env, "APP-1")

	submit := func(body string) specSubmitResponse {
		rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
			map[string]any{"body_md": body}, map[string]string{HeaderActor: "agent:cto"})
		if rec.Code != http.StatusOK {
			t.Fatalf("submit status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		return decodeSpecSubmit(t, rec.Body)
	}

	v1 := submit("# v1\n")
	v2 := submit("# v2\n")

	// Reject with the current version; the gate stays at spec_review.
	rejectRec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "reject", "reason": "缺少回滚方案"},
		map[string]string{"If-Match": strconv.Itoa(v2.ProjectVersion)})
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200 (body %s)", rejectRec.Code, rejectRec.Body.String())
	}
	rejected := decodeProjectFromResponse(t, rejectRec)
	if rejected.Status != 1 {
		t.Fatalf("status = %d, want 1 after reject", rejected.Status)
	}
	if rejected.Reason == nil || *rejected.Reason != "缺少回滚方案" {
		t.Fatalf("reason = %v, want the rejection reason", rejected.Reason)
	}

	v3 := submit("# v3 补充回滚方案\n")
	if v3.Spec.Version != 3 {
		t.Errorf("version = %d, want 3", v3.Spec.Version)
	}
	if v3.Spec.PreviousMaterialID == nil || *v3.Spec.PreviousMaterialID != v2.Spec.MaterialID {
		t.Errorf("previous = %v, want v2 %q", v3.Spec.PreviousMaterialID, v2.Spec.MaterialID)
	}
	if got := specPointerHTTP(t, env, "APP-1"); got != v3.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want v3 %q", got, v3.Spec.MaterialID)
	}

	// The chain endpoint must agree with the pointer and with 1..3.
	chainRec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=2", nil)
	if chainRec.Code != http.StatusOK {
		t.Fatalf("chain status = %d, want 200", chainRec.Code)
	}
	chain := decodeChain(t, chainRec)
	if chain.Count != 3 {
		t.Fatalf("chain count = %d, want 3", chain.Count)
	}
	wantIDs := []string{v1.Spec.MaterialID, v2.Spec.MaterialID, v3.Spec.MaterialID}
	for i, item := range chain.Items {
		if item.Version != i+1 || item.MaterialID != wantIDs[i] {
			t.Errorf("items[%d] = v%d %q, want v%d %q", i, item.Version, item.MaterialID, i+1, wantIDs[i])
		}
		if i == 0 {
			if item.PreviousMaterialID != nil {
				t.Errorf("items[0].previous = %v, want null", *item.PreviousMaterialID)
			}
			continue
		}
		if item.PreviousMaterialID == nil || *item.PreviousMaterialID != wantIDs[i-1] {
			t.Errorf("items[%d].previous = %v, want %q", i, item.PreviousMaterialID, wantIDs[i-1])
		}
	}
	if chain.CurrentMaterialID == nil || *chain.CurrentMaterialID != v3.Spec.MaterialID {
		t.Errorf("current = %v, want %q", chain.CurrentMaterialID, v3.Spec.MaterialID)
	}

	// Approve clears the reason and leaves the pointer on v3.
	approveRec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(v3.ProjectVersion)})
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200 (body %s)", approveRec.Code, approveRec.Body.String())
	}
	approved := decodeProjectFromResponse(t, approveRec)
	if approved.Status != 2 {
		t.Errorf("status = %d, want 2 (planning)", approved.Status)
	}
	if approved.Reason != nil {
		t.Errorf("reason = %v, want null after approval", *approved.Reason)
	}
	if got := specPointerHTTP(t, env, "APP-1"); got != v3.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want v3 to survive approval", got)
	}
}

// TestSubmitSpecHTTPConcurrent submits ten specs in parallel through the HTTP
// stack and checks the chain is linear.
func TestSubmitSpecHTTPConcurrent(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")
	acceptHTTPProject(t, env, "APP-1")

	const submissions = 10
	versions := make(chan int, submissions)
	statuses := make(chan int, submissions)
	var wg sync.WaitGroup
	for i := 0; i < submissions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/projects/specs?id=APP-1",
				map[string]any{"body_md": "# 并发 " + strconv.Itoa(i) + "\n"},
				map[string]string{HeaderActor: "agent:cto-" + strconv.Itoa(i)})
			statuses <- rec.Code
			if rec.Code == http.StatusOK {
				versions <- decodeSpecSubmit(t, rec.Body).Spec.Version
			}
		}(i)
	}
	wg.Wait()
	close(statuses)
	close(versions)

	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 for every concurrent submit", status)
		}
	}
	seen := make(map[int]struct{}, submissions)
	for version := range versions {
		if _, duplicate := seen[version]; duplicate {
			t.Fatalf("version %d was returned twice", version)
		}
		seen[version] = struct{}{}
	}
	if len(seen) != submissions {
		t.Fatalf("distinct versions = %d, want %d", len(seen), submissions)
	}

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=2", nil)
	chain := decodeChain(t, rec)
	if chain.Count != submissions {
		t.Fatalf("chain count = %d, want %d", chain.Count, submissions)
	}
	for i, item := range chain.Items {
		if item.Version != i+1 {
			t.Errorf("items[%d].version = %d, want %d", i, item.Version, i+1)
		}
		if i > 0 {
			if item.PreviousMaterialID == nil || *item.PreviousMaterialID != chain.Items[i-1].MaterialID {
				t.Errorf("items[%d].previous = %v, want %q", i, item.PreviousMaterialID, chain.Items[i-1].MaterialID)
			}
		}
	}
	head := chain.Items[len(chain.Items)-1].MaterialID
	if got := specPointerHTTP(t, env, "APP-1"); got != head {
		t.Errorf("spec pointer = %q, want the v%d head %q", got, submissions, head)
	}
}
