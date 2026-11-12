package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/im"
	"github.com/Casper-Mars/echuan/internal/im/card"
	"github.com/Casper-Mars/echuan/internal/store"
)

// This file adapts the existing services to the interfaces the IM poller needs.
// Keeping the adapters here (instead of importing app from internal/im) means
// the IM package stays independent of the service layer and can be tested with
// plain fakes.

// imInbox maps InboxService onto im.InboxSource.
type imInbox struct {
	svc *InboxService
}

// NewIMInboxSource exposes the inbox service to the IM poller.
func NewIMInboxSource(svc *InboxService) im.InboxSource { return &imInbox{svc: svc} }

func (a *imInbox) Pending(ctx context.Context, limit int) ([]im.InboxEntry, error) {
	res, err := a.svc.Inbox(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]im.InboxEntry, 0, len(res.Items))
	for _, item := range res.Items {
		entry := im.InboxEntry{
			Kind:       item.Kind,
			ID:         item.ID,
			State:      item.State,
			Title:      item.Title,
			UpdateTime: item.UpdateTime,
			Age:        item.Age,
			Stale:      item.Stale,
		}
		if item.Reason != nil {
			entry.Reason = *item.Reason
		}
		out = append(out, entry)
	}
	return out, nil
}

// imSubjects resolves the metadata a card needs.
type imSubjects struct {
	projects *ProjectService
	items    *WorkItemService
}

// NewIMSubjectSource exposes project and work-item lookups to the poller.
func NewIMSubjectSource(projects *ProjectService, items *WorkItemService) im.SubjectSource {
	return &imSubjects{projects: projects, items: items}
}

func (a *imSubjects) ProjectGate(ctx context.Context, entry im.InboxEntry) (card.Project, error) {
	detail, err := a.projects.GetProject(ctx, entry.ID)
	if err != nil {
		return card.Project{}, err
	}
	p := card.Project{
		ProductID: detail.Project.ProductID,
		Version:   detail.Project.Version,
	}

	// The gate determines which material the reviewer is looking at.
	switch entry.State {
	case card.StateRequirementReview:
		if detail.Requirement != nil {
			p.MaterialID = detail.Requirement.MaterialID
			p.MaterialVersion = detail.Requirement.Version
			p.MaterialKind = "requirement"
		}
	case card.StateSpecReview:
		if detail.Spec != nil {
			p.MaterialID = detail.Spec.MaterialID
			p.MaterialVersion = detail.Spec.Version
			p.MaterialKind = "spec"
		}
	}
	return p, nil
}

func (a *imSubjects) WorkItemGate(ctx context.Context, entry im.InboxEntry) (card.WorkItem, error) {
	detail, err := a.items.GetWorkItem(ctx, entry.ID)
	if err != nil {
		return card.WorkItem{}, err
	}
	w := card.WorkItem{
		ProjectID:   detail.Item.ProjectID,
		Attempt:     detail.Item.Attempt,
		MaxAttempts: detail.Item.MaxAttempts,
	}
	if detail.Item.WorkerID != nil {
		w.Worker = *detail.Item.WorkerID
	}
	if detail.Result != nil {
		w.ReportID = detail.Result.MaterialID
	}
	return w, nil
}

// imCards persists card projections.
type imCards struct {
	st *store.Store
}

// NewIMActionStore exposes the same table to the card callback handler.
func NewIMActionStore(st *store.Store) im.ActionStore { return &imCards{st: st} }

// NewIMCardStore exposes the im_card table to the poller.
func NewIMCardStore(st *store.Store) im.CardStore { return &imCards{st: st} }

