package platform

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// ULID helper.
//
// The identifier is 128 bits laid out like a ULID: a 48-bit big-endian
// millisecond timestamp followed by 80 random bits.
//
// # Encoding
//
// The value is rendered with Crockford's base32 alphabet
// ("0123456789ABCDEFGHJKMNPQRSTVWXYZ") following the ULID spec bit layout:
// the 128-bit value is left-padded with two zero bits to 130 bits and then
// split into 26 groups of five bits, most significant group first. Because of
// the two leading zero bits the first character is always '0'-'7', which is
// the shape of the ids in the design doc ("w_01JBC9Z3K4QW8V0M2X7Y5N6P8R").
//
// This is *not* how encoding/base32 lays bits out: StdEncoding pads at the
// tail, which would put the two spare bits at the end instead of the front
// and break the ordering property below. Hence the explicit 5-bit grouping.
//
// # Ordering
//
// Crockford's alphabet is ordered so that its ASCII order equals the numeric
// order of its digits, therefore the rendered strings sort exactly like the
// underlying 128-bit values. Within one millisecond the 80-bit entropy is
// incremented rather than re-randomised, so ids produced in the same
// millisecond are *strictly increasing as strings* as well as in value. If
// the entropy space is exhausted (practically impossible) the timestamp
// advances by one millisecond and the entropy restarts at zero. If the wall
// clock steps backwards the last used millisecond is kept so ids never go
// back.
//
// Material ids keep their historical RFC 4648 rendering; they are produced by
// internal/material from the raw bytes returned by NewULIDBytes, so this
// package's alphabet change does not affect them.

const (
	// ulidRandomBytes is the number of random bytes appended to the 6-byte
	// millisecond prefix (80 bits).
	ulidRandomBytes = 10
	// ulidTimeBytes is the length of the big-endian millisecond prefix.
	ulidTimeBytes = 6
	// ULIDLength is the length of a rendered identifier: 130 bits / 5.
	ULIDLength = 26
	// WorkerIDPrefix is prepended to a ULID to form a worker id.
	WorkerIDPrefix = "w_"
	// WorkerIDLength is the length of a worker id: prefix + ULID.
	WorkerIDLength = len(WorkerIDPrefix) + ULIDLength
)

// crockfordAlphabet is Crockford's base32 alphabet: 0-9 then A-Z without
// I, L, O and U. Its ASCII order matches the numeric order of the digits,
// which is what makes ULID strings sortable.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// crockfordDecode maps an ASCII byte to its 5-bit value; -1 means "not in the
// alphabet". Initialised once at start-up.
var crockfordDecode = func() [256]int8 {
	var table [256]int8
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(crockfordAlphabet); i++ {
		table[crockfordAlphabet[i]] = int8(i)
	}
	return table
}()

var (
	ulidMu     sync.Mutex
	ulidLastMS int64
	ulidLast   [ulidRandomBytes]byte
	ulidSeeded bool
)

// NewULID returns the Crockford base32 identifier for now: 48-bit millisecond
// timestamp plus 80 bits of monotonic entropy → 26 characters.
func NewULID(now time.Time) (string, error) {
	raw, err := NewULIDBytes(now)
	if err != nil {
		return "", err
	}
	return encodeULID(raw), nil
}

// NewULIDBytes returns the raw 16 bytes behind NewULID: a 48-bit big-endian
// millisecond timestamp followed by 80 bits of monotonic entropy.
//
// It is the shared generator for every id in the project. Callers that need a
// different rendering (material ids keep their historical RFC 4648 base32
// alphabet) encode these bytes themselves; the uniqueness and monotonicity
// guarantees are the same regardless of how the bytes are rendered.
func NewULIDBytes(now time.Time) ([16]byte, error) {
	var raw [16]byte

	ms := now.UnixMilli()
	ulidMu.Lock()
	defer ulidMu.Unlock()

	switch {
	case !ulidSeeded || ms > ulidLastMS:
		var entropy [ulidRandomBytes]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return [16]byte{}, fmt.Errorf("platform: generate ulid: %w", err)
		}
		ulidLastMS = ms
		ulidLast = entropy
		ulidSeeded = true
	default:
		// Same millisecond (or the clock stepped backwards): keep the last
		// millisecond and bump the entropy so the value keeps increasing.
		ms = ulidLastMS
		if !incrementEntropy(&ulidLast) {
			// 2^80 ids inside one millisecond: move to the next one.
			ulidLastMS++
			ms = ulidLastMS
			ulidLast = [ulidRandomBytes]byte{}
		}
	}

	writeMillis(raw[:ulidTimeBytes], uint64(ms))
	copy(raw[ulidTimeBytes:], ulidLast[:])
	return raw, nil
}

