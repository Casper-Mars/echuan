package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// createProductRow inserts one product inside a transaction.
func createProductRow(t *testing.T, st *Store, p NewProduct) int64 {
	t.Helper()
	var id int64
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, err = CreateProduct(context.Background(), tx, p)
		return err
	}); err != nil {
		t.Fatalf("CreateProduct(%s): %v", p.ProductID, err)
	}
	return id
}

// TestProductCRUDAndUniqueViolation exercises the product write/read surface
// directly: duplicate business ids must surface as ErrUniqueViolation instead
// of a generic driver error, and every single-row writer must report a missing
// row as sql.ErrNoRows.
func TestProductCRUDAndUniqueViolation(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()

	var (
		description = "驿传平台"
		license     = "MIT"
	)
	id := createProductRow(t, st, NewProduct{
		ProductID:   "APP",
		Name:        "驿传",
		GitURL:      "git@example.com:APP.git",
		Description: &description,
		License:     &license,
		CreateTime:  "2026-01-01T00:00:00Z",
		UpdateTime:  "2026-01-01T00:00:00Z",
	})

	byID, err := GetProductByID(ctx, st.Read(), id)
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if byID.ProductID != "APP" || byID.Name != "驿传" || byID.GitURL != "git@example.com:APP.git" {
		t.Errorf("product = %+v", byID)
	}
	if byID.Description == nil || *byID.Description != description {
		t.Errorf("description = %v", byID.Description)
	}
	if byID.ProjectCount != 0 {
		t.Errorf("project_count = %d, want 0", byID.ProjectCount)
	}

	byCode, err := GetProductByProductID(ctx, st.Read(), "APP")
	if err != nil {
		t.Fatalf("GetProductByProductID: %v", err)
	}
	if byCode.ID != id {
		t.Errorf("id = %d, want %d", byCode.ID, id)
	}

	// A second product + pagination.
	createProductRow(t, st, NewProduct{
		ProductID: "BPP", Name: "B", GitURL: "git@example.com:BPP.git",
		CreateTime: "2026-01-02T00:00:00Z", UpdateTime: "2026-01-02T00:00:00Z",
	})
	createProductRow(t, st, NewProduct{
		ProductID: "CPP", Name: "C", GitURL: "git@example.com:CPP.git",
		CreateTime: "2026-01-03T00:00:00Z", UpdateTime: "2026-01-03T00:00:00Z",
	})

	firstPage, err := ListProducts(ctx, st.Read(), 0, 2)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].ProductID != "APP" || firstPage[1].ProductID != "BPP" {
		t.Errorf("first page = %+v, want APP, BPP", firstPage)
	}
	secondPage, err := ListProducts(ctx, st.Read(), firstPage[1].ID, 2)
	if err != nil {
		t.Fatalf("ListProducts page 2: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ProductID != "CPP" {
		t.Errorf("second page = %+v, want CPP", secondPage)
	}

	// Duplicate business id → typed unique violation.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := CreateProduct(ctx, tx, NewProduct{
			ProductID: "APP", Name: "dup", GitURL: "git", CreateTime: "t", UpdateTime: "t",
		})
		return err
	})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("duplicate CreateProduct error = %v, want ErrUniqueViolation", err)
	}

	// Full-row update, then a read-back.
	newName := "驿传 v2"
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateProduct(ctx, tx, id, ProductUpdate{
			Name: newName, GitURL: "git@example.com:APP2.git",
			Description: nil, License: &license, UpdateTime: "2026-02-01T00:00:00Z",
		})
	}); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	updated, err := GetProductByID(ctx, st.Read(), id)
	if err != nil {
		t.Fatalf("GetProductByID after update: %v", err)
	}
	if updated.Name != newName || updated.GitURL != "git@example.com:APP2.git" {
		t.Errorf("updated = %+v", updated)
	}
	if updated.Description != nil {
		t.Errorf("description = %v, want NULL", *updated.Description)
	}

	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateProduct(ctx, tx, 999, ProductUpdate{Name: "x", GitURL: "y", UpdateTime: "t"})
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateProduct(missing) = %v, want sql.ErrNoRows", err)
	}

	if _, err := CountProjectsByProduct(ctx, st.Read(), id); err != nil {
		t.Fatalf("CountProjectsByProduct: %v", err)
	}

	if err := st.Tx(ctx, func(tx *sql.Tx) error { return DeleteProduct(ctx, tx, id) }); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}
	if _, err := GetProductByID(ctx, st.Read(), id); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetProductByID(deleted) = %v, want sql.ErrNoRows", err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error { return DeleteProduct(ctx, tx, id) }); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteProduct(missing) = %v, want sql.ErrNoRows", err)
	}
}

