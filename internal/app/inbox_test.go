package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// seedInboxFixture inserts one product, one project in the given status and one
// task in the given status, both stamped with updateTime.
func seedInboxFixture(t *testing.T, st *store.Store, projectStatus, taskStatus int, updateTime string) {
	t.Helper()
	ctx := context.Background()

	res, err := st.Write().ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', 'APP 产品', 'git@example.com:APP.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productFK, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}

	res, err = st.Write().ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, reason, version, create_time, update_time)
		VALUES ('APP-1', ?, 1, '订单导出', ?, '被驳回：缺少边界', 1, ?, ?)`,
		productFK, projectStatus, updateTime, updateTime)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectFK, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}

	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority, status,
		                   attempt_count, max_attempts, reason, version, create_time, update_time)
		VALUES ('APP-1-T0001', ?, 1, '补并发测试', 'body', 0, ?, 3, 3, 'attempt 用尽', 1, ?, ?)`,
		projectFK, taskStatus, updateTime, updateTime); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func newInboxEnv(t *testing.T, staleHours int) (*InboxService, *store.Store, *fakeClock) {
	t.Helper()
	e := newProjectTestEnv(t)
	service := NewInboxService(e.store, nil, staleHours)
	clock := newFakeClock()
	service.SetClock(clock)
	return service, e.store, clock
}

func TestInboxEmpty(t *testing.T) {
	service, _, clock := newInboxEnv(t, 0) // 0 falls back to the 72h default

	result, err := service.Inbox(context.Background(), 50)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if result.Items == nil {
		t.Fatal("Items = nil, want an empty non-nil slice")
	}
	if len(result.Items) != 0 {
		t.Fatalf("len(Items) = %d, want 0", len(result.Items))
	}
	if result.ReviewStaleHours != 72 {
		t.Fatalf("review_stale_hours = %d, want 72", result.ReviewStaleHours)
	}
	if !result.GeneratedAt.Equal(clock.Now()) {
		t.Fatalf("generated_at = %s, want the clock reading %s", result.GeneratedAt, clock.Now())
	}
}

func TestInboxLimitValidation(t *testing.T) {
	service, _, _ := newInboxEnv(t, 72)

	for _, limit := range []int{0, -1, 201} {
		_, err := service.Inbox(context.Background(), limit)
		if err == nil {
			t.Fatalf("limit %d accepted", limit)
		}
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("limit %d: err = %v, want invalid argument", limit, err)
		}
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("limit %d: err = %T, want *domain.ValidationError", limit, err)
		}
		if len(validation.Fields) != 1 || validation.Fields[0].Field != "limit" {
			t.Fatalf("limit %d: fields = %+v, want a limit detail", limit, validation.Fields)
		}
	}
}

func TestInboxDerivesAgeAndStaleFromConfiguration(t *testing.T) {
	service, st, clock := newInboxEnv(t, 1)
	// 2h old → stale at a 1h threshold; the task is 30m old → not stale.
	seedInboxFixture(t, st, domain.StatusSpecReview, domain.TaskPendingAcceptance,
		clock.Now().Add(-2*time.Hour).Format(time.RFC3339Nano))
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE tasks SET update_time = ?`, clock.Now().Add(-30*time.Minute).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("restamp task: %v", err)
	}

	result, err := service.Inbox(context.Background(), 50)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if result.ReviewStaleHours != 1 {
		t.Fatalf("review_stale_hours = %d, want 1", result.ReviewStaleHours)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(result.Items))
	}

	// Oldest first: the 2h project, then the 30m task.
	project := result.Items[0]
	if project.Kind != "project" || project.ID != "APP-1" || project.StateName != "spec_review" {
		t.Fatalf("first item = %+v", project)
	}
	if project.AgeSeconds() != int64(2*time.Hour/time.Second) || !project.Stale {
		t.Errorf("project age/stale = %d/%v, want 7200/true", project.AgeSeconds(), project.Stale)
	}
	if project.Reason == nil || *project.Reason != "被驳回：缺少边界" {
		t.Errorf("project reason = %v", project.Reason)
	}

	task := result.Items[1]
	if task.Kind != "task" || task.ID != "APP-1-T0001" || task.StateName != "pending_acceptance" {
		t.Fatalf("second item = %+v", task)
	}
	if task.AgeSeconds() != 1800 || task.Stale {
		t.Errorf("task age/stale = %d/%v, want 1800/false", task.AgeSeconds(), task.Stale)
	}
	if task.Reason == nil || *task.Reason != "attempt 用尽" {
		t.Errorf("task reason = %v", task.Reason)
	}
}

// TestInboxIsNotCached is acceptance criterion 13: a database change is
// visible on the next call without restart or wait.
func TestInboxIsNotCached(t *testing.T) {
	service, st, clock := newInboxEnv(t, 72)
	seedInboxFixture(t, st, domain.StatusSpecReview, domain.TaskFailed,
		clock.Now().Add(-time.Hour).Format(time.RFC3339Nano))

	first, err := service.Inbox(context.Background(), 50)
	if err != nil {
		t.Fatalf("first Inbox: %v", err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("len(first) = %d, want 2", len(first.Items))
	}

	// A decision on the project leaves the inbox; a retry clears the task.
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET status = ?, reason = NULL`, domain.StatusSpecDraft); err != nil {
		t.Fatalf("decide project: %v", err)
	}
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE tasks SET status = ?, reason = NULL`, domain.TaskReady); err != nil {
		t.Fatalf("retry task: %v", err)
	}

	second, err := service.Inbox(context.Background(), 50)
	if err != nil {
		t.Fatalf("second Inbox: %v", err)
	}
	if len(second.Items) != 0 {
		t.Fatalf("items after decisions = %+v, want none", second.Items)
	}

	// Moving update_time backwards makes the age grow on the next read.
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET status = ?`, domain.StatusSpecReview); err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET update_time = ?`, clock.Now().Add(-3*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("backdate project: %v", err)
	}

	third, err := service.Inbox(context.Background(), 50)
	if err != nil {
		t.Fatalf("third Inbox: %v", err)
	}
	if len(third.Items) != 1 {
		t.Fatalf("len(third) = %d, want 1", len(third.Items))
	}
	if third.Items[0].AgeSeconds() != 3*24*3600 || !third.Items[0].Stale {
		t.Fatalf("age/stale = %d/%v, want 259200/true", third.Items[0].AgeSeconds(), third.Items[0].Stale)
	}
}

func TestInboxRespectsLimit(t *testing.T) {
	service, st, clock := newInboxEnv(t, 72)
	ctx := context.Background()

	res, err := st.Write().ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', 'APP 产品', 'git@example.com:APP.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productFK, _ := res.LastInsertId()
	for i := 1; i <= 3; i++ {
		updateTime := clock.Now().Add(-time.Duration(i) * time.Hour).Format(time.RFC3339Nano)
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO projects (project_id, product_id, seq_no, title, status, version, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
			"APP-"+string(rune('0'+i)), productFK, i, "项目", domain.StatusSpecReview, updateTime, updateTime); err != nil {
			t.Fatalf("insert project %d: %v", i, err)
		}
	}

	result, err := service.Inbox(ctx, 1)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].ID != "APP-3" || result.Items[0].AgeSeconds() != 3*3600 {
		t.Fatalf("item = %+v, want the oldest project APP-3", result.Items[0])
	}
}
