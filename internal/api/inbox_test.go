package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// inboxTestClock is a fixed platform.Clock so the HTTP tests can assert exact
// ages and generated_at values.
type inboxTestClock struct{ now time.Time }

func (c *inboxTestClock) Now() time.Time { return c.now }

// newInboxHTTPEnv builds the real middleware stack with only the inbox service
// registered. Rows are seeded with SQL so update_time is exact and no other
// endpoint is needed to reach a given status.
func newInboxHTTPEnv(t *testing.T, staleHours int, now time.Time) (http.Handler, *store.Store, *inboxTestClock) {
	t.Helper()

	st, err := store.Open(store.Config{Path: filepath.Join(t.TempDir(), "state.db"), ReadPoolSize: 2})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.OpenReadPool(context.Background()); err != nil {
		t.Fatalf("OpenReadPool: %v", err)
	}

	logger := testLogger(&strings.Builder{})
	inboxService := app.NewInboxService(st, logger, staleHours)
	clock := &inboxTestClock{now: now}
	inboxService.SetClock(clock)

	h := Chain(
		NewRouter(logger, nil, nil, nil, nil, nil, nil, nil, nil, inboxService),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	return h, st, clock
}

// seedInboxRow inserts one project or task, whichever kind is asked for.
func seedInboxRow(t *testing.T, st *store.Store, kind, id string, status int, title, updateTime string, reason *string) {
	t.Helper()
	ctx := context.Background()

	res, err := st.Write().ExecContext(ctx, `
		INSERT OR IGNORE INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', 'APP 产品', 'git@example.com:APP.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}

	var productFK int64
	if err := st.Read().QueryRowContext(ctx, `SELECT id FROM products WHERE product_id = 'APP'`).Scan(&productFK); err != nil {
		t.Fatalf("read product id: %v", err)
	}

	res, err = st.Write().ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, reason, version, create_time, update_time)
		VALUES ('OWNER', ?, 900, '任务宿主', 4, NULL, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		productFK)
	if err != nil {
		// UNIQUE(project_id) means a previous seed call already created it.
		if !strings.Contains(err.Error(), "UNIQUE") {
			t.Fatalf("insert owner project: %v", err)
		}
	} else if _, err := res.LastInsertId(); err != nil {
		t.Fatalf("owner project id: %v", err)
	}

	var ownerFK int64
	if err := st.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE project_id = 'OWNER'`).Scan(&ownerFK); err != nil {
		t.Fatalf("read owner project id: %v", err)
	}

	switch kind {
	case "project":
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO projects (project_id, product_id, seq_no, title, status, reason, version, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			id, productFK, seqNo(id), title, status, reason, updateTime, updateTime); err != nil {
			t.Fatalf("insert project %s: %v", id, err)
		}
	case "task":
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority, status,
			                   attempt_count, max_attempts, reason, version, create_time, update_time)
			VALUES (?, ?, ?, ?, 'body', 0, ?, 0, 3, ?, 1, ?, ?)`,
			id, ownerFK, seqNo(id), title, status, reason, updateTime, updateTime); err != nil {
			t.Fatalf("insert task %s: %v", id, err)
		}
	default:
		t.Fatalf("unknown kind %q", kind)
	}
}

// seqNo derives a stable per-project sequence number from the trailing digits
// of the business id so seeded rows never violate UNIQUE(project_id, seq_no).
// The 1000 offset keeps these rows clear of the owner project's fixed 900.
func seqNo(id string) int {
	start := len(id)
	for start > 0 && id[start-1] >= '0' && id[start-1] <= '9' {
		start--
	}
	n := 0
	if start < len(id) {
		n, _ = strconv.Atoi(id[start:])
	}
	return 1000 + n
}

func decodeInbox(t *testing.T, rec *httptest.ResponseRecorder) inboxResponse {
	t.Helper()
	var body inboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode inbox %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestInboxHTTPEmpty(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, _, _ := newInboxHTTPEnv(t, 72, now)

	rec := doRequest(t, h, http.MethodGet, "/v1/inbox", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("body = %s, want items:[] (not null)", rec.Body.String())
	}

	body := decodeInbox(t, rec)
	if len(body.Items) != 0 || body.Count != 0 {
		t.Fatalf("items/count = %d/%d, want 0/0", len(body.Items), body.Count)
	}
	if body.ReviewStaleHours != 72 {
		t.Fatalf("review_stale_hours = %d, want 72", body.ReviewStaleHours)
	}
	if _, err := time.Parse(time.RFC3339Nano, body.GeneratedAt); err != nil {
		t.Fatalf("generated_at = %q: %v", body.GeneratedAt, err)
	}
	if body.GeneratedAt != "2026-10-27T09:50:00Z" {
		t.Errorf("generated_at = %q, want the injected clock", body.GeneratedAt)
	}
}

// TestInboxHTTPContract checks the documented body field by field.
func TestInboxHTTPContract(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, st, _ := newInboxHTTPEnv(t, 72, now)

	reason := "attempt 用尽"
	seedInboxRow(t, st, "project", "APP-1", domain.StatusSpecReview,
		"订单导出", "2026-10-24T09:50:00Z", nil)
	seedInboxRow(t, st, "task", "APP-1-T0003", domain.TaskFailed,
		"补并发测试", "2026-10-26T09:50:00Z", &reason)

	rec := doRequest(t, h, http.MethodGet, "/v1/inbox", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeInbox(t, rec)
	if body.Count != 2 || len(body.Items) != 2 {
		t.Fatalf("count/items = %d/%d, want 2", body.Count, len(body.Items))
	}

	project := body.Items[0]
	if project.Kind != "project" || project.ID != "APP-1" || project.State != domain.StatusSpecReview {
		t.Errorf("project identity = %+v", project)
	}
	if project.StateName != "spec_review" || project.Title != "订单导出" {
		t.Errorf("project name/title = %q/%q", project.StateName, project.Title)
	}
	if project.UpdateTime != "2026-10-24T09:50:00Z" {
		t.Errorf("update_time = %q, want the stored value", project.UpdateTime)
	}
	if project.AgeSeconds != 3*24*3600 || !project.Stale {
		t.Errorf("age/stale = %d/%v, want 259200/true", project.AgeSeconds, project.Stale)
	}
	if project.Reason != nil {
		t.Errorf("project reason = %v, want null", project.Reason)
	}

	task := body.Items[1]
	if task.Kind != "task" || task.ID != "APP-1-T0003" || task.State != domain.TaskFailed {
		t.Errorf("task identity = %+v", task)
	}
	if task.StateName != "failed" || task.Title != "补并发测试" {
		t.Errorf("task name/title = %q/%q", task.StateName, task.Title)
	}
	if task.AgeSeconds != 24*3600 || task.Stale {
		t.Errorf("task age/stale = %d/%v, want 86400/false", task.AgeSeconds, task.Stale)
	}
	if task.Reason == nil || *task.Reason != reason {
		t.Errorf("task reason = %v, want %q", task.Reason, reason)
	}

	// The JSON must carry the documented member names, including null reason.
	raw := rec.Body.String()
	for _, member := range []string{
		`"kind":"project"`, `"id":"APP-1"`, `"state":1`, `"state_name":"spec_review"`,
		`"title":"订单导出"`, `"update_time":"2026-10-24T09:50:00Z"`,
		`"age_seconds":259200`, `"stale":true`, `"reason":null`,
		`"count":2`, `"review_stale_hours":72`, `"generated_at":"2026-10-27T09:50:00Z"`,
	} {
		if !strings.Contains(raw, member) {
			t.Errorf("body missing %s: %s", member, raw)
		}
	}
}

func TestInboxHTTPLimitValidation(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, _, _ := newInboxHTTPEnv(t, 72, now)

	for _, query := range []string{"limit=0", "limit=-1", "limit=abc", "limit=201"} {
		rec := doRequest(t, h, http.MethodGet, "/v1/inbox?"+query, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body %s)", query, rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" {
			t.Errorf("%s: code = %q, want invalid_argument", query, body.Code)
		}
		if len(body.Details) != 1 || body.Details[0].Field != "limit" {
			t.Errorf("%s: details = %+v, want one limit detail", query, body.Details)
		}
	}

	for _, query := range []string{"limit=1", "limit=200", ""} {
		rec := doRequest(t, h, http.MethodGet, "/v1/inbox?"+query, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (body %s)", query, rec.Code, rec.Body.String())
		}
	}
}

// TestInboxHTTPLimitSelectsOldest proves limit=1 keeps the oldest item and
// that the default limit is 50 rather than "all rows".
func TestInboxHTTPLimitSelectsOldest(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, st, _ := newInboxHTTPEnv(t, 72, now)

	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 55; i++ {
		ts := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		seedInboxRow(t, st, "project", "APP-"+twoDigits(i), domain.StatusPlanReview, "项目", ts, nil)
	}

	rec := doRequest(t, h, http.MethodGet, "/v1/inbox?limit=1", nil)
	body := decodeInbox(t, rec)
	if len(body.Items) != 1 || body.Items[0].ID != "APP-00" {
		t.Fatalf("limit=1 items = %+v, want the oldest APP-00", body.Items)
	}
	if body.Items[0].AgeSeconds != 110*60 {
		t.Errorf("age_seconds = %d, want 6600", body.Items[0].AgeSeconds)
	}

	rec = doRequest(t, h, http.MethodGet, "/v1/inbox", nil)
	body = decodeInbox(t, rec)
	if body.Count != 50 || len(body.Items) != 50 {
		t.Fatalf("default limit returned %d items, want 50", len(body.Items))
	}
	if body.Items[0].ID != "APP-00" || body.Items[49].ID != "APP-49" {
		t.Errorf("default page = %s…%s, want APP-00…APP-49", body.Items[0].ID, body.Items[49].ID)
	}
}

// TestInboxHTTPNoCache is acceptance criterion 13 over HTTP.
func TestInboxHTTPNoCache(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, st, _ := newInboxHTTPEnv(t, 72, now)
	seedInboxRow(t, st, "project", "APP-1", domain.StatusSpecReview, "订单导出", "2026-10-24T09:50:00Z", nil)

	first := decodeInbox(t, doRequest(t, h, http.MethodGet, "/v1/inbox", nil))
	if first.Count != 1 {
		t.Fatalf("first count = %d, want 1", first.Count)
	}

	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET status = ? WHERE project_id = 'APP-1'`, domain.StatusAccepted); err != nil {
		t.Fatalf("approve project: %v", err)
	}

	second := decodeInbox(t, doRequest(t, h, http.MethodGet, "/v1/inbox", nil))
	if second.Count != 0 || len(second.Items) != 0 {
		t.Fatalf("second items = %+v, want none after the decision", second.Items)
	}
}

