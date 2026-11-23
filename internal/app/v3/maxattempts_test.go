package v3

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// TestCreateTaskUsesConfiguredMaxAttempts pins the config → task wiring: an
// omitted max_attempts becomes the configured value, an explicit one wins, and
// an unwired service keeps the historical default of 3. The configured value is
// 7 on purpose so a regression that hard-codes 3 cannot pass.
func TestCreateTaskUsesConfiguredMaxAttempts(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaxAttempts(7)
	projectID := seedProject(t, st, "APP")

	cases := []struct {
		name     string
		taskID   string
		explicit int
		want     int
	}{
		{name: "omitted uses config", taskID: "t_cfg", explicit: 0, want: 7},
		{name: "explicit wins", taskID: "t_explicit", explicit: 1, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := svc.CreateTask(context.Background(), CreateTaskRequest{
				ProjectID: projectID, TaskID: tc.taskID, Kind: enum.Dev, Title: tc.taskID,
				MaxAttempts: tc.explicit,
			})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			if created.MaxAttempts != tc.want {
				t.Errorf("created.MaxAttempts = %d, want %d", created.MaxAttempts, tc.want)
			}
			// GET must report the persisted value, not the request default.
			got, err := svc.GetTask(context.Background(), tc.taskID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if got.MaxAttempts != tc.want {
				t.Errorf("GetTask MaxAttempts = %d, want %d", got.MaxAttempts, tc.want)
			}
		})
	}
}

// TestCreateTaskUnconfiguredDefaultsToThree keeps "no config still 3" true for
// a service that was never wired with a limit.
func TestCreateTaskUnconfiguredDefaultsToThree(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")

	created, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_default", Kind: enum.Dev, Title: "默认",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3 without config", created.MaxAttempts)
	}
}

// TestWithMaxAttemptsIgnoresNonPositive makes sure a bad wiring value cannot
// turn the budget into 0 (which the store would silently rewrite) or negative.
func TestWithMaxAttemptsIgnoresNonPositive(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaxAttempts(0).WithMaxAttempts(-5)
	projectID := seedProject(t, st, "APP")

	created, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_nonpos", Kind: enum.Dev, Title: "非正数",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.MaxAttempts != defaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want %d", created.MaxAttempts, defaultMaxAttempts)
	}
}
