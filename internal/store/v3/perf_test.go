package v3

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// seedReadyDevTasks bulk-inserts n ready dev tasks into one project.
func seedReadyDevTasks(t *testing.T, st *storedb.Store, projectPK int64, n int) {
	t.Helper()
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(context.Background(), `
			INSERT INTO tasks (task_id, project_id, kind, kind_parallel, title, description_md, context_md,
			                   status, attempt, max_attempts, create_time, update_time)
			VALUES (?, ?, ?, 1, 'dev', '', '', ?, 0, 3, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := 0; i < n; i++ {
			if _, err := stmt.ExecContext(context.Background(),
				fmt.Sprintf("t_perf_%04d", i), projectPK, enum.Dev, enum.Ready, testNow, testNow); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed ready tasks: %v", err)
	}
}

// TestClaimTaskLatency seeds 1000 tasks and asserts the P95 claim time stays
// under 50ms. The threshold is deliberately loose; the point is to catch a
// missing index or an accidental full scan, not to benchmark SQLite.
func TestClaimTaskLatency(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "性能", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var projectPK int64
	if err := st.Read().QueryRowContext(ctx,
		`SELECT id FROM projects WHERE project_id = ?`, project.ProjectID).Scan(&projectPK); err != nil {
		t.Fatalf("project pk: %v", err)
	}
	seedReadyDevTasks(t, st, projectPK, 1000)

	const samples = 100
	durations := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		seedWorker(t, st, fmt.Sprintf("w_perf_%03d", i), testNow)
		start := time.Now()
		_, err := ClaimTask(ctx, st, ClaimInput{
			Kind:       enum.Dev,
			WorkerID:   fmt.Sprintf("w_perf_%03d", i),
			LeaseID:    fmt.Sprintf("l_perf_%03d", i),
			Now:        testNow,
			LeaseUntil: testUntil,
		})
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		durations = append(durations, elapsed)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	index := int(float64(len(durations))*0.95) - 1
	if index < 0 {
		index = 0
	}
	p95 := durations[index]
	if p95 > 50*time.Millisecond {
		t.Fatalf("claim P95 = %v, want < 50ms (all: min=%v max=%v)",
			p95, durations[0], durations[len(durations)-1])
	}
	t.Logf("claim latency: P50=%v P95=%v max=%v", durations[len(durations)/2], p95, durations[len(durations)-1])
}
