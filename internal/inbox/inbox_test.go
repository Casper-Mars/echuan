package inbox

import (
	"strings"
	"testing"
	"time"
)

// fixedNow is the clock reading every derivation test uses.
var fixedNow = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// staleAfter is the default 72h threshold expressed as a duration.
const staleAfter = 72 * time.Hour

func TestDeriveEmpty(t *testing.T) {
	entries, err := Derive(nil, fixedNow, staleAfter)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if entries == nil {
		t.Fatal("entries = nil, want an empty non-nil slice")
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestDeriveAgeFloorsToWholeSeconds(t *testing.T) {
	cases := []struct {
		name       string
		updateTime string
		wantAge    int64
	}{
		{name: "exact", updateTime: "2026-10-27T08:00:00Z", wantAge: 3600},
		{name: "floor below one second", updateTime: "2026-10-27T08:59:59.999Z", wantAge: 0},
		{name: "floor of fractional seconds", updateTime: "2026-10-27T08:59:58.750Z", wantAge: 1},
		{name: "fifty milliseconds", updateTime: "2026-10-27T09:00:00.050Z", wantAge: 0},
		{name: "future clamps to zero", updateTime: "2026-10-27T10:00:00Z", wantAge: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := Derive([]Item{{
				Kind:       KindProject,
				ID:         "APP-1",
				State:      1,
				UpdateTime: tc.updateTime,
			}}, fixedNow, staleAfter)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if got := entries[0].AgeSeconds(); got != tc.wantAge {
				t.Fatalf("age_seconds = %d, want %d", got, tc.wantAge)
			}
			if entries[0].Age < 0 {
				t.Fatalf("Age = %s, want non-negative", entries[0].Age)
			}
		})
	}
}

func TestDeriveStaleBoundary(t *testing.T) {
	cases := []struct {
		name      string
		update    time.Time
		wantStale bool
	}{
		{name: "71h", update: fixedNow.Add(-71 * time.Hour), wantStale: false},
		{name: "just under 72h", update: fixedNow.Add(-72*time.Hour + time.Second), wantStale: false},
		{name: "exactly 72h", update: fixedNow.Add(-72 * time.Hour), wantStale: true},
		{name: "73h", update: fixedNow.Add(-73 * time.Hour), wantStale: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := Derive([]Item{{
				Kind:       KindProject,
				ID:         "APP-1",
				State:      1,
				UpdateTime: tc.update.Format(time.RFC3339Nano),
			}}, fixedNow, staleAfter)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if entries[0].Stale != tc.wantStale {
				t.Fatalf("stale = %v, want %v (age %s)", entries[0].Stale, tc.wantStale, entries[0].Age)
			}
		})
	}
}

func TestDeriveStateNamesFollowKind(t *testing.T) {
	items := []Item{
		{Kind: KindProject, ID: "APP-1", State: 1, UpdateTime: "2026-10-27T08:00:00Z"},
		{Kind: KindProject, ID: "APP-2", State: 3, UpdateTime: "2026-10-27T08:00:00Z"},
		{Kind: KindProject, ID: "APP-3", State: 5, UpdateTime: "2026-10-27T08:00:00Z"},
		{Kind: KindTask, ID: "APP-1-T0001", State: 3, UpdateTime: "2026-10-27T08:00:00Z"},
		{Kind: KindTask, ID: "APP-1-T0002", State: 5, UpdateTime: "2026-10-27T08:00:00Z"},
		{Kind: "unknown", ID: "X-1", State: 1, UpdateTime: "2026-10-27T08:00:00Z"},
	}
	want := []string{"spec_review", "plan_review", "delivery_review", "pending_acceptance", "failed", ""}

	entries, err := Derive(items, fixedNow, staleAfter)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for i, entry := range entries {
		if entry.StateName != want[i] {
			t.Errorf("entry %d (%s/%d) state_name = %q, want %q", i, entry.Kind, entry.State, entry.StateName, want[i])
		}
	}
}

