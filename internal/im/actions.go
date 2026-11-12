package im

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/im/feishu"
)

// Decision buttons a card can send.
const (
	actionApprove = "approve"
	actionReject  = "reject"
)

// Project statuses that require a human decision. They mirror the three gates
// in the project state machine; the numbers are stable because they are
// persisted.
const (
	projectStatusRequirementReview = 1
	projectStatusSpecReview        = 3
	projectStatusTasksReview       = 5
)

// ProjectState is the minimum a decision needs from the project.
type ProjectState struct {
	Status  int
	Version int
}

// Decider performs the actual platform transitions. It is implemented by the
// app layer so the IM package never imports the service layer.
type Decider interface {
	ProjectState(ctx context.Context, projectID string) (ProjectState, error)
	DecideProject(ctx context.Context, projectID, decision, reason, actor string, expectVersion int) error
	ReviewPlan(ctx context.Context, projectID, decision, reason, actor string) error
	DecideWorkItem(ctx context.Context, workID, decision, reason, actor string) error
}

// ActionStore persists the pieces an inbound callback needs.
type ActionStore interface {
	CardByMessage(ctx context.Context, messageID string) (CardRecord, error)
	ActionResult(ctx context.Context, idemKey string) (string, bool, error)
	SaveAction(ctx context.Context, idemKey, subject, action, result string, at time.Time) error
}

// ActionHandler turns a card click into a platform decision.
type ActionHandler struct {
	cfg     Config
	cards   ActionStore
	decider Decider
	logger  *slog.Logger
	now     func() time.Time
}

// NewActionHandler builds the handler. now is injectable for tests.
func NewActionHandler(cfg Config, cards ActionStore, decider Decider, logger *slog.Logger, now func() time.Time) *ActionHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ActionHandler{cfg: cfg, cards: cards, decider: decider, logger: logger, now: now}
}

// Handle executes one card action. It never returns an error: every failure is
// turned into a toast the user can act on, because a callback that returns an
// error only shows a generic failure in the client.
func (h *ActionHandler) Handle(ctx context.Context, action feishu.CardAction) feishu.CardActionResult {
	if !h.allowed(action.ChatID) {
		h.logger.Warn("im card action from unknown chat", "chat", action.ChatID, "open_id", action.OpenID)
		return toast("warning", "无权操作这张卡片")
	}
	if action.Action != actionApprove && action.Action != actionReject {
		return toast("warning", "未知的操作")
	}

	rec, err := h.cards.CardByMessage(ctx, action.MessageID)
	if err != nil {
		h.logger.Warn("im card lookup failed", "message_id", action.MessageID, "error", err)
		return toast("warning", "该卡片已失效，请回 Web 查看")
	}

	reason := strings.TrimSpace(action.Form["reason"])
	actor := "human:feishu:" + action.OpenID
	// The first callback is the only place the app-scoped open_id shows up; the
	// decision it produces stores it in gate_decisions.decided_by.
	h.logger.Info("im card action", "subject", rec.SubjectID, "action", action.Action,
		"open_id", action.OpenID, "chat", action.ChatID)

	idemKey := fmt.Sprintf("%s|%s|%d", action.MessageID, action.Action, rec.Version)
	if previous, ok, err := h.cards.ActionResult(ctx, idemKey); err != nil {
		h.logger.Warn("im action lookup failed", "key", idemKey, "error", err)
	} else if ok {
		return toast("info", previous)
	}

	result, err := h.decide(ctx, rec, action.Action, reason, actor)
	if err != nil {
		return h.failureToast(err)
	}

	if err := h.cards.SaveAction(ctx, idemKey, rec.SubjectID, action.Action, result, h.now()); err != nil {
		h.logger.Warn("im action save failed", "key", idemKey, "error", err)
	}
	return toast("success", result)
}

// decide dispatches to the right transition for the card's subject.
func (h *ActionHandler) decide(ctx context.Context, rec CardRecord, action, reason, actor string) (string, error) {
	switch rec.SubjectKind {
	case "project":
		state, err := h.decider.ProjectState(ctx, rec.SubjectID)
		if err != nil {
			return "", err
		}
		if state.Version != rec.Version {
			return "", &domain.InvalidVersionError{
				Expected: rec.Version, Current: state.Version,
			}
		}

		switch state.Status {
		case projectStatusRequirementReview, projectStatusSpecReview:
			if err := h.decider.DecideProject(ctx, rec.SubjectID, action, reason, actor, rec.Version); err != nil {
				return "", err
			}
		case projectStatusTasksReview:
			if action == actionReject && reason == "" {
				return "", &domain.ValidationError{Fields: []domain.FieldError{
					{Field: "reason", Reason: "驳回必须填写理由"},
				}}
			}
			if err := h.decider.ReviewPlan(ctx, rec.SubjectID, action, reason, actor); err != nil {
				return "", err
			}
		default:
			return "", &domain.InvalidStateError{Reason: "该门禁已被处理"}
		}

	case "work_item":
		// A failed item can only be sent back to the queue: approve requires
		// the submitted state, so offering it would always fail.
		if action == actionApprove && rec.Closed {
			return "", &domain.InvalidStateError{Reason: "该任务已结束"}
		}
		if err := h.decider.DecideWorkItem(ctx, rec.SubjectID, action, reason, actor); err != nil {
			return "", err
		}

	default:
		return "", &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "subject", Reason: "unsupported card subject"},
		}}
	}

	if action == actionApprove {
		return "已通过", nil
	}
	return "已驳回", nil
}

// failureToast maps a domain error onto the wording the card promised.
func (h *ActionHandler) failureToast(err error) feishu.CardActionResult {
	var invalidVersion *domain.InvalidVersionError
	var invalidState *domain.InvalidStateError
	var validation *domain.ValidationError

	switch {
	case errors.As(err, &invalidVersion):
		return toast("warning", "内容已更新，请回 Web 查看")
	case errors.As(err, &invalidState):
		return toast("info", "该事项已处理，无需重复操作")
	case errors.As(err, &validation):
		return toast("warning", validationMessage(validation))
	default:
		h.logger.Error("im decision failed", "error", err)
		return toast("error", "平台暂时不可用，请稍后重试")
	}
}

func validationMessage(err *domain.ValidationError) string {
	for _, field := range err.Fields {
		if field.Field == "reason" {
			return field.Reason
		}
	}
	return "参数不合法"
}

func (h *ActionHandler) allowed(chatID string) bool {
	if chatID == "" {
		return false
	}
	for _, allowed := range h.cfg.AllowedChatIDs {
		if allowed == chatID {
			return true
		}
	}
	return false
}

func toast(kind, content string) feishu.CardActionResult {
	return feishu.CardActionResult{Toast: content, ToastType: kind}
}

// idemKeyFor is exported for tests that need to seed the action log.
func idemKeyFor(messageID, action string, version int) string {
	return messageID + "|" + action + "|" + strconv.Itoa(version)
}
