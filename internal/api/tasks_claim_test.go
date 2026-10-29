package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// claimHTTPEnv builds the API server (with the claim service wired) and one
// planning project holding one ready card.
func claimHTTPEnv(t *testing.T) (http.Handler, *store.Store, string, string) {
	t.Helper()
	h, st, projectID := newTaskHTTPEnv(t)
	// Claim context still supports a project without a spec pointer.
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET spec_material_id = NULL WHERE project_id = ?`, projectID); err != nil {
		t.Fatalf("null spec pointer: %v", err)
	}
	detail := postTask(t, h, projectID, "领取卡", "# 正文\n", 10)
	forceTaskColumns(t, st, detail.ID, domain.TaskReady, 0, nil, nil, nil)
	return h, st, projectID, detail.ID
}

// claimResponseHTTP is the 200 body of the claim endpoint.
type claimResponseHTTP struct {
	Task struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		BodyMD    string `json:"body_md"`
		Attempt   int    `json:"attempt"`
		Priority  int    `json:"priority"`
	} `json:"task"`
	Lease struct {
		LeaseID    string `json:"lease_id"`
		Until      string `json:"until"`
		TTLSeconds int    `json:"ttl_seconds"`
	} `json:"lease"`
	Context struct {
		SpecVersion int    `json:"spec_version"`
		SpecDigest  string `json:"spec_digest"`
		Workdir     string `json:"workdir"`
		Product     struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			GitURL string `json:"git_url"`
		} `json:"product"`
	} `json:"context"`
}

func decodeClaim(t *testing.T, rec *httptest.ResponseRecorder) claimResponseHTTP {
	t.Helper()
	var body claimResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode claim %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestClaimHTTP(t *testing.T) {
	h, _, projectID, taskID := claimHTTPEnv(t)

	before := time.Now().UTC()
	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeClaim(t, rec)
	if body.Task.ID != taskID || body.Task.ProjectID != projectID {
		t.Errorf("task = %s/%s, want %s/%s", body.Task.ID, body.Task.ProjectID, taskID, projectID)
	}
	if body.Task.Title != "领取卡" || body.Task.BodyMD != "# 正文\n" || body.Task.Priority != 10 {
		t.Errorf("task payload = %+v", body.Task)
	}
	if body.Task.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", body.Task.Attempt)
	}
	if len(body.Lease.LeaseID) != 18 || body.Lease.LeaseID[:2] != "l_" {
		t.Errorf("lease_id = %q, want l_ + 16 hex", body.Lease.LeaseID)
	}
	if body.Lease.TTLSeconds != 90 {
		t.Errorf("ttl_seconds = %d, want 90", body.Lease.TTLSeconds)
	}
	until, err := time.Parse(time.RFC3339Nano, body.Lease.Until)
	if err != nil {
		t.Fatalf("parse until %q: %v", body.Lease.Until, err)
	}
	if delta := until.Sub(before); delta < 85*time.Second || delta > 95*time.Second {
		t.Errorf("until - now = %s, want ~90s", delta)
	}

	if body.Context.SpecVersion != 0 || body.Context.SpecDigest != "" {
		t.Errorf("spec = %d/%q, want 0/\"\"", body.Context.SpecVersion, body.Context.SpecDigest)
	}
	if body.Context.Product.ID != "APP" || body.Context.Product.Name != "APP 产品" ||
		body.Context.Product.GitURL != "git@example.com:APP.git" {
		t.Errorf("product = %+v", body.Context.Product)
	}
	if want := "/workspaces/" + projectID; len(body.Context.Workdir) < len(want) ||
		body.Context.Workdir[len(body.Context.Workdir)-len(want):] != want {
		t.Errorf("workdir = %q, want suffix %q", body.Context.Workdir, want)
	}

	// The queue is empty now: 204 with no body at all.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second claim: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 body = %q, want empty", rec.Body.String())
	}
}

func TestClaimHTTPValidation(t *testing.T) {
	h, _, _, _ := claimHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing product: status = %d, want 400", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "invalid_argument" || len(body.Details) == 0 || body.Details[0].Field != "product" {
		t.Errorf("missing product body = %+v", body)
	}

	// An absent body is a 400 that names the body, not the product.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "NOPE"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown product: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "not_found" {
		t.Errorf("unknown product code = %q", body.Code)
	}
}

// TestClaimHTTPTwentyWorkers is the end-to-end concurrency case: 20 HTTP
// requests for one card, exactly one 200 and nineteen 204s.
func TestClaimHTTPTwentyWorkers(t *testing.T) {
	h, _, _, taskID := claimHTTPEnv(t)

	const workers = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		okCount int
		noCount int
		bad     []int
	)
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusOK:
				okCount++
				if body := decodeClaim(t, rec); body.Task.ID != taskID {
					bad = append(bad, -1)
				}
			case http.StatusNoContent:
				noCount++
			default:
				bad = append(bad, rec.Code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(bad) != 0 {
		t.Fatalf("unexpected status codes: %v", bad)
	}
	if okCount != 1 || noCount != workers-1 {
		t.Fatalf("200s = %d, 204s = %d, want 1/%d", okCount, noCount, workers-1)
	}
}

func TestHeartbeatHTTP(t *testing.T) {
	h, _, _, taskID := claimHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	claimed := decodeClaim(t, rec)

	var renewed struct {
		LeaseID    string `json:"lease_id"`
		Until      string `json:"until"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id="+taskID,
		map[string]any{"lease_id": claimed.Lease.LeaseID})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &renewed); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	if renewed.LeaseID != claimed.Lease.LeaseID || renewed.TTLSeconds != 90 {
		t.Errorf("renewal = %+v", renewed)
	}
	renewedUntil, err := time.Parse(time.RFC3339Nano, renewed.Until)
	if err != nil {
		t.Fatalf("parse renewed until: %v", err)
	}
	claimedUntil, err := time.Parse(time.RFC3339Nano, claimed.Lease.Until)
	if err != nil {
		t.Fatalf("parse claimed until: %v", err)
	}
	if renewedUntil.Before(claimedUntil) {
		t.Errorf("renewed until = %s, want not before %s", renewedUntil, claimedUntil)
	}

	// Wrong lease id → 409 lease_lost.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id="+taskID,
		map[string]any{"lease_id": "l_0000000000000000"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("wrong lease: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "lease_lost" {
		t.Errorf("wrong lease code = %q, want lease_lost", body.Code)
	}

	// Missing lease_id → 400.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id="+taskID, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing lease_id: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); len(body.Details) == 0 || body.Details[0].Field != "lease_id" {
		t.Errorf("missing lease_id details = %+v", body.Details)
	}

	// Empty body is a 400 too (the lease id cannot be read at all).
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id="+taskID, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}

	// Missing task id → 400.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat",
		map[string]any{"lease_id": claimed.Lease.LeaseID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: status = %d, want 400", rec.Code)
	}

	// Unknown task → 404.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id=APP-9-T9999",
		map[string]any{"lease_id": claimed.Lease.LeaseID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestHeartbeatHTTPExpiredLease forces the deadline into the past and proves the
// endpoint answers lease_lost rather than extending a dead lease.
func TestHeartbeatHTTPExpiredLease(t *testing.T) {
	h, st, _, taskID := claimHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	claimed := decodeClaim(t, rec)

	past := "2020-01-01T00:00:00.000Z"
	forceTaskColumns(t, st, taskID, domain.TaskInProgress, 1, &claimed.Lease.LeaseID, &past, nil)

	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/heartbeat?id="+taskID,
		map[string]any{"lease_id": claimed.Lease.LeaseID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expired heartbeat: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "lease_lost" {
		t.Errorf("expired heartbeat code = %q, want lease_lost", body.Code)
	}
}

// TestClaimHTTPReclaimsExpiredCard proves the lazy reclaim is reachable through
// the endpoint: an expired card is handed out again on the next claim.
func TestClaimHTTPReclaimsExpiredCard(t *testing.T) {
	h, st, _, taskID := claimHTTPEnv(t)
	if rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"}); rec.Code != http.StatusOK {
		t.Fatalf("first claim: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	past := "2020-01-01T00:00:00.000Z"
	lease := "l_expired"
	forceTaskColumns(t, st, taskID, domain.TaskInProgress, 1, &lease, &past, nil)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim after expiry: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeClaim(t, rec)
	if body.Task.ID != taskID || body.Task.Attempt != 2 {
		t.Errorf("reclaimed claim = %+v, want %s attempt 2", body.Task, taskID)
	}
}
