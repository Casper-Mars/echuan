package im

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/im/feishu"
)

type fakeActionStore struct {
	card    CardRecord
	cardErr error
	results map[string]string
	saved   []string
}

func newFakeActionStore() *fakeActionStore {
	return &fakeActionStore{results: map[string]string{}}
}

func (f *fakeActionStore) CardByMessage(context.Context, string) (CardRecord, error) {
	return f.card, f.cardErr
}

func (f *fakeActionStore) ActionResult(_ context.Context, key string) (string, bool, error) {
	v, ok := f.results[key]
	return v, ok, nil
}

func (f *fakeActionStore) SaveAction(_ context.Context, key, _, _, result string, _ time.Time) error {
	f.results[key] = result
	f.saved = append(f.saved, key)
	return nil
}

type deciderCall struct {
	kind   string
	id     string
	action string
	reason string
	actor  string
	expect int
}

type fakeDecider struct {
	state  ProjectState
	err    error
	states int
	calls  []deciderCall
}

func (f *fakeDecider) ProjectState(context.Context, string) (ProjectState, error) {
	f.states++
	return f.state, f.err
}

func (f *fakeDecider) DecideProject(_ context.Context, id, decision, reason, actor string, expect int) error {
	f.calls = append(f.calls, deciderCall{"project", id, decision, reason, actor, expect})
	return f.err
}

func (f *fakeDecider) ReviewPlan(_ context.Context, id, decision, reason, actor string) error {
	f.calls = append(f.calls, deciderCall{"plan", id, decision, reason, actor, 0})
	return f.err
}

func (f *fakeDecider) DecideWorkItem(_ context.Context, id, decision, reason, actor string) error {
	f.calls = append(f.calls, deciderCall{"item", id, decision, reason, actor, 0})
	return f.err
}

func newTestActionHandler(store ActionStore, decider Decider) *ActionHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewActionHandler(Config{AllowedChatIDs: []string{"oc_allowed"}}, store, decider, logger,
		func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) })
}

func projectCard(status int) *fakeActionStore {
	store := newFakeActionStore()
	store.card = CardRecord{SubjectKind: "project", SubjectID: "APP-1", ChatID: "oc_allowed", MessageID: "om_1", Version: 3}
	return store
}

func TestActionRejectsUnknownChat(t *testing.T) {
	store := projectCard(1)
	decider := &fakeDecider{}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{ChatID: "oc_other", MessageID: "om_1", Action: "approve"})
	if res.ToastType != "warning" || !strings.Contains(res.Toast, "无权") {
		t.Fatalf("toast = %+v, want a warning about permission", res)
	}
	if len(decider.calls) != 0 {
		t.Error("decider must not be called for an unknown chat")
	}
}

func TestActionApprovesRequirementGate(t *testing.T) {
	store := projectCard(1)
	decider := &fakeDecider{state: ProjectState{Status: 1, Version: 3}}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{
		ChatID: "oc_allowed", MessageID: "om_1", Action: "approve", OpenID: "ou_1",
		Form: map[string]string{"reason": ""},
	})
	if res.ToastType != "success" || !strings.Contains(res.Toast, "已通过") {
		t.Fatalf("toast = %+v, want success", res)
	}
	if len(decider.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(decider.calls))
	}
	call := decider.calls[0]
	if call.id != "APP-1" || call.action != "approve" || call.expect != 3 {
		t.Errorf("call = %+v", call)
	}
	if call.actor != "human:feishu:ou_1" {
		t.Errorf("actor = %q", call.actor)
	}
	if len(store.saved) != 1 {
		t.Errorf("saved = %d, want 1 idempotency record", len(store.saved))
	}
}

func TestActionRejectsStaleVersion(t *testing.T) {
	store := projectCard(1)
	decider := &fakeDecider{state: ProjectState{Status: 1, Version: 4}}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{ChatID: "oc_allowed", MessageID: "om_1", Action: "approve"})
	if res.ToastType != "warning" || !strings.Contains(res.Toast, "已更新") {
		t.Fatalf("toast = %+v, want stale warning", res)
	}
	if len(decider.calls) != 0 {
		t.Error("decider must not be called on a version mismatch")
	}
}

func TestActionTasksReviewRequiresReasonOnReject(t *testing.T) {
	store := projectCard(5)
	decider := &fakeDecider{state: ProjectState{Status: 5, Version: 3}}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{ChatID: "oc_allowed", MessageID: "om_1", Action: "reject"})
	if res.ToastType != "warning" || !strings.Contains(res.Toast, "理由") {
		t.Fatalf("toast = %+v, want reason warning", res)
	}
	if len(decider.calls) != 0 {
		t.Error("ReviewPlan must not be called without a reason")
	}
}

func TestActionRoutesPlanReview(t *testing.T) {
	store := projectCard(5)
	decider := &fakeDecider{state: ProjectState{Status: 5, Version: 3}}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{
		ChatID: "oc_allowed", MessageID: "om_1", Action: "reject",
		Form: map[string]string{"reason": "拆分粒度过粗"},
	})
	if res.ToastType != "success" {
		t.Fatalf("toast = %+v", res)
	}
	if len(decider.calls) != 1 || decider.calls[0].kind != "plan" {
		t.Fatalf("calls = %+v, want one plan review", decider.calls)
	}
}

func TestActionRoutesWorkItem(t *testing.T) {
	store := newFakeActionStore()
	store.card = CardRecord{SubjectKind: "work_item", SubjectID: "APP-1-T0007", ChatID: "oc_allowed", MessageID: "om_2"}
	decider := &fakeDecider{}
	h := newTestActionHandler(store, decider)

	res := h.Handle(context.Background(), feishu.CardAction{ChatID: "oc_allowed", MessageID: "om_2", Action: "approve"})
	if res.ToastType != "success" {
		t.Fatalf("toast = %+v", res)
	}
	if len(decider.calls) != 1 || decider.calls[0].kind != "item" {
		t.Fatalf("calls = %+v", decider.calls)
	}
}

func TestActionIsIdempotent(t *testing.T) {
	store := projectCard(1)
	decider := &fakeDecider{state: ProjectState{Status: 1, Version: 3}}
	h := newTestActionHandler(store, decider)

	act := feishu.CardAction{ChatID: "oc_allowed", MessageID: "om_1", Action: "approve"}
	first := h.Handle(context.Background(), act)
	second := h.Handle(context.Background(), act)

	if first.Toast != second.Toast {
		t.Errorf("second toast = %q, want %q", second.Toast, first.Toast)
	}
	if len(decider.calls) != 1 {
		t.Errorf("decider calls = %d, want 1 (idempotent replay)", len(decider.calls))
	}
}

func TestActionMapsDomainErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantSub string
	}{
		{"invalid state", &domain.InvalidStateError{Reason: "already decided"}, "已处理"},
		{"validation", &domain.ValidationError{Fields: []domain.FieldError{{Field: "reason", Reason: "驳回必须填写理由"}}}, "理由"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := projectCard(1)
			decider := &fakeDecider{state: ProjectState{Status: 1, Version: 3}, err: tc.err}
			h := newTestActionHandler(store, decider)

			res := h.Handle(context.Background(), feishu.CardAction{ChatID: "oc_allowed", MessageID: "om_1", Action: "approve"})
			if !strings.Contains(res.Toast, tc.wantSub) {
				t.Errorf("toast = %q, want it to contain %q", res.Toast, tc.wantSub)
			}
		})
	}
}
