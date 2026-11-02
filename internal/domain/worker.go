package domain

import "time"

// Worker liveness and occupancy values (tech design v0.6 §2.3 / §6).
//
// State is persisted in workers.state (CHECK 'idle'|'busy'); presence is
// derived, never stored — it is a function of last_seen, the current instant
// and the configured TTL, so a crashed worker becomes offline without anyone
// writing to the database.
const (
	// PresenceActive is a worker whose last heartbeat is inside the TTL.
	PresenceActive = "active"
	// PresenceOffline is a worker whose last heartbeat is older than the TTL.
	PresenceOffline = "offline"

	// WorkerStateIdle is a registered worker holding no task.
	WorkerStateIdle = "idle"
	// WorkerStateBusy is a worker that holds a task pointer.
	WorkerStateBusy = "busy"
)

// Presence reports the liveness of a worker seen at lastSeen.
//
// The boundary is strict on the TTL side: a heartbeat exactly one TTL old is
// still active, only a strictly older one is offline. With the default 45s TTL
// that means 44s → active, 45s → active, 46s → offline, which is what the
// design's "45s 内视为在线" wording requires and what keeps a worker that
// heartbeats exactly on the interval from flapping.
//
// A lastSeen in the future (clock skew between the worker and the server) is
// active rather than an error: the worker just reported, and refusing to show
// it would be worse than tolerating the skew.
func Presence(lastSeen, now time.Time, ttl time.Duration) string {
	if now.Sub(lastSeen) > ttl {
		return PresenceOffline
	}
	return PresenceActive
}
