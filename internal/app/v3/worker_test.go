package v3

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// registerWorkerAt inserts one registry row with an explicit last_seen, which is
// the only way to place a node on either side of worker_ttl without waiting.
func registerWorkerAt(t *testing.T, svc *WorkerService, workerID string, lastSeen time.Time) {
	t.Helper()
	if err := storev3.UpsertWorker(context.Background(), svc.st, workerID, "{}", formatTime(lastSeen)); err != nil {
		t.Fatalf("UpsertWorker %s: %v", workerID, err)
	}
}

// TestPresenceIsDerivedByOneRule pins AC5 at the application layer: the listing
// and the detail endpoint must answer the same presence for one row, and the
// answer must follow worker_ttl (40s inside the default 45s → active, 50s
// outside → offline). The exact 45s boundary is covered by
// internal/domain/worker_test.go, because an HTTP round trip cannot hit it.
func TestPresenceIsDerivedByOneRule(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	workers := NewWorkerService(st).WithClock(func() time.Time { return now })

	registerWorkerAt(t, workers, "w_fresh", now.Add(-40*time.Second))
	registerWorkerAt(t, workers, "w_stale", now.Add(-50*time.Second))

	list, err := workers.List(context.Background(), 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	presences := workers.PresenceOf(list)
	if len(presences) != len(list) {
		t.Fatalf("presences = %d, workers = %d", len(presences), len(list))
	}
	byID := make(map[string]string, len(list))
	for i, worker := range list {
		byID[worker.WorkerID] = presences[i]
	}
	want := map[string]string{"w_fresh": "active", "w_stale": "offline"}
	for workerID, presence := range want {
		if got := byID[workerID]; got != presence {
			t.Errorf("list presence %s = %q, want %q", workerID, got, presence)
		}
		worker, err := workers.Get(context.Background(), workerID)
		if err != nil {
			t.Fatalf("Get %s: %v", workerID, err)
		}
		if got := workers.Presence(worker); got != presence {
			t.Errorf("detail presence %s = %q, want %q (list and detail must agree)", workerID, got, presence)
		}
	}
}

// TestPresenceDegradesDirtyLastSeenDocumented pins the fallback: a row whose
// last_seen cannot be parsed is reported offline instead of failing the page.
func TestPresenceDegradesDirtyLastSeen(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	workers := NewWorkerService(st).WithClock(func() time.Time { return now })

	if err := storev3.UpsertWorker(context.Background(), st, "w_dirty", "{}", "not-a-timestamp"); err != nil {
		t.Fatalf("UpsertWorker: %v", err)
	}
	worker, err := workers.Get(context.Background(), "w_dirty")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := workers.Presence(worker); got != "offline" {
		t.Errorf("presence of unparseable last_seen = %q, want offline", got)
	}
}

// TestGetMissingWorkerIs404 pins the detail endpoint's not-found class at the
// application layer (the HTTP code is asserted in internal/api).
func TestGetMissingWorkerIs404(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	workers := NewWorkerService(st).WithClock(func() time.Time { return now })

	if _, err := workers.Get(context.Background(), "w_missing"); StatusOf(err) != 404 {
		t.Fatalf("Get(missing) status = %d (%v), want 404", StatusOf(err), err)
	}
}

// TestClaimRequiresRegisteredWorker is AC7 at the application layer: both the
// queued and the empty-queue case answer 409 with the recovery instruction, and
// one heartbeat makes the same claim succeed.
func TestClaimRequiresRegisteredWorker(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Dev)

	// Unregistered id with a card waiting: refuse before taking it.
	_, err := svc.Claim(context.Background(), "w_ghost", enum.Dev)
	if got := StatusOf(err); got != 409 {
		t.Fatalf("claim(unregistered, queued) status = %d (%v), want 409", got, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "未注册") || !strings.Contains(msg, "心跳") {
		t.Errorf("409 message = %q, want it to name 未注册 and 心跳", msg)
	}

	// Unregistered id with an empty queue: the registry check runs first, so the
	// answer is still 409 instead of the 204 that would hide a stale id forever.
	_, err = svc.Claim(context.Background(), "w_ghost", enum.Requirement)
	if got := StatusOf(err); got != 409 {
		t.Fatalf("claim(unregistered, empty queue) status = %d (%v), want 409", got, err)
	}

	// The card is still ready: neither refused claim took it.
	task, err := svc.GetTask(context.Background(), "t_a")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Ready || task.WorkerID != nil {
		t.Fatalf("task after refused claims = status %s worker %v, want ready/nil", task.Status, task.WorkerID)
	}

	// One heartbeat registers the id and the next claim gets the card.
	workers := NewWorkerService(st)
	if err := workers.Heartbeat(context.Background(), "w_ghost", "{}", nil); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := svc.Claim(context.Background(), "w_ghost", enum.Dev); err != nil {
		t.Fatalf("claim after heartbeat: %v", err)
	}
}
