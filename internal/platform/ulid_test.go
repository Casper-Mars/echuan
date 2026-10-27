package platform

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// ulidCharset is Crockford's base32 alphabet, the character set NewULID uses.
const ulidCharset = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// resetULIDState clears the monotonic generator so a test observes a fresh
// random start instead of the state left behind by an earlier test.
func resetULIDState() {
	ulidMu.Lock()
	ulidLastMS = 0
	ulidLast = [ulidRandomBytes]byte{}
	ulidSeeded = false
	ulidMu.Unlock()
}

// ulidValue decodes id back into its 16 raw bytes. Crockford's alphabet is
// ASCII-ordered like its digits, so strings and values sort identically; this
// helper exists for tests that need to inspect the timestamp prefix.
func ulidValue(t *testing.T, id string) [16]byte {
	t.Helper()
	raw, err := decodeULID(id)
	if err != nil {
		t.Fatalf("decode %q: %v", id, err)
	}
	return raw
}

func TestNewULIDShape(t *testing.T) {
	resetULIDState()

	now := time.Date(2026, 3, 4, 5, 6, 7, 890*int(time.Millisecond), time.UTC)
	id, err := NewULID(now)
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}

	if len(id) != ULIDLength {
		t.Fatalf("len(id) = %d, want %d (%q)", len(id), ULIDLength, id)
	}
	if id[0] < '0' || id[0] > '7' {
		t.Errorf("first character = %q, want '0'-'7' (two leading zero bits)", id[0])
	}
	for i, r := range id {
		if !strings.ContainsRune(ulidCharset, r) {
			t.Fatalf("id %q contains %q at %d, outside the Crockford alphabet", id, r, i)
		}
	}

	raw := ulidValue(t, id)
	if got, want := millisFromBytes(raw), now.UnixMilli(); got != want {
		t.Errorf("timestamp prefix = %d, want %d", got, want)
	}

	decoded, err := DecodeULIDMillis(id)
	if err != nil {
		t.Fatalf("DecodeULIDMillis: %v", err)
	}
	if decoded != now.UnixMilli() {
		t.Errorf("DecodeULIDMillis = %d, want %d", decoded, now.UnixMilli())
	}
}

// TestNewULIDMonotonicSameMillisecond checks that 1000 ids created inside one
// millisecond are strictly increasing as strings. Comparing the rendered
// strings directly is the point: Crockford's ASCII order matches digit order,
// so "sorts by string" and "sorts by time" agree.
func TestNewULIDMonotonicSameMillisecond(t *testing.T) {
	resetULIDState()

	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	prev := mustULID(t, now)
	for i := 0; i < 1000; i++ {
		cur := mustULID(t, now)
		if !(cur > prev) {
			t.Fatalf("id %d did not increase as a string: %q <= %q", i, cur, prev)
		}
		prev = cur
	}
}

func TestNewULIDTimestampAdvances(t *testing.T) {
	resetULIDState()

	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	prev := mustULID(t, base)
	// The leading characters carry the timestamp, so ids one millisecond
	// apart must also sort in time order as strings.
	for i := 1; i <= 10; i++ {
		now := base.Add(time.Duration(i) * time.Millisecond)
		cur := mustULID(t, now)
		if !(cur > prev) {
			t.Fatalf("id at +%dms did not increase: %q <= %q", i, cur, prev)
		}
		if cur[:10] <= prev[:10] {
			t.Fatalf("prefix at +%dms did not advance: %q <= %q", i, cur[:10], prev[:10])
		}
		if got, want := millisFromBytes(ulidValue(t, cur)), now.UnixMilli(); got != want {
			t.Fatalf("timestamp prefix = %d, want %d", got, want)
		}
		prev = cur
	}
}

func TestNewULIDClockBackwardsStaysMonotonic(t *testing.T) {
	resetULIDState()

	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	prev := mustULID(t, base)
	cur := mustULID(t, base.Add(-time.Second))
	if !(cur > prev) {
		t.Fatalf("id after clock rollback did not increase: %q <= %q", cur, prev)
	}
	if got, want := millisFromBytes(ulidValue(t, cur)), base.UnixMilli(); got != want {
		t.Fatalf("rollback id timestamp = %d, want the last used millisecond %d", got, want)
	}
}

