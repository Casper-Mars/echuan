package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// TestConfigLeaseTTLReachesClaimAndWorkerService walks the production path for
// D-2 (issue #165): TOML → platform.Load → newTaskService / newWorkerService
// (the wiring in run) → claim. It is the regression guard for "lease_ttl is
// parsed and validated but never wired": delete WithLeaseTTL from
// newTaskService and the claim falls back to the built-in 60s while the worker
// service still publishes the configured value, which is exactly the two-source
// mismatch the card is about.
//
// 7s is deliberately no default (neither the 90s config default nor the 60s
// service default), so a hard-coded duration anywhere on the path fails.
func TestConfigLeaseTTLReachesClaimAndWorkerService(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "echuan.toml")
	toml := "listen = \"127.0.0.1:18721\"\n" +
		"data_dir = \"" + dataDir + "\"\n" +
		"read_pool_size = 2\n" +
		"lease_ttl = \"7s\"\n"
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := platform.Load(configPath)
	if err != nil {
		t.Fatalf("platform.Load: %v", err)
	}
	if cfg.LeaseTTL != 7*time.Second {
		t.Fatalf("cfg.LeaseTTL = %s, want 7s (fixture is broken)", cfg.LeaseTTL)
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
	workerService := newWorkerService(st, cfg)

	// Same source: the value the detail endpoint publishes is the configured one.
	if got := workerService.Rhythm().LeaseTTL; got != cfg.LeaseTTL {
		t.Errorf("worker rhythm lease_ttl = %s, want %s", got, cfg.LeaseTTL)
	}

	projectID := seedWiringProject(t, st, "LTTL")
	ctx := context.Background()
	if err := workerService.Heartbeat(ctx, "w_ttl", "", []enum.TaskKind{enum.Dev}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := taskService.CreateTask(ctx, appv3.CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_wire_lease", Kind: enum.Dev, Title: "租约接线",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	before := time.Now().UTC()
	claimed, err := taskService.Claim(ctx, "w_ttl", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.LeaseUntil == nil {
		t.Fatalf("claimed task has no lease_until")
	}
	until, err := time.Parse("2006-01-02T15:04:05.000Z", *claimed.LeaseUntil)
	if err != nil {
		t.Fatalf("parse lease_until %q: %v", *claimed.LeaseUntil, err)
	}
	if want := before.Add(cfg.LeaseTTL); until.Before(want.Add(-time.Second)) {
		t.Errorf("lease_until = %s, want >= %s (now+%s)", *claimed.LeaseUntil, want.Format("2006-01-02T15:04:05.000Z"), cfg.LeaseTTL)
	}
	if got := until.Sub(before); got > cfg.LeaseTTL+time.Second {
		t.Errorf("lease duration = %s, want %s (config lease_ttl, not the built-in 60s)", got, cfg.LeaseTTL)
	}
}