func TestDerivePreservesOrderAndFields(t *testing.T) {
	reason := "attempt 用尽"
	items := []Item{
		{Kind: KindProject, ID: "APP-1", State: 1, Title: "订单导出", UpdateTime: "2026-10-24T09:00:00Z"},
		{Kind: KindTask, ID: "APP-1-T0003", State: 5, Title: "补并发测试", UpdateTime: "2026-10-26T09:00:00Z", Reason: &reason},
		{Kind: KindProject, ID: "APP-2", State: 3, Title: "对账", UpdateTime: "2026-10-26T10:00:00Z"},
	}

	entries, err := Derive(items, fixedNow, staleAfter)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// Input order is preserved: sorting belongs to the SQL statement.
	if entries[0].ID != "APP-1" || entries[1].ID != "APP-1-T0003" || entries[2].ID != "APP-2" {
		t.Fatalf("order = %s, %s, %s", entries[0].ID, entries[1].ID, entries[2].ID)
	}

	first := entries[0]
	if first.Title != "订单导出" || first.State != 1 || first.StateName != "spec_review" {
		t.Errorf("first = %+v", first)
	}
	if first.UpdateTime != "2026-10-24T09:00:00Z" {
		t.Errorf("update_time = %q, want the stored value verbatim", first.UpdateTime)
	}
	if first.Reason != nil {
		t.Errorf("reason = %v, want nil", first.Reason)
	}
	if first.Age != 72*time.Hour || !first.Stale {
		t.Errorf("age/stale = %s/%v, want 72h/true", first.Age, first.Stale)
	}

	second := entries[1]
	if second.Reason == nil || *second.Reason != reason {
		t.Errorf("reason = %v, want %q", second.Reason, reason)
	}
	if second.Stale {
		t.Error("24h-old task must not be stale at 72h")
	}
}

// TestDeriveKeepsFractionalUpdateTime guards the contract detail that the API
// echoes the stored text: a .500Z value must not be re-rendered as .5Z.
func TestDeriveKeepsFractionalUpdateTime(t *testing.T) {
	const stored = "2026-10-27T08:59:59.500Z"
	entries, err := Derive([]Item{{
		Kind: KindTask, ID: "APP-1-T0001", State: 3, UpdateTime: stored,
	}}, fixedNow, staleAfter)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if entries[0].UpdateTime != stored {
		t.Fatalf("update_time = %q, want %q", entries[0].UpdateTime, stored)
	}
	if entries[0].AgeSeconds() != 0 {
		t.Fatalf("age_seconds = %d, want 0", entries[0].AgeSeconds())
	}
}

func TestDeriveRejectsUnparsableUpdateTime(t *testing.T) {
	_, err := Derive([]Item{{
		Kind: KindProject, ID: "APP-1", State: 1, UpdateTime: "not-a-timestamp",
	}}, fixedNow, staleAfter)
	if err == nil {
		t.Fatal("Derive accepted an unparsable update_time")
	}
	if !strings.Contains(err.Error(), "APP-1") {
		t.Errorf("error = %v, want it to name the offending item", err)
	}
}

// TestDeriveUsesInjectedThreshold proves the threshold is not hardcoded: the
// same 2h-old item is fresh at 72h and stale at 1h.
func TestDeriveUsesInjectedThreshold(t *testing.T) {
	item := Item{Kind: KindProject, ID: "APP-1", State: 1, UpdateTime: "2026-10-27T07:00:00Z"}

	loose, err := Derive([]Item{item}, fixedNow, 72*time.Hour)
	if err != nil {
		t.Fatalf("Derive 72h: %v", err)
	}
	if loose[0].Stale {
		t.Error("2h-old item marked stale at 72h")
	}

	tight, err := Derive([]Item{item}, fixedNow, 1*time.Hour)
	if err != nil {
		t.Fatalf("Derive 1h: %v", err)
	}
	if !tight[0].Stale {
		t.Error("2h-old item not stale at 1h")
	}
}
