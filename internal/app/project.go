package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// maxProjectTitleLen bounds a project title in characters.
const maxProjectTitleLen = 200

// CreateProjectInput is the use-case input for POST /v1/projects.
type CreateProjectInput struct {
	ProductID     string
	Title         string
	RequirementMD string
	UploadedBy    string
}

// UpdateProjectInput is the use-case input for PATCH /v1/projects. Nil means
// "field not supplied"; Status and ProductID are only accepted so they can be
// rejected with a precise field error.
type UpdateProjectInput struct {
	Title     *string
	Status    *int
	ProductID *string
}

// WriteRequirementInput is the use-case input for
// POST /v1/projects/requirement.
type WriteRequirementInput struct {
	BodyMD     string
	UploadedBy string
}

// ProjectPage is one page of projects plus the physical id of the last item
// when more rows exist (0 otherwise). The API layer renders NextID as the
// opaque next_cursor value.
type ProjectPage struct {
	Items  []domain.Project
	NextID int64
}

// ProjectService implements the project (= requirement) use cases. It owns
// validation, business rules and transaction boundaries; SQL stays in the
// store package and file I/O in the material package.
type ProjectService struct {
	store     *store.Store
	materials *material.Writer
	now       func() time.Time
}

// NewProjectService builds the project service on top of an open store and the
// material writer that owns data_dir/objects.
func NewProjectService(st *store.Store, materials *material.Writer) *ProjectService {
	return &ProjectService{
		store:     st,
		materials: materials,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// MaxMaterialBytes returns the configured material_max_bytes, so the HTTP layer
// can size its request body limit.
func (s *ProjectService) MaxMaterialBytes() int64 { return s.materials.MaxBytes() }

// CreateProject inserts a project and stores its requirement in one
// transaction: project row → kind=1 material → requirement_material_id
// backfill. A failure at any step rolls everything back, and the object file
// created by this request is removed when the transaction did not commit.
func (s *ProjectService) CreateProject(ctx context.Context, in CreateProjectInput) (domain.ProjectDetail, error) {
	normalized, fields := s.validateCreateProject(in)
	if len(fields) > 0 {
		return domain.ProjectDetail{}, &domain.ValidationError{Fields: fields}
	}

	var (
		out     domain.ProjectDetail
		written material.Stored
	)

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		product, err := store.GetProductByProductID(ctx, tx, normalized.ProductID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "product", Key: normalized.ProductID}
			}
			return err
		}

		// The sequence number is taken under the write lock so that
		// UNIQUE(product_id, seq_no) can never be violated by a concurrent
		// create.
		seq, err := store.NextSeqNo(ctx, tx, "projects", "product_id", product.ID)
		if err != nil {
			return err
		}

		ts := formatTimestamp(s.timestamp())
		projectID := fmt.Sprintf("%s-%d", product.ProductID, seq)

		// Insert the project first with an empty pointer, then store the
		// requirement and backfill it. The order is what makes the rollback
		// story simple: a failed material write leaves no half-built project,
		// and a failed backfill leaves neither a materials row nor an object.
		id, err := store.CreateProject(ctx, tx, store.NewProject{
			ProjectID:             projectID,
			ProductID:             product.ID,
			SeqNo:                 seq,
			Title:                 normalized.Title,
			RequirementMaterialID: nil,
			Status:                domain.StatusDraft,
			Version:               1,
			CreateTime:            ts,
			UpdateTime:            ts,
		})
		if err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.ConflictError{Reason: fmt.Sprintf(
					"project %q already exists", projectID)}
			}
			return err
		}

		written, err = s.materials.Write(ctx, tx, material.WriteInput{
			Kind:       domain.MaterialKindRequirement,
			Title:      normalized.Title,
			Body:       []byte(normalized.RequirementMD),
			UploadedBy: normalized.UploadedBy,
		})
		if err != nil {
			return err
		}

		if err := store.AttachRequirementPointer(ctx, tx, id, written.Material.ID); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, id)
		return err
	})
	if err != nil {
		// The materials row died with the rolled-back transaction; the object
		// file did not. Drop it only when this write created it.
		if written.NewObject {
			_ = s.materials.RemoveNewObject(written)
		}
		return domain.ProjectDetail{}, err
	}
	return out, nil
}

