package im

import (
	"context"
	"errors"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// V3Decider applies a card decision to a V3 task.
type V3Decider struct {
	tasks *appv3.Service
}

// NewV3Decider builds the decider.
func NewV3Decider(tasks *appv3.Service) *V3Decider { return &V3Decider{tasks: tasks} }

// ProjectState is not used by V3: there are no project-level gates.
func (d *V3Decider) ProjectState(context.Context, string) (ProjectState, error) {
	return ProjectState{}, errors.New("im: project gates were removed in V3")
}

// DecideProject is not used by V3.
func (d *V3Decider) DecideProject(context.Context, string, string, string, string, int) error {
	return errors.New("im: project gates were removed in V3")
}

// ReviewPlan is not used by V3: breakdown approval is a task decision.
func (d *V3Decider) ReviewPlan(context.Context, string, string, string, string) error {
	return errors.New("im: plan review was removed in V3")
}

// DecideWorkItem approves or rejects one task. The reviewed artifact is the
// task's latest output material.
func (d *V3Decider) DecideWorkItem(ctx context.Context, taskID, decision, reason, actor string) error {
	materialID, err := d.tasks.LatestOutputMaterialID(ctx, taskID)
	if err != nil {
		return err
	}
	verdict := enum.Approve
	if decision == actionReject {
		verdict = enum.Reject
		if reason == "" {
			reason = "IM 驳回（未填理由）"
		}
	}
	_, err = d.tasks.Decide(ctx, appv3.DecideRequest{
		TaskID: taskID, Decision: verdict, MaterialID: materialID,
		DecidedBy: actor, Reason: reason,
	})
	return err
}
