package v3

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

var testClock = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

// newTestService opens a migrated store and a service on a controllable clock.
func newTestService(t *testing.T, now *time.Time) (*Service, *storedb.Store) {
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

	svc := New(st).WithClock(func() time.Time { return *now }).WithLeaseTTL(2 * time.Second)
	return svc, st
}

// seedProductOnly inserts a product and returns its business id.
func seedProductOnly(t *testing.T, st *storedb.Store, code string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := storedb.CreateProduct(ctx, tx, storedb.NewProduct{
			ProductID:  code,
			Name:       code,
			GitURL:     "https://example.com/" + code + ".git",
			CreateTime: formatTime(testClock),
			UpdateTime: formatTime(testClock),
		})
		return err
	}); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return code
}

// seedProject inserts a product and an open project and returns the project id.
func seedProject(t *testing.T, st *storedb.Store, code string) string {
	t.Helper()
	seedProductOnly(t, st, code)
	ctx := context.Background()
	project, err := storev3.CreateProject(ctx, st, storev3.CreateProjectInput{
		ProductID: code,
		Name:      code,
		Now:       formatTime(testClock),
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return project.ProjectID
}

func createMaterial(t *testing.T, svc *Service, materialID string) string {
	t.Helper()
	got, err := svc.CreateMaterial(context.Background(), CreateMaterialRequest{
		MaterialID: materialID,
		Kind:       enum.MaterialRequirement,
		Version:    1,
		Title:      materialID,
		Sha256:     "sha-" + materialID,
		Size:       1,
		Path:       "/tmp/" + materialID,
		UploadedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}
	return got
}

// seedWorker registers a worker so Claim accepts it: Claim checks the registry
// first and answers 409 otherwise (issue #165 §2). Registering before claiming is
// what a real client does (one heartbeat, then claim).
func seedWorker(t *testing.T, st *storedb.Store, workerID string) {
	t.Helper()
	if err := storev3.UpsertWorker(context.Background(), st, workerID, "{}", formatTime(testClock)); err != nil {
		t.Fatalf("UpsertWorker %s: %v", workerID, err)
	}
}

// createReadyTask is the common one-task seed.
func createReadyTask(t *testing.T, svc *Service, projectID, taskID string, kind enum.TaskKind) storev3.Task {
	t.Helper()
	task, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID,
		TaskID:    taskID,
		Kind:      kind,
		Title:     taskID,
	})
	if err != nil {
		t.Fatalf("CreateTask %s: %v", taskID, err)
	}
	return task
}