// GetProject returns one project with its requirement body, current spec (if
// any) and task statistics.
func (s *ProjectService) GetProject(ctx context.Context, projectID string) (domain.ProjectDetail, error) {
	if strings.TrimSpace(projectID) == "" {
		return domain.ProjectDetail{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	row, err := store.GetProjectByProjectID(ctx, s.store.Read(), projectID)
	if err != nil {
		if store.IsNotFound(err) {
			return domain.ProjectDetail{}, &domain.NotFoundError{Resource: "project", Key: projectID}
		}
		return domain.ProjectDetail{}, err
	}
	return s.detail(ctx, s.store.Read(), row.ID)
}

// ListProjects returns a stable page ordered by physical id. It reads limit+1
// rows so the caller knows whether a next page exists without a second query.
func (s *ProjectService) ListProjects(ctx context.Context, productID string, status *int, afterID int64, limit int) (ProjectPage, error) {
	if limit < 1 || limit > maxListLimit {
		return ProjectPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "limit", Reason: fmt.Sprintf("must be between 1 and %d", maxListLimit)},
		}}
	}
	if afterID < 0 {
		return ProjectPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "cursor", Reason: "is invalid"},
		}}
	}
	if status != nil && (*status < domain.StatusDraft || *status > domain.StatusCancelled) {
		return ProjectPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "status", Reason: "must be between 0 and 7"},
		}}
	}

	rows, err := store.ListProjects(ctx, s.store.Read(), store.ProjectFilter{
		ProductCode: strings.TrimSpace(productID),
		Status:      status,
		AfterID:     afterID,
		Limit:       limit + 1,
	})
	if err != nil {
		return ProjectPage{}, err
	}

	page := ProjectPage{Items: make([]domain.Project, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			page.NextID = page.Items[limit-1].ID
			break
		}
		project, err := projectFromRow(row)
		if err != nil {
			return ProjectPage{}, err
		}
		page.Items = append(page.Items, project)
	}
	return page, nil
}

// UpdateProject renames one project. Only the title is business-writable: the
// status moves through the action endpoints and the numbering/pointers are
// platform-maintained (tech design §4.3).
func (s *ProjectService) UpdateProject(ctx context.Context, projectID string, in UpdateProjectInput) (domain.Project, error) {
	if strings.TrimSpace(projectID) == "" {
		return domain.Project{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	title, fields := validateUpdateProject(in)
	if len(fields) > 0 {
		return domain.Project{}, &domain.ValidationError{Fields: fields}
	}

	var out domain.Project
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}

		if err := store.UpdateProjectTitle(ctx, tx, row.ID, title, formatTimestamp(s.nextTimestamp(row.UpdateTime))); err != nil {
			return err
		}

		fresh, err := store.GetProjectByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out, err = projectFromRow(fresh)
		return err
	})
	if err != nil {
		return domain.Project{}, err
	}
	return out, nil
}

// DeleteProject removes a draft/cancelled project that owns no child objects.
//
// The status, spec and task checks run in the same write transaction as the
// DELETE, so nothing can be attached between the check and the removal.
func (s *ProjectService) DeleteProject(ctx context.Context, projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}

		if row.Status != domain.StatusDraft && row.Status != domain.StatusCancelled {
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"project %q is in status %s; only draft and cancelled projects can be deleted",
				projectID, domain.StatusName(row.Status))}
		}
		if row.SpecMaterialID != nil {
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"project %q already has a spec", projectID)}
		}
		tasks, err := store.CountTasksByProject(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if tasks > 0 {
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"project %q still has %d task(s)", projectID, tasks)}
		}
		return store.DeleteProject(ctx, tx, row.ID)
	})
}