// TestInboxHTTPStaleThresholdFromConfig proves the threshold is injected: the
// same 2h-old item is fresh at 72h and stale at 1h, and review_stale_hours
// echoes the value used.
func TestInboxHTTPStaleThresholdFromConfig(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		staleHours int
		wantStale  bool
	}{
		{name: "72h", staleHours: 72, wantStale: false},
		{name: "1h", staleHours: 1, wantStale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newInboxHTTPEnv(t, tc.staleHours, now)
			seedInboxRow(t, st, "project", "APP-1", domain.StatusDeliveryReview,
				"订单导出", "2026-10-27T07:50:00Z", nil) // 2h old

			body := decodeInbox(t, doRequest(t, h, http.MethodGet, "/v1/inbox", nil))
			if body.ReviewStaleHours != tc.staleHours {
				t.Fatalf("review_stale_hours = %d, want %d", body.ReviewStaleHours, tc.staleHours)
			}
			if len(body.Items) != 1 {
				t.Fatalf("items = %+v, want 1", body.Items)
			}
			if body.Items[0].Stale != tc.wantStale {
				t.Fatalf("stale = %v, want %v", body.Items[0].Stale, tc.wantStale)
			}
			if body.Items[0].AgeSeconds != 7200 {
				t.Fatalf("age_seconds = %d, want 7200", body.Items[0].AgeSeconds)
			}
		})
	}
}

func TestInboxHTTPMethodNotAllowed(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC)
	h, _, _ := newInboxHTTPEnv(t, 72, now)

	rec := doRequest(t, h, http.MethodPost, "/v1/inbox", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", body.Code)
	}
}

// TestInboxHTTPNotRegisteredWithoutService keeps the health-only router
// behaviour: /v1/inbox is a 404, not a panic.
func TestInboxHTTPNotRegisteredWithoutService(t *testing.T) {
	h := testServer(t)
	rec := doRequest(t, h, http.MethodGet, "/v1/inbox", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func twoDigits(i int) string {
	const digits = "0123456789"
	return string([]byte{digits[i/10], digits[i%10]})
}
