package domain

import (
	"errors"
	"fmt"
	"sort"
	"testing"
)

// taskEdges is the expected legal matrix, written out independently of
// taskTransitions.
var taskEdges = [][2]int{
	{TaskBacklog, TaskReady},
	{TaskReady, TaskInProgress},
	{TaskInProgress, TaskPendingAcceptance},
	{TaskInProgress, TaskReady},
	{TaskPendingAcceptance, TaskDone},
	{TaskPendingAcceptance, TaskReady},
	{TaskBacklog, TaskBlocked},
	{TaskReady, TaskBlocked},
	{TaskInProgress, TaskBlocked},
	{TaskPendingAcceptance, TaskBlocked},
	{TaskFailed, TaskBlocked},
	{TaskBlocked, TaskReady},
	{TaskInProgress, TaskFailed},
	{TaskFailed, TaskReady},
	{TaskFailed, TaskCancelled},
}

func TestTaskTransitionMatrix(t *testing.T) {
	legal := make(map[string]bool, len(taskEdges))
	for _, edge := range taskEdges {
		legal[fmt.Sprintf("%d-%d", edge[0], edge[1])] = true
	}

	for from := TaskBacklog; from <= TaskCancelled; from++ {
		for to := TaskBacklog; to <= TaskCancelled; to++ {
			key := fmt.Sprintf("%d-%d", from, to)
			err := ValidateTaskTransition(from, to)
			if legal[key] {
				if err != nil {
					t.Errorf("%s: error = %v, want nil", key, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("%s: accepted an illegal transition", key)
				continue
			}
			if !errors.Is(err, ErrInvalidState) {
				t.Errorf("%s: error = %v, want ErrInvalidState", key, err)
			}
		}
	}
}

func TestTaskTerminalStatuses(t *testing.T) {
	for _, status := range []int{TaskDone, TaskCancelled} {
		if !IsTerminalTaskStatus(status) {
			t.Errorf("IsTerminalTaskStatus(%d) = false", status)
		}
		if targets := TaskTargets(status); len(targets) != 0 {
			t.Errorf("TaskTargets(%d) = %v, want none", status, targets)
		}
	}
	for _, status := range []int{TaskBacklog, TaskReady, TaskInProgress, TaskPendingAcceptance, TaskFailed, TaskBlocked} {
		if IsTerminalTaskStatus(status) {
			t.Errorf("IsTerminalTaskStatus(%d) = true", status)
		}
	}
}

func TestTaskTargets(t *testing.T) {
	want := map[int][]int{
		TaskBacklog:           {TaskReady, TaskBlocked},
		TaskReady:             {TaskInProgress, TaskBlocked},
		TaskInProgress:        {TaskPendingAcceptance, TaskReady, TaskBlocked, TaskFailed},
		TaskPendingAcceptance: {TaskDone, TaskReady, TaskBlocked},
		TaskFailed:            {TaskBlocked, TaskReady, TaskCancelled},
		TaskBlocked:           {TaskReady},
	}
	for from, expected := range want {
		got := TaskTargets(from)
		sort.Ints(got)
		wantSorted := append([]int(nil), expected...)
		sort.Ints(wantSorted)
		if fmt.Sprint(got) != fmt.Sprint(wantSorted) {
			t.Errorf("TaskTargets(%d) = %v, want %v", from, got, wantSorted)
		}
	}
}

func TestTaskStatusName(t *testing.T) {
	want := []string{"backlog", "ready", "in_progress", "pending_acceptance", "done", "failed", "blocked", "cancelled"}
	for status, name := range want {
		if got := TaskStatusName(status); got != name {
			t.Errorf("TaskStatusName(%d) = %q, want %q", status, got, name)
		}
	}
	if got := TaskStatusName(99); got != "unknown" {
		t.Errorf("TaskStatusName(99) = %q, want unknown", got)
	}
	if TaskStatusDone != TaskDone {
		t.Errorf("TaskStatusDone = %d, want %d", TaskStatusDone, TaskDone)
	}
}