// WriteRequirement appends a new requirement version and moves the pointer.
//
// Allowed while the project is draft or in spec review: past that point the
// requirement is the baseline the plan was built on.
func (s *ProjectService) WriteRequirement(ctx context.Context, projectID string, in WriteRequirementInput) (domain.ProjectDetail, error) {
	if strings.TrimSpace(projectID) == "" {
		return domain.ProjectDetail{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	body, fields := s.validateRequirementBody("body_md", in.BodyMD)
	if len(fields) > 0 {
		return domain.ProjectDetail{}, &domain.ValidationError{Fields: fields}
	}

	uploadedBy := actorOrDefault(in.UploadedBy)

	var (
		out     domain.ProjectDetail
		written material.Stored
	)

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}
		if row.Status != domain.StatusDraft && row.Status != domain.StatusSpecReview {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; the requirement can only be rewritten in draft or spec_review",
				projectID, domain.StatusName(row.Status))}
		}

		written, err = s.materials.Write(ctx, tx, material.WriteInput{
			Kind:               domain.MaterialKindRequirement,
			Title:              row.Title,
			Body:               body,
			UploadedBy:         uploadedBy,
			PreviousMaterialID: row.RequirementMaterialID,
		})
		if err != nil {
			return err
		}

		if err := store.MoveRequirementPointer(ctx, tx, row.ID, written.Material.ID,
			formatTimestamp(s.nextTimestamp(row.UpdateTime))); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, row.ID)
		return err
	})
	if err != nil {
		if written.NewObject {
			_ = s.materials.RemoveNewObject(written)
		}
		return domain.ProjectDetail{}, err
	}
	return out, nil
}

// detail loads everything the detail response needs: the project row, the
// requirement body from the object store, the spec summary and the task
// histogram. It works inside a write transaction (q = tx) and outside it.
func (s *ProjectService) detail(ctx context.Context, q store.Querier, projectPhysicalID int64) (domain.ProjectDetail, error) {
	row, err := store.GetProjectByID(ctx, q, projectPhysicalID)
	if err != nil {
		return domain.ProjectDetail{}, err
	}
	project, err := projectFromRow(row)
	if err != nil {
		return domain.ProjectDetail{}, err
	}

	histogram, err := store.TaskStatusHistogram(ctx, q, row.ID)
	if err != nil {
		return domain.ProjectDetail{}, err
	}
	project.TaskStats.ByStatus = histogram

	detail := domain.ProjectDetail{Project: project}

	if row.RequirementMaterialID != nil {
		requirement, err := store.GetMaterialByID(ctx, q, *row.RequirementMaterialID)
		if err != nil {
			return domain.ProjectDetail{}, err
		}
		mapped, err := materialFromRow(requirement)
		if err != nil {
			return domain.ProjectDetail{}, err
		}
		detail.Requirement = &mapped

		body, err := s.materials.Read(requirement)
		if err != nil {
			return domain.ProjectDetail{}, err
		}
		detail.RequirementMD = string(body)
	}

	if row.SpecMaterialID != nil {
		spec, err := store.GetMaterialByID(ctx, q, *row.SpecMaterialID)
		if err != nil {
			return domain.ProjectDetail{}, err
		}
		mapped, err := materialFromRow(spec)
		if err != nil {
			return domain.ProjectDetail{}, err
		}
		detail.Spec = &mapped
	}

	return detail, nil
}

// validateCreateProject normalises and validates a create request.
func (s *ProjectService) validateCreateProject(in CreateProjectInput) (CreateProjectInput, []domain.FieldError) {
	var fields []domain.FieldError

	in.ProductID = strings.TrimSpace(in.ProductID)
	if in.ProductID == "" {
		fields = append(fields, domain.FieldError{Field: "product_id", Reason: "is required"})
	}

	in.Title = strings.TrimSpace(in.Title)
	switch {
	case in.Title == "":
		fields = append(fields, domain.FieldError{Field: "title", Reason: "is required"})
	case utf8.RuneCountInString(in.Title) > maxProjectTitleLen:
		fields = append(fields, domain.FieldError{
			Field:  "title",
			Reason: fmt.Sprintf("must be at most %d characters", maxProjectTitleLen),
		})
	}

	if _, bodyFields := s.validateRequirementBody("requirement_md", in.RequirementMD); len(bodyFields) > 0 {
		fields = append(fields, bodyFields...)
	}
	in.UploadedBy = actorOrDefault(in.UploadedBy)

	return in, fields
}

