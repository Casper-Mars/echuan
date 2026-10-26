package platform

import (
	"crypto/rand"
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
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// timestamp-derived value instead of panicking in a request path.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}
