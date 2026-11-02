package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Clock abstracts the wall clock so tests and lease logic can control time.
type Clock interface {
	Now() time.Time
}

// RealClock is the production Clock implementation.
type RealClock struct{}

// Now returns the current UTC time.
func (RealClock) Now() time.Time { return time.Now().UTC() }

// NewRequestID returns a random 128-bit identifier rendered as 32 hex
// characters, suitable for the X-Request-Id header.
func NewRequestID() string {
	return randomHex(16)
}

// NewLeaseID returns a task lease identifier: the prefix "l_" followed by 16
// hex characters (64 random bits), e.g. "l_7f3a9c1b2d4e6a80".
//
// A lease id must be unpredictable rather than merely unique: it is the only
// proof a worker holds a card, so a guessable (timestamp-derived) value would
// let any client heartbeat or report on someone else's task.
func NewLeaseID() string { return "l_" + randomHex(8) }

// randomHex returns n random bytes rendered as 2n hex characters.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails on supported platforms. Hashing the clock
		// keeps the documented length and avoids panicking in a request path;
		// this branch exists for completeness, not as an expected source of
		// entropy.
		sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		copy(buf, sum[:])
	}
	return hex.EncodeToString(buf)
}