// validateRequirementBody checks a requirement body without rewriting it: the
// markdown is stored byte-for-byte, but whitespace-only content is rejected.
// field is the request field name the error should name.
func (s *ProjectService) validateRequirementBody(field, body string) ([]byte, []domain.FieldError) {
	if strings.TrimSpace(body) == "" {
		return nil, []domain.FieldError{{Field: field, Reason: "is required"}}
	}
	if int64(len(body)) > s.materials.MaxBytes() {
		return nil, []domain.FieldError{{
			Field:  field,
			Reason: fmt.Sprintf("must be at most %d bytes", s.materials.MaxBytes()),
		}}
	}
	return []byte(body), nil
}

// validateUpdateProject rejects every field a PATCH must not touch and
// requires at least one writable field.
func validateUpdateProject(in UpdateProjectInput) (string, []domain.FieldError) {
	var fields []domain.FieldError

	if in.ProductID != nil {
		fields = append(fields, domain.FieldError{Field: "product_id", Reason: "is immutable"})
	}
	if in.Status != nil {
		fields = append(fields, domain.FieldError{Field: "status", Reason: "is maintained by the action endpoints"})
	}

	title := ""
	if in.Title != nil {
		trimmed := strings.TrimSpace(*in.Title)
		switch {
		case trimmed == "":
			fields = append(fields, domain.FieldError{Field: "title", Reason: "is required"})
		case utf8.RuneCountInString(trimmed) > maxProjectTitleLen:
			fields = append(fields, domain.FieldError{
				Field:  "title",
				Reason: fmt.Sprintf("must be at most %d characters", maxProjectTitleLen),
			})
		default:
			title = trimmed
		}
	}

	if in.Title == nil && in.Status == nil && in.ProductID == nil {
		fields = append(fields, domain.FieldError{
			Field:  "body",
			Reason: "at least one of title is required",
		})
	}

	return title, fields
}

// projectFromRow converts the store read model into the domain entity.
func projectFromRow(row store.Project) (domain.Project, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return domain.Project{}, err
	}
	updateTime, err := parseTimestamp(row.UpdateTime)
	if err != nil {
		return domain.Project{}, err
	}
	return domain.Project{
		ID:                    row.ID,
		ProjectID:             row.ProjectID,
		ProductID:             row.ProductCode,
		SeqNo:                 row.SeqNo,
		Title:                 row.Title,
		Status:                row.Status,
		Reason:                stringValue(row.Reason),
		Version:               row.Version,
		RequirementMaterialID: int64Value(row.RequirementMaterialID),
		SpecMaterialID:        int64Value(row.SpecMaterialID),
		TaskStats: domain.TaskStats{
			Total: row.TaskTotal,
			Done:  row.TaskDone,
		},
		CreateTime: createTime,
		UpdateTime: updateTime,
	}, nil
}

// materialFromRow converts the store read model into the domain entity. Path
// stays in the store layer.
func materialFromRow(row store.Material) (domain.Material, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return domain.Material{}, err
	}
	return domain.Material{
		MaterialID: row.MaterialID,
		Kind:       row.Kind,
		Version:    row.Version,
		Title:      row.Title,
		SHA256:     row.SHA256,
		Size:       row.Size,
		UploadedBy: row.UploadedBy,
		CreateTime: createTime,
	}, nil
}

// timestamp returns the current time truncated to milliseconds, the precision
// persisted in create_time/update_time.
func (s *ProjectService) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// nextTimestamp returns a timestamp strictly after the persisted update_time,
// so two writes in the same millisecond still order deterministically.
func (s *ProjectService) nextTimestamp(previous string) time.Time {
	next := s.timestamp()
	old, err := parseTimestamp(previous)
	if err != nil {
		return next
	}
	if !next.After(old) {
		return old.Add(time.Millisecond)
	}
	return next
}

// int64Value maps a nullable physical id to 0 ("no pointer").
func int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// actorOrDefault maps an absent X-Actor declaration to the documented
// "unknown" actor (tech design §4.1).
func actorOrDefault(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "unknown"
	}
	return actor
}
