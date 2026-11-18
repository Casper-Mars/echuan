package v3

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

const (
	testNow   = "2026-11-18T00:00:00.000Z"
	testUntil = "2099-01-01T00:00:00.000Z"
)

// testStore opens a migrated V3 database with a read pool.
func testStore(t *testing.T) *storedb.Store {
	t.Helper()

	st, err := storedb.Open(storedb.Config{
		Path:         filepath.Join(t.TempDir(), "state.db"),
		ReadPoolSize: 4,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.OpenReadPool(context.Background()); err != nil {
		t.Fatalf("open read pool: %v", err)
	}
	return st
}

// seedProduct inserts a product and returns its physical id.
func seedProduct(t *testing.T, st *storedb.Store, code string) int64 {
	t.Helper()
	var pk int64
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		id, err := storedb.CreateProduct(context.Background(), tx, storedb.NewProduct{
			ProductID:  code,
			Name:       code,
			GitURL:     "https://example.com/" + code + ".git",
			CreateTime: testNow,
			UpdateTime: testNow,
		})
		pk = id
		return err
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return pk
}

// claimAndReport moves one task from ready to in_review.
func claimAndReport(t *testing.T, st *storedb.Store, taskID string, kind enum.TaskKind) {
	t.Helper()
	ctx := context.Background()
	leaseID := "l_" + taskID
	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind:       kind,
		WorkerID:   "w_" + taskID,
		LeaseID:    leaseID,
		Now:        testNow,
		LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("claim %s: %v", taskID, err)
	}
	if err := ReportTask(ctx, st, taskID, leaseID, testNow); err != nil {
		t.Fatalf("report %s: %v", taskID, err)
	}
}

// claimReportApprove drives one task through claim → report → approve.
func claimReportApprove(t *testing.T, st *storedb.Store, taskID string, kind enum.TaskKind) {
	t.Helper()
	claimAndReport(t, st, taskID, kind)
	if _, err := DecideTask(context.Background(), st, DecideInput{
		TaskID: taskID, Decision: enum.Approve, DecidedBy: "cto", Now: testNow,
	}); err != nil {
		t.Fatalf("approve %s: %v", taskID, err)
	}
}