// TestProjectCRUDAndTransitions covers the project writers the app layer uses:
// creation under both UNIQUE constraints, pointer moves, the conditional
// status/version UPDATE and the task counters.
func TestProjectCRUDAndTransitions(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	productID := createProductRow(t, st, NewProduct{
		ProductID: "APP", Name: "A", GitURL: "git", CreateTime: "t", UpdateTime: "t",
	})

	requirementID := insertMaterial(t, st, "m_req_1", 1, 1, nil)

	var projectID int64
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		projectID, err = CreateProject(ctx, tx, NewProject{
			ProjectID: "APP-1", ProductID: productID, SeqNo: 1, Title: "登录页改版",
			RequirementMaterialID: &requirementID, Status: 0, Version: 1,
			CreateTime: "2026-01-01T00:00:00Z", UpdateTime: "2026-01-01T00:00:00Z",
		})
		return err
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Duplicate business id.
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := CreateProject(ctx, tx, NewProject{
			ProjectID: "APP-1", ProductID: productID, SeqNo: 2, Title: "dup",
			Status: 0, Version: 1, CreateTime: "t", UpdateTime: "t",
		})
		return err
	})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("duplicate project_id error = %v, want ErrUniqueViolation", err)
	}

	// Duplicate (product_id, seq_no).
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := CreateProject(ctx, tx, NewProject{
			ProjectID: "APP-2", ProductID: productID, SeqNo: 1, Title: "dup seq",
			Status: 0, Version: 1, CreateTime: "t", UpdateTime: "t",
		})
		return err
	})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("duplicate (product_id, seq_no) error = %v, want ErrUniqueViolation", err)
	}

	byID, err := GetProjectByID(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if byID.ProjectID != "APP-1" || byID.ProductCode != "APP" || byID.SeqNo != 1 {
		t.Errorf("project = %+v", byID)
	}
	if byID.RequirementMaterialID == nil || *byID.RequirementMaterialID != requirementID {
		t.Errorf("requirement pointer = %v, want %d", byID.RequirementMaterialID, requirementID)
	}

	byCode, err := GetProjectByProjectID(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProjectByProjectID: %v", err)
	}
	if byCode.ID != projectID {
		t.Errorf("id = %d, want %d", byCode.ID, projectID)
	}

	// A second project of the same product, then list filters.
	var otherID int64
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		otherID, err = CreateProject(ctx, tx, NewProject{
			ProjectID: "APP-2", ProductID: productID, SeqNo: 2, Title: "二号",
			Status: 1, Version: 1, CreateTime: "t", UpdateTime: "t",
		})
		return err
	}); err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}
	createProductRow(t, st, NewProduct{
		ProductID: "BPP", Name: "B", GitURL: "git", CreateTime: "t", UpdateTime: "t",
	})

	all, err := ListProjects(ctx, st.Read(), ProjectFilter{ProductCode: "APP", Limit: 10})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("projects = %d, want 2", len(all))
	}
	status := 1
	review, err := ListProjects(ctx, st.Read(), ProjectFilter{ProductCode: "APP", Status: &status, Limit: 10})
	if err != nil {
		t.Fatalf("ListProjects(status): %v", err)
	}
	if len(review) != 1 || review[0].ProjectID != "APP-2" {
		t.Errorf("filtered = %+v, want APP-2", review)
	}
	page, err := ListProjects(ctx, st.Read(), ProjectFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects(page): %v", err)
	}
	if len(page) != 1 || page[0].ID != projectID {
		t.Errorf("first page = %+v", page)
	}

	// Title update bumps the version.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateProjectTitle(ctx, tx, projectID, "新标题", "2026-02-01T00:00:00Z")
	}); err != nil {
		t.Fatalf("UpdateProjectTitle: %v", err)
	}
	renamed, err := GetProjectByID(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID after rename: %v", err)
	}
	if renamed.Title != "新标题" || renamed.Version != 2 {
		t.Errorf("renamed = %+v, want title/version", renamed)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateProjectTitle(ctx, tx, 999, "x", "t")
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateProjectTitle(missing) = %v, want sql.ErrNoRows", err)
	}

	// Conditional transition: wrong version and wrong status match nothing.
	reason := "spec 缺少回滚方案"
	transition := func(expectStatus, expectVersion, next int) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = TransitionProject(ctx, tx, projectID,
				expectStatus, expectVersion, next, &reason, "alice", "2026-03-01T00:00:00Z")
			return err
		}); err != nil {
			t.Fatalf("TransitionProject: %v", err)
		}
		return affected
	}
	if got := transition(0, 1, 1); got != 0 {
		t.Errorf("stale version affected = %d, want 0", got)
	}
	if got := transition(0, 2, 1); got != 1 {
		t.Errorf("valid transition affected = %d, want 1", got)
	}
	moved, err := GetProjectByID(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID after transition: %v", err)
	}
	if moved.Status != 1 || moved.Version != 3 {
		t.Errorf("moved = %+v, want status 1 / version 3", moved)
	}
	if moved.Reason == nil || *moved.Reason != reason {
		t.Errorf("reason = %v", moved.Reason)
	}
	if moved.LastActor == nil || *moved.LastActor != "alice" {
		t.Errorf("last_actor = %v", moved.LastActor)
	}

	// Pointer moves.
	specID := insertMaterial(t, st, "m_spec_1", 2, 1, nil)
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return MoveSpecPointer(ctx, tx, projectID, specID, "cto", "2026-03-02T00:00:00Z")
	}); err != nil {
		t.Fatalf("MoveSpecPointer: %v", err)
	}
	secondReq := insertMaterial(t, st, "m_req_2", 1, 2, &requirementID)
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return MoveRequirementPointer(ctx, tx, projectID, secondReq, "2026-03-03T00:00:00Z")
	}); err != nil {
		t.Fatalf("MoveRequirementPointer: %v", err)
	}
	withSpec, err := GetProjectByID(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID after pointers: %v", err)
	}
	if withSpec.SpecMaterialID == nil || *withSpec.SpecMaterialID != specID {
		t.Errorf("spec pointer = %v, want %d", withSpec.SpecMaterialID, specID)
	}
	if withSpec.RequirementMaterialID == nil || *withSpec.RequirementMaterialID != secondReq {
		t.Errorf("requirement pointer = %v, want %d", withSpec.RequirementMaterialID, secondReq)
	}

	// Task counters and the status histogram.
	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 1},
		{taskID: "APP-1-T0002", seq: 2, status: 4},
		{taskID: "APP-1-T0003", seq: 3, status: 4},
		{taskID: "APP-1-T0004", seq: 4, status: 7},
	})
	if n, err := CountTasksByProject(ctx, st.Read(), projectID); err != nil || n != 4 {
		t.Errorf("CountTasksByProject = %d (%v), want 4", n, err)
	}
	histogram, err := TaskStatusHistogram(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("TaskStatusHistogram: %v", err)
	}
	if histogram[1] != 1 || histogram[4] != 2 || histogram[7] != 1 || len(histogram) != 3 {
		t.Errorf("histogram = %v, want {1:1,4:2,7:1}", histogram)
	}
	withTasks, err := GetProjectByID(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("GetProjectByID with tasks: %v", err)
	}
	if withTasks.TaskTotal != 4 || withTasks.TaskDone != 2 {
		t.Errorf("task counters = %d/%d, want 4/2", withTasks.TaskTotal, withTasks.TaskDone)
	}

	// Delete removes exactly one project row (otherID has no tasks, so the
	// tasks.project_id foreign key does not block it).
	if err := st.Tx(ctx, func(tx *sql.Tx) error { return DeleteProject(ctx, tx, otherID) }); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := GetProjectByID(ctx, st.Read(), otherID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetProjectByID(deleted) = %v, want sql.ErrNoRows", err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error { return DeleteProject(ctx, tx, otherID) }); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteProject(missing) = %v, want sql.ErrNoRows", err)
	}
	if _, err := GetProjectByID(ctx, st.Read(), projectID); err != nil {
		t.Errorf("the first project was deleted too: %v", err)
	}
}