// NewWorkerID returns "w_" followed by a ULID, i.e. a 28 character worker id.
func NewWorkerID(now time.Time) (string, error) {
	id, err := NewULID(now)
	if err != nil {
		return "", err
	}
	return WorkerIDPrefix + id, nil
}

// DecodeULIDMillis reports the 48-bit millisecond timestamp carried by a
// Crockford-rendered ULID (the format NewULID produces).
func DecodeULIDMillis(id string) (int64, error) {
	raw, err := decodeULID(id)
	if err != nil {
		return 0, err
	}
	return millisFromBytes(raw), nil
}

// encodeULID renders raw as 26 Crockford characters using the ULID bit
// layout: the 128-bit value is left-padded with 2 zero bits to 130 bits and
// split into 26 groups of 5 bits, most significant group first.
//
// Group i (0-based) covers padded bits (129-5i) down to (125-5i). Only the
// first group (i = 0) reaches above bit 127, where the two padding bits of
// the 130-bit layout sit; every later group maps entirely onto value bits.
func encodeULID(raw [16]byte) string {
	var out [ULIDLength]byte
	for i := 0; i < ULIDLength; i++ {
		low := 125 - 5*i
		var v byte
		for k := 4; k >= 0; k-- {
			bit := low + k
			if bit < 0 || bit > 127 {
				// Padding bit: always zero.
				continue
			}
			if raw[15-bit/8]&(1<<uint(bit%8)) != 0 {
				v |= 1 << uint(k)
			}
		}
		out[i] = crockfordAlphabet[v]
	}
	return string(out[:])
}

// decodeULID parses a Crockford-rendered ULID back into its 16 raw bytes.
// Characters outside the alphabet (including lower case and I/L/O/U) are
// rejected, as is any id whose padding bits are non-zero.
func decodeULID(id string) ([16]byte, error) {
	var raw [16]byte
	if len(id) != ULIDLength {
		return raw, fmt.Errorf("platform: ulid %q: invalid length %d, want %d", id, len(id), ULIDLength)
	}
	for i := 0; i < ULIDLength; i++ {
		digit := crockfordDecode[id[i]]
		if digit < 0 {
			return raw, fmt.Errorf("platform: ulid %q: invalid character %q at %d", id, id[i], i)
		}
		low := 125 - 5*i
		for k := 0; k < 5; k++ {
			if digit&(1<<uint(k)) == 0 {
				continue
			}
			bit := low + k
			if bit < 0 || bit > 127 {
				return raw, fmt.Errorf("platform: ulid %q: value overflows 128 bits", id)
			}
			raw[15-bit/8] |= 1 << uint(bit%8)
		}
	}
	return raw, nil
}

// writeMillis stores ms big-endian in dst.
func writeMillis(dst []byte, ms uint64) {
	dst[0] = byte(ms >> 40)
	dst[1] = byte(ms >> 32)
	dst[2] = byte(ms >> 24)
	dst[3] = byte(ms >> 16)
	dst[4] = byte(ms >> 8)
	dst[5] = byte(ms)
}

// millisFromBytes reads the 48-bit big-endian millisecond prefix.
func millisFromBytes(raw [16]byte) int64 {
	var ms int64
	for _, b := range raw[:ulidTimeBytes] {
		ms = ms<<8 | int64(b)
	}
	return ms
}

// incrementEntropy treats entropy as a big-endian 80-bit counter and adds one.
// It reports false on overflow (all bits were 1).
func incrementEntropy(entropy *[ulidRandomBytes]byte) bool {
	for i := len(entropy) - 1; i >= 0; i-- {
		entropy[i]++
		if entropy[i] != 0 {
			return true
		}
	}
	return false
}
