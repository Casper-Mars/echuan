package main

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// TestConfigMaxAttemptsReachesCreatedTask walks the whole production path:
// TOML → platform.Load → newTaskService (the wiring in run) → CreateTask →
// persisted row. It is the regression guard for "the configuration is never
// wired into the task service": delete WithMaxAttempts from newTaskService and
// this test must fail (the service then falls back to the built-in 3).
//
// The configured value is 7 on purpose: a hard-coded 3 anywhere on the path
// cannot pass.
func TestConfigMaxAttemptsReachesCreatedTask(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "echuan.toml")
	toml := "listen = \"127.0.0.1:18720\"\n" +
		"data_dir = \"" + dataDir + "\"\n" +
		"read_pool_size = 2\n" +
		"max_attempts = 7\n"
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := platform.Load(configPath)
	if err != nil {
		t.Fatalf("platform.Load: %v", err)
	}
	if cfg.MaxAttempts != 7 {
		t.Fatalf("cfg.MaxAttempts = %d, want 7 (fixture is broken)", cfg.MaxAttempts)
	}

	st, err := openStore(cfg, platform.NewLogger("error", io.Discard))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("close store: %v", cerr)
		}
	})

	taskService := newTaskService(st, cfg)
	projectID := seedWiringProject(t, st, "WIRE")
	ctx := context.Background()

	cases := []struct {
		name     string
		taskID   string
		explicit int
		want     int
	}{
		{name: "omitted uses configured value", taskID: "t_wire_omitted", explicit: 0, want: 7},
		{name: "explicit value wins", taskID: "t_wire_explicit", explicit: 1, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := taskService.CreateTask(ctx, appv3.CreateTaskRequest{
				ProjectID: projectID, TaskID: tc.taskID, Kind: enum.Dev, Title: tc.taskID,
				MaxAttempts: tc.explicit,
			})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			if created.MaxAttempts != tc.want {
				t.Errorf("created.MaxAttempts = %d, want %d", created.MaxAttempts, tc.want)
			}
			// Read the row back through the service so the assertion is about
			// what landed in the database, not only the in-memory result.
			got, err := taskService.GetTask(ctx, tc.taskID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if got.MaxAttempts != tc.want {
				t.Errorf("persisted max_attempts = %d, want %d", got.MaxAttempts, tc.want)
			}
		})
	}
}

// seedWiringProject inserts one product and one open project, returning the
// project business id.
func seedWiringProject(t *testing.T, st *store.Store, code string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateProduct(ctx, tx, store.NewProduct{
			ProductID: code, Name: code, GitURL: "https://example.com/" + code + ".git",
			CreateTime: now, UpdateTime: now,
		})
		return err
	}); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	project, err := storev3.CreateProject(ctx, st, storev3.CreateProjectInput{
		ProductID: code, Name: code, Now: now,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return project.ProjectID
}