func (a *imCards) List(ctx context.Context) ([]im.CardRecord, error) {
	rows, err := store.ListIMCards(ctx, a.st.Read())
	if err != nil {
		return nil, err
	}
	out := make([]im.CardRecord, 0, len(rows))
	for _, row := range rows {
		rec := im.CardRecord{
			SubjectKind: row.SubjectKind,
			SubjectID:   row.SubjectID,
			ChatID:      row.ChatID,
			MessageID:   row.MessageID,
			Fingerprint: row.Fingerprint,
			Version:     row.Version,
			Closed:      row.ClosedAt != nil,
		}
		if rec.SentAt, err = time.Parse(time.RFC3339, row.SentAt); err != nil {
			return nil, fmt.Errorf("app: parse im_card sent_at %q: %w", row.SentAt, err)
		}
		if rec.RenderedAt, err = time.Parse(time.RFC3339, row.RenderedAt); err != nil {
			return nil, fmt.Errorf("app: parse im_card rendered_at %q: %w", row.RenderedAt, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func (a *imCards) Put(ctx context.Context, rec im.CardRecord) error {
	var closed *string
	if rec.Closed {
		at := formatTimestamp(rec.RenderedAt)
		closed = &at
	}
	return store.UpsertIMCard(ctx, a.st.Write(), store.IMCardRow{
		SubjectKind: rec.SubjectKind,
		SubjectID:   rec.SubjectID,
		ChatID:      rec.ChatID,
		MessageID:   rec.MessageID,
		Fingerprint: rec.Fingerprint,
		Version:     rec.Version,
		SentAt:      formatTimestamp(rec.SentAt),
		RenderedAt:  formatTimestamp(rec.RenderedAt),
		ClosedAt:    closed,
	})
}

func (a *imCards) Close(ctx context.Context, kind, id string, at time.Time) error {
	return store.CloseIMCard(ctx, a.st.Write(), kind, id, formatTimestamp(at))
}

// imDecider adapts the project and work-item services to im.Decider.
type imDecider struct {
	projects *ProjectService
	items    *WorkItemService
}

// NewIMDecider exposes the decision paths to the card callback handler.
func NewIMDecider(projects *ProjectService, items *WorkItemService) im.Decider {
	return &imDecider{projects: projects, items: items}
}

func (d *imDecider) ProjectState(ctx context.Context, projectID string) (im.ProjectState, error) {
	detail, err := d.projects.GetProject(ctx, projectID)
	if err != nil {
		return im.ProjectState{}, err
	}
	return im.ProjectState{Status: detail.Project.Status, Version: detail.Project.Version}, nil
}

func (d *imDecider) DecideProject(ctx context.Context, projectID, decision, reason, actor string, expectVersion int) error {
	version := expectVersion
	_, err := d.projects.Decide(ctx, DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.Decision(decision),
		Reason:        reason,
		ExpectVersion: &version,
		Actor:         actor,
	})
	return err
}

func (d *imDecider) ReviewPlan(ctx context.Context, projectID, decision, reason, actor string) error {
	_, err := d.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: projectID,
		Decision:  domain.Decision(decision),
		Reason:    reason,
		Actor:     actor,
	})
	return err
}

func (d *imDecider) DecideWorkItem(ctx context.Context, workID, decision, reason, actor string) error {
	_, err := d.items.DecideWorkItem(ctx, WorkItemDecisionInput{
		WorkID:   workID,
		Decision: decision,
		Reason:   reason,
		Actor:    actor,
	})
	return err
}

func (a *imCards) CardByMessage(ctx context.Context, messageID string) (im.CardRecord, error) {
	row, err := store.GetIMCardByMessageID(ctx, a.st.Read(), messageID)
	if err != nil {
		return im.CardRecord{}, err
	}
	rec := im.CardRecord{
		SubjectKind: row.SubjectKind,
		SubjectID:   row.SubjectID,
		ChatID:      row.ChatID,
		MessageID:   row.MessageID,
		Fingerprint: row.Fingerprint,
		Version:     row.Version,
		Closed:      row.ClosedAt != nil,
	}
	if rec.SentAt, err = time.Parse(time.RFC3339, row.SentAt); err != nil {
		return im.CardRecord{}, err
	}
	rec.RenderedAt = rec.SentAt
	return rec, nil
}

func (a *imCards) ActionResult(ctx context.Context, idemKey string) (string, bool, error) {
	result, err := store.GetIMActionResult(ctx, a.st.Read(), idemKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return result, true, nil
}

func (a *imCards) SaveAction(ctx context.Context, idemKey, subject, action, result string, at time.Time) error {
	return store.PutIMActionResult(ctx, a.st.Write(), idemKey, subject, action, result, formatTimestamp(at))
}
