package app

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/store"
)

// Test helpers shared by every app test. They deliberately write raw SQL so a
// test can put a row into a state the public API would not allow.

// setProjectState forces a project into a given status/version, optionally with
// a pre-existing reason.
func (e *projectTestEnv) setProjectState(t *testing.T, projectID string, status, version int, reason *string) {
	t.Helper()
	if _, err := e.store.Write().ExecContext(context.Background(), `
		UPDATE projects SET status = ?, version = ?, reason = ? WHERE project_id = ?`,
		status, version, reason, projectID); err != nil {
		t.Fatalf("set project state: %v", err)
	}
}

type projectState struct {
	Status    int
	Version   int
	Reason    *string
	LastActor *string
}

func (e *projectTestEnv) projectState(t *testing.T, projectID string) projectState {
	t.Helper()
	var state projectState
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT status, version, reason, last_actor FROM projects WHERE project_id = ?`,
		projectID).Scan(&state.Status, &state.Version, &state.Reason, &state.LastActor); err != nil {
		t.Fatalf("read project state: %v", err)
	}
	return state
}

// createProject creates one requirement-draft project and returns its id.
func (e *projectTestEnv) createProject(t *testing.T, productID, title string) string {
	t.Helper()
	detail, err := e.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     productID,
		Title:         title,
		RequirementMD: "# 需求\n\n正文\n",
		UploadedBy:    "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", title, err)
	}
	return detail.Project.ProjectID
}

// insertWorkItem inserts one card with an explicit state/version and returns
// its physical id.
func (e *projectTestEnv) insertWorkItem(t *testing.T, projectID, workID string, seq int, state string, version int) int64 {
	t.Helper()
	physical := e.physicalProjectID(t, projectID)
	res, err := e.store.Write().ExecContext(context.Background(), `
		INSERT INTO work_items (work_id, project_id, seq_no, kind, title, body_md, state, attempt,
		                        max_attempts, version, create_time, update_time)
		VALUES (?, ?, ?, 'dev', ?, '正文', ?, 0, 3, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		workID, physical, seq, "任务 "+workID, state, version)
	if err != nil {
		t.Fatalf("insert work item %s: %v", workID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("insert work item id: %v", err)
	}
	return id
}

// workItemStates reads the state of every card of one project, keyed by id.
func (e *projectTestEnv) workItemStates(t *testing.T, projectID string) map[string]string {
	t.Helper()
	rows, err := e.store.Read().QueryContext(context.Background(), `
		SELECT w.work_id, w.state FROM work_items w
		  JOIN projects p ON p.id = w.project_id
		 WHERE p.project_id = ?`, projectID)
	if err != nil {
		t.Fatalf("read work item states: %v", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			t.Fatalf("scan work item state: %v", err)
		}
		out[id] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate work item states: %v", err)
	}
	return out
}

// seedBlockedWorkItem inserts a blocked dev card owned by projectID.
func (e *projectTestEnv) seedBlockedWorkItem(t *testing.T, projectID, workID string, seq int) int64 {
	t.Helper()
	return e.insertWorkItem(t, projectID, workID, seq, store.WorkItemStateBlocked, 1)
}
