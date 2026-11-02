package domain

import (
	"testing"
	"time"
)

// TestPresenceBoundary pins the offline boundary of tech design §6: a worker
// that has not been seen for more than the TTL is offline, and exactly one TTL
// old is still active. With the default 45s TTL that is 44s active, 45s active,
// 46s offline.
func TestPresenceBoundary(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	ttl := 45 * time.Second

	cases := []struct {
		name     string
		lastSeen time.Time
		want     string
	}{
		{"44s ago", now.Add(-44 * time.Second), PresenceActive},
		{"45s ago (exactly the TTL)", now.Add(-45 * time.Second), PresenceActive},
		{"46s ago", now.Add(-46 * time.Second), PresenceOffline},
		{"just now", now, PresenceActive},
		{"ahead of the clock (skew)", now.Add(5 * time.Second), PresenceActive},
		{"an hour ago", now.Add(-time.Hour), PresenceOffline},
	}

	for _, tc := range cases {
		if got := Presence(tc.lastSeen, now, ttl); got != tc.want {
			t.Errorf("Presence(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPresenceZeroTTL documents the degenerate configuration: an empty TTL
// means "offline unless the heartbeat is current", and even then the boundary
// stays strict.
func TestPresenceZeroTTL(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	if got := Presence(now, now, 0); got != PresenceActive {
		t.Errorf("Presence(same instant, ttl 0) = %q, want %q", got, PresenceActive)
	}
	if got := Presence(now.Add(-time.Nanosecond), now, 0); got != PresenceOffline {
		t.Errorf("Presence(1ns old, ttl 0) = %q, want %q", got, PresenceOffline)
	}
}

// TestWorkerStateConstants keeps the persisted state values (workers.state
// CHECK) stable; the store writes the same literals.
func TestWorkerStateConstants(t *testing.T) {
	if WorkerStateIdle != "idle" || WorkerStateBusy != "busy" {
		t.Errorf("state constants = %q/%q, want idle/busy", WorkerStateIdle, WorkerStateBusy)
	}
	if PresenceActive != "active" || PresenceOffline != "offline" {
		t.Errorf("presence constants = %q/%q, want active/offline", PresenceActive, PresenceOffline)
	}
}
