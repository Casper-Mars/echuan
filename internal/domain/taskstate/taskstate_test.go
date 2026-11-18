package taskstate

import (
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// TestTransitionTable is the exhaustive 7×7 contract: exactly the documented
// edges are legal, everything else is rejected.
func TestTransitionTable(t *testing.T) {
	allowed := map[enum.TaskStatus]map[enum.TaskStatus]bool{
		enum.Pending: {
			enum.Ready:     true,
			enum.Failed:    true,
			enum.Cancelled: true,
		},
		enum.Ready: {
			enum.InProgress: true,
			enum.Failed:     true,
			enum.Cancelled:  true,
		},
		enum.InProgress: {
			enum.InReview:  true,
			enum.Failed:    true,
			enum.Cancelled: true,
		},
		enum.InReview: {
			enum.Done:      true,
			enum.Ready:     true,
			enum.Failed:    true,
			enum.Cancelled: true,
		},
		enum.Failed: {
			enum.Ready:     true,
			enum.Pending:   true,
			enum.Cancelled: true,
		},
	}

	statuses := enum.TaskStatuses()
	if len(statuses) != 7 {
		t.Fatalf("statuses = %d, want 7", len(statuses))
	}

	for _, from := range statuses {
		for _, to := range statuses {
			want := allowed[from][to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
			err := Validate(from, to)
			if want && err != nil {
				t.Errorf("Validate(%s, %s) = %v, want nil", from, to, err)
			}
			if !want && err == nil {
				t.Errorf("Validate(%s, %s) = nil, want error", from, to)
			}
		}
	}
}

func TestInitialStatus(t *testing.T) {
	if got := Initial(false); got != enum.Ready {
		t.Errorf("Initial(no unmet) = %s, want ready", got)
	}
	if got := Initial(true); got != enum.Pending {
		t.Errorf("Initial(unmet) = %s, want pending", got)
	}
}

func TestTransitionTargets(t *testing.T) {
	cases := []struct {
		name string
		got  enum.TaskStatus
		want enum.TaskStatus
	}{
		{"claim", AfterClaim(), enum.InProgress},
		{"report", AfterReport(), enum.InReview},
		{"approve", AfterApprove(), enum.Done},
		{"reject", AfterReject(), enum.Ready},
		{"fail", AfterFail(), enum.Failed},
		{"cancel", AfterCancel(), enum.Cancelled},
		{"reset with deps", AfterReset(true), enum.Pending},
		{"reset without deps", AfterReset(false), enum.Ready},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

// TestRejectedTaskIsImmediatelyClaimable pins the reason reject goes to ready:
// the next worker can claim the rework without an operator touch.
func TestRejectedTaskIsImmediatelyClaimable(t *testing.T) {
	afterReject := AfterReject()
	if afterReject != enum.Ready {
		t.Fatalf("after reject = %s, want ready", afterReject)
	}
	if !CanTransition(afterReject, enum.InProgress) {
		t.Fatal("ready → in_progress must be legal")
	}
	if IsTerminal(afterReject) {
		t.Fatal("ready must not be terminal")
	}
}

func TestActiveAndTerminal(t *testing.T) {
	active := []enum.TaskStatus{enum.Pending, enum.Ready, enum.InProgress, enum.InReview}
	terminal := []enum.TaskStatus{enum.Done, enum.Cancelled}

	for _, status := range active {
		if !IsActive(status) {
			t.Errorf("%s should be active", status)
		}
		if IsTerminal(status) {
			t.Errorf("%s should not be terminal", status)
		}
	}
	for _, status := range terminal {
		if IsTerminal(status) {
			continue
		}
		t.Errorf("%s should be terminal", status)
	}
	if !IsTerminal(enum.Done) || !IsTerminal(enum.Cancelled) {
		t.Error("done/cancelled must be terminal")
	}
	if IsActive(enum.Done) || IsActive(enum.Cancelled) {
		t.Error("terminal states must not be active")
	}
	// failed is neither active nor terminal: it can be reset.
	if IsActive(enum.Failed) || IsTerminal(enum.Failed) {
		t.Error("failed must be neither active nor terminal")
	}
}

func TestTerminalStatesHaveNoExit(t *testing.T) {
	for _, status := range []enum.TaskStatus{enum.Done, enum.Cancelled} {
		for _, to := range enum.TaskStatuses() {
			if CanTransition(status, to) {
				t.Errorf("%s → %s must be illegal", status, to)
			}
		}
	}
}

func TestLabels(t *testing.T) {
	cases := []struct {
		kind   enum.TaskKind
		ready  string
		active string
		review string
	}{
		{enum.Requirement, "待处理", "处理中", "待审核"},
		{enum.Design, "待设计", "设计中", "待评审"},
		{enum.Breakdown, "待拆分", "拆分中", "待评审"},
		{enum.Dev, "待开发", "开发中", "待验收"},
	}
	for _, tc := range cases {
		if got := Label(tc.kind, enum.Ready); got != tc.ready {
			t.Errorf("%s ready label = %q, want %q", tc.kind, got, tc.ready)
		}
		if got := Label(tc.kind, enum.InProgress); got != tc.active {
			t.Errorf("%s in_progress label = %q, want %q", tc.kind, got, tc.active)
		}
		if got := Label(tc.kind, enum.InReview); got != tc.review {
			t.Errorf("%s in_review label = %q, want %q", tc.kind, got, tc.review)
		}
		if got := Label(tc.kind, enum.Pending); got != "等待前置" {
			t.Errorf("%s pending label = %q, want 等待前置", tc.kind, got)
		}
		if got := Label(tc.kind, enum.Done); got != "已完成" {
			t.Errorf("%s done label = %q, want 已完成", tc.kind, got)
		}
	}

	// Unknown pairs fall back to the status name instead of an empty chip.
	if got := Label(enum.TaskKind(99), enum.Ready); got != "ready" {
		t.Errorf("unknown kind label = %q, want ready", got)
	}
	if got := Label(enum.Dev, enum.TaskStatus(99)); got != "invalid(99)" {
		t.Errorf("unknown status label = %q, want invalid(99)", got)
	}
}

func TestLabelsReturnsCopy(t *testing.T) {
	first := Labels(enum.Dev)
	first[enum.Ready] = "被改坏了"
	if got := Label(enum.Dev, enum.Ready); got != "待开发" {
		t.Fatalf("Labels leaked its map: label = %q", got)
	}
}