func TestNewULIDConcurrentUnique(t *testing.T) {
	resetULIDState()

	const n = 10000

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = make(map[string]struct{}, n)
	)
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Mix same-millisecond bursts with advancing timestamps.
			ts := now.Add(time.Duration(i%3) * time.Millisecond)
			id, err := NewULID(ts)
			if err != nil {
				t.Errorf("NewULID: %v", err)
				return
			}
			mu.Lock()
			ids[id] = struct{}{}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("got %d unique ids, want %d (%d collisions)", len(ids), n, n-len(ids))
	}
}

func TestNewWorkerID(t *testing.T) {
	resetULIDState()

	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	id, err := NewWorkerID(now)
	if err != nil {
		t.Fatalf("NewWorkerID: %v", err)
	}
	if WorkerIDLength != 28 {
		t.Fatalf("WorkerIDLength = %d, want 28", WorkerIDLength)
	}
	if len(id) != 28 {
		t.Fatalf("len(id) = %d, want 28 (%q)", len(id), id)
	}
	if !strings.HasPrefix(id, "w_") {
		t.Fatalf("id = %q, want prefix %q", id, WorkerIDPrefix)
	}
	suffix := strings.TrimPrefix(id, WorkerIDPrefix)
	if got, err := DecodeULIDMillis(suffix); err != nil {
		t.Fatalf("worker id suffix is not a valid ulid: %v", err)
	} else if want := now.UnixMilli(); got != want {
		t.Fatalf("worker id timestamp = %d, want %d", got, want)
	}

	const n = 10000
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = make(map[string]struct{}, n)
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := NewWorkerID(now)
			if err != nil {
				t.Errorf("NewWorkerID: %v", err)
				return
			}
			if len(id) != 28 || !strings.HasPrefix(id, "w_") {
				t.Errorf("malformed worker id %q", id)
				return
			}
			mu.Lock()
			ids[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("got %d unique worker ids, want %d", len(ids), n)
	}
}

// TestNewULIDRoundTrip pins the encode/decode pair, including the 2-bit
// left padding: the highest value the 48-bit timestamp can take still fits in
// the first character.
func TestNewULIDRoundTrip(t *testing.T) {
	cases := []string{
		"00000000000000000000000000",
		"7ZZZZZZZZZZZZZZZZZZZZZZZZZ",
		"01JBC9Z3K4QW8V0M2X7Y5N6P8R",
	}
	for _, want := range cases {
		raw, err := decodeULID(want)
		if err != nil {
			t.Fatalf("decodeULID(%q): %v", want, err)
		}
		if got := encodeULID(raw); got != want {
			t.Errorf("round trip = %q, want %q", got, want)
		}
	}
}

// TestDecodeULIDMillisRejectsBadInput covers length, alphabet and padding
// violations.
func TestDecodeULIDMillisRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "short", id: "ABC"},
		{name: "long", id: strings.Repeat("0", 27)},
		{name: "lowercase", id: "01jbc9z3k4qw8v0m2x7y5n6p8r"},
		{name: "excluded letter I", id: "0I000000000000000000000000"},
		{name: "excluded letter L", id: "0L000000000000000000000000"},
		{name: "excluded letter O", id: "0O000000000000000000000000"},
		{name: "excluded letter U", id: "0U000000000000000000000000"},
		{name: "rfc4648 alphabet", id: "A1JBC9Z3K4QW8V0M2X7Y5N6P8"},
		{name: "padding bits set", id: "Z0000000000000000000000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeULIDMillis(tc.id); err == nil {
				t.Fatalf("DecodeULIDMillis(%q): expected error", tc.id)
			}
		})
	}
}

func BenchmarkNewULID(b *testing.B) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := NewULID(now); err != nil {
				b.Fatalf("NewULID: %v", err)
			}
		}
	})
}

func mustULID(t *testing.T, now time.Time) string {
	t.Helper()
	id, err := NewULID(now)
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	return id
}
