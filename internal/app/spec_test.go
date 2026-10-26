package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// titlePtr builds the optional title argument without a named variable.
func titlePtr(v string) *string { return &v }

// newSpecReviewProject creates one project and accepts it, so the project sits
// in spec_review (status 1) — the state a spec is normally submitted in.
func (e *projectTestEnv) newSpecReviewProject(t *testing.T, title string) string {
	t.Helper()
	e.createProduct(t, "APP")
	detail, err := e.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         title,
		RequirementMD: "# 需求\n\n正文\n",
		UploadedBy:    "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	projectID := detail.Project.ProjectID
	if _, err := e.service.AcceptProject(context.Background(), projectID, "pm"); err != nil {
		t.Fatalf("AcceptProject(%s): %v", projectID, err)
	}
	return projectID
}

// specChain reads the kind=2 version chain the way the API does.
func (e *projectTestEnv) specChain(t *testing.T, projectID string) MaterialChain {
	t.Helper()
	chain, err := NewMaterialService(e.store, e.writer).Chain(context.Background(), projectID, domain.MaterialKindSpec)
	if err != nil {
		t.Fatalf("Chain(%s, kind=2): %v", projectID, err)
	}
	return chain
}

// specPointer returns the business id the project's spec pointer names.
func (e *projectTestEnv) specPointer(t *testing.T, projectID string) string {
	t.Helper()
	var materialID *string
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT m.material_id
		  FROM projects p
		  JOIN materials m ON m.id = p.spec_material_id
		 WHERE p.project_id = ?`, projectID).Scan(&materialID); err != nil {
		t.Fatalf("read spec pointer of %s: %v", projectID, err)
	}
	if materialID == nil {
		return ""
	}
	return *materialID
}

func mustSubmitSpec(t *testing.T, e *projectTestEnv, projectID string, title *string, body string) SpecSubmission {
	t.Helper()
	result, err := e.service.SubmitSpec(context.Background(), projectID, "agent:cto", title, body)
	if err != nil {
		t.Fatalf("SubmitSpec(%s): %v", projectID, err)
	}
	return result
}

// TestSubmitSpecVersionChain covers the whole review loop: v1 → v2, a rejection
// that leaves the gate and the reason in place, v3 on top of v2, and an
// approval that clears the reason without moving the pointer.
func TestSubmitSpecVersionChain(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	// v1 with no title: the project title is the fallback.
	first := mustSubmitSpec(t, env, projectID, nil, "# 方案 v1\n\n第一版正文\n")
	if first.Spec.Kind != domain.MaterialKindSpec {
		t.Errorf("kind = %d, want %d", first.Spec.Kind, domain.MaterialKindSpec)
	}
	if first.Spec.Version != 1 {
		t.Errorf("spec version = %d, want 1", first.Spec.Version)
	}
	if first.PreviousMaterialID != "" {
		t.Errorf("previous = %q, want empty for the root", first.PreviousMaterialID)
	}
	if first.Spec.Title != "登录页改版" {
		t.Errorf("title = %q, want the project title fallback", first.Spec.Title)
	}
	if first.Spec.UploadedBy != "agent:cto" {
		t.Errorf("uploaded_by = %q, want agent:cto", first.Spec.UploadedBy)
	}
	if got := env.specPointer(t, projectID); got != first.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want %q", got, first.Spec.MaterialID)
	}
	// Submitting must not move the gate.
	if state := env.projectState(t, projectID); state.Status != domain.StatusSpecReview {
		t.Errorf("status = %d, want %d (spec_review)", state.Status, domain.StatusSpecReview)
	}

	// v2 with an explicit title, chained onto v1.
	title := "登录页改版方案（二稿）"
	second := mustSubmitSpec(t, env, projectID, &title, "# 方案 v2\n\n第二版正文\n")
	if second.Spec.Version != 2 {
		t.Errorf("spec version = %d, want 2", second.Spec.Version)
	}
	if second.PreviousMaterialID != first.Spec.MaterialID {
		t.Errorf("previous = %q, want %q", second.PreviousMaterialID, first.Spec.MaterialID)
	}
	if second.Spec.Title != title {
		t.Errorf("title = %q, want %q", second.Spec.Title, title)
	}
	if got := env.specPointer(t, projectID); got != second.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want %q", got, second.Spec.MaterialID)
	}
	if got := env.specChain(t, projectID); got.CurrentMaterialID != second.Spec.MaterialID {
		t.Errorf("chain head = %q, want %q", got.CurrentMaterialID, second.Spec.MaterialID)
	}

	// Reject: status stays spec_review, reason is readable, version bumps.
	rejected, err := env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionReject,
		Reason:        "并发方案没有覆盖会话续期",
		ExpectVersion: intPtr(env.projectState(t, projectID).Version),
		Actor:         "human:liu",
	})
	if err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	if rejected.ToStatus != domain.StatusSpecReview {
		t.Errorf("to status = %d, want %d", rejected.ToStatus, domain.StatusSpecReview)
	}
	if got := env.specPointer(t, projectID); got != second.Spec.MaterialID {
		t.Errorf("reject must not move the spec pointer: %q", got)
	}
	if state := env.projectState(t, projectID); state.Reason == nil || *state.Reason == "" {
		t.Errorf("reason = %v, want the rejection reason", state.Reason)
	}

	// v3 after the rejection: chains onto v2 and becomes the pointer target.
	third := mustSubmitSpec(t, env, projectID, nil, "# 方案 v3\n\n补充了会话续期\n")
	if third.Spec.Version != 3 {
		t.Errorf("spec version = %d, want 3", third.Spec.Version)
	}
	if third.PreviousMaterialID != second.Spec.MaterialID {
		t.Errorf("previous = %q, want %q", third.PreviousMaterialID, second.Spec.MaterialID)
	}
	if got := env.specPointer(t, projectID); got != third.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want %q", got, third.Spec.MaterialID)
	}
	// Resubmitting keeps the reason visible until the next approval.
	if state := env.projectState(t, projectID); state.Reason == nil || !strings.Contains(*state.Reason, "会话续期") {
		t.Errorf("reason = %v, want it preserved across resubmission", state.Reason)
	}

	chain := env.specChain(t, projectID)
	if len(chain.Versions) != 3 {
		t.Fatalf("chain length = %d, want 3", len(chain.Versions))
	}
	for i, link := range chain.Versions {
		if link.Material.Version != i+1 {
			t.Errorf("versions[%d] = %d, want %d", i, link.Material.Version, i+1)
		}
		if i == 0 {
			if link.PreviousMaterialID != "" {
				t.Errorf("root previous = %q, want empty", link.PreviousMaterialID)
			}
			continue
		}
		if want := chain.Versions[i-1].Material.MaterialID; link.PreviousMaterialID != want {
			t.Errorf("versions[%d].previous = %q, want %q", i, link.PreviousMaterialID, want)
		}
	}

	// Approve: planning, reason cleared, pointer still v3.
	approved, err := env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: intPtr(env.projectState(t, projectID).Version),
		Actor:         "human:liu",
	})
	if err != nil {
		t.Fatalf("Decide(approve): %v", err)
	}
	if approved.ToStatus != domain.StatusPlanning {
		t.Errorf("to status = %d, want %d (planning)", approved.ToStatus, domain.StatusPlanning)
	}
	state := env.projectState(t, projectID)
	if state.Reason != nil {
		t.Errorf("reason = %v, want nil after approval", *state.Reason)
	}
	if got := env.specPointer(t, projectID); got != third.Spec.MaterialID {
		t.Errorf("approve must not move the spec pointer: %q", got)
	}

	// planning accepts another spec (the CTO may revise while planning).
	fourth := mustSubmitSpec(t, env, projectID, nil, "# 方案 v4\n\nplanning 阶段再改一次\n")
	if fourth.Spec.Version != 4 {
		t.Errorf("spec version = %d, want 4", fourth.Spec.Version)
	}
	if fourth.Project.Status != domain.StatusPlanning {
		t.Errorf("status = %d, want %d (planning)", fourth.Project.Status, domain.StatusPlanning)
	}
}

func TestSubmitSpecStateGuard(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	title := "方案"
	for _, status := range []int{
		domain.StatusDraft,
		domain.StatusPlanReview,
		domain.StatusDeveloping,
		domain.StatusDeliveryReview,
		domain.StatusAccepted,
		domain.StatusCancelled,
	} {
		t.Run(domain.StatusName(status), func(t *testing.T) {
			env.setProjectState(t, projectID, status, 7, nil)
			_, err := env.service.SubmitSpec(context.Background(), projectID, "agent:cto", &title, "# 方案\n")
			if !errors.Is(err, domain.ErrInvalidState) {
				t.Fatalf("error = %v, want invalid_state", err)
			}
			var stateErr *domain.InvalidStateError
			if !errors.As(err, &stateErr) {
				t.Fatalf("error = %T, want *domain.InvalidStateError", err)
			}
			if !strings.Contains(stateErr.Reason, domain.StatusName(status)) {
				t.Errorf("reason = %q, want it to name %s", stateErr.Reason, domain.StatusName(status))
			}
		})
	}

	// Draft must not have been written to either.
	if got := env.count(t, "SELECT COUNT(*) FROM materials WHERE kind = ?", domain.MaterialKindSpec); got != 0 {
		t.Errorf("kind=2 rows = %d, want 0", got)
	}

	// spec_review and planning are the two allowed states.
	for _, status := range []int{domain.StatusSpecReview, domain.StatusPlanning} {
		t.Run("allowed_"+domain.StatusName(status), func(t *testing.T) {
			env.setProjectState(t, projectID, status, 7, nil)
			if _, err := env.service.SubmitSpec(context.Background(), projectID, "agent:cto", &title, "# 方案\n"); err != nil {
				t.Fatalf("SubmitSpec in %s: %v", domain.StatusName(status), err)
			}
		})
	}
}

func TestSubmitSpecValidation(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	blank := "   "
	tooLong := strings.Repeat("长", maxSpecTitleLen+1)

	cases := []struct {
		name      string
		title     *string
		body      string
		wantField string
	}{
		{"blank title", &blank, "# 方案\n", "title"},
		{"too long title", &tooLong, "# 方案\n", "title"},
		{"empty body", titlePtr("方案"), "", "body_md"},
		{"blank body", titlePtr("方案"), " \n\t ", "body_md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.service.SubmitSpec(context.Background(), projectID, "agent:cto", tc.title, tc.body)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want *domain.ValidationError", err)
			}
			if len(validation.Fields) != 1 || validation.Fields[0].Field != tc.wantField {
				t.Fatalf("fields = %+v, want one %s error", validation.Fields, tc.wantField)
			}
		})
	}

	if got := env.count(t, "SELECT COUNT(*) FROM materials WHERE kind = ?", domain.MaterialKindSpec); got != 0 {
		t.Errorf("kind=2 rows = %d, want 0 after rejected requests", got)
	}

	if _, err := env.service.SubmitSpec(context.Background(), "", "agent:cto", titlePtr("方案"), "# 方案\n"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("empty project id: error = %v, want invalid_argument", err)
	}
	if _, err := env.service.SubmitSpec(context.Background(), "APP-404", "agent:cto", titlePtr("方案"), "# 方案\n"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown project: error = %v, want not_found", err)
	}
}

// TestSubmitSpecTooLarge drives the material_max_bytes path: the writer aborts,
// so no row is inserted and the pointer keeps pointing at the previous version.
func TestSubmitSpecTooLarge(t *testing.T) {
	const limit = 64
	env := newProjectTestEnvWithLimit(t, limit)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	small := mustSubmitSpec(t, env, projectID, nil, "# v1\n")
	objectsBefore := countObjectFiles(t, env.dataDir)

	_, err := env.service.SubmitSpec(context.Background(), projectID, "agent:cto", nil, strings.Repeat("超", limit))
	var tooLarge *domain.TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("error = %v, want *domain.TooLargeError", err)
	}
	if tooLarge.Field != "body_md" {
		t.Errorf("field = %q, want body_md", tooLarge.Field)
	}
	if tooLarge.Limit != limit {
		t.Errorf("limit = %d, want %d", tooLarge.Limit, limit)
	}

	if got := env.count(t, "SELECT COUNT(*) FROM materials WHERE kind = ?", domain.MaterialKindSpec); got != 1 {
		t.Errorf("kind=2 rows = %d, want 1 (the oversized write must not land)", got)
	}
	if got := env.specPointer(t, projectID); got != small.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want the unchanged %q", got, small.Spec.MaterialID)
	}
	if got := countObjectFiles(t, env.dataDir); got != objectsBefore {
		t.Errorf("object files = %d, want unchanged %d", got, objectsBefore)
	}
}

// TestSubmitSpecConcurrent submits ten specs at once and asserts the chain stays
// linear: versions 1..N exactly once each, every link pointing at its
// predecessor, and the pointer on the last version.
func TestSubmitSpecConcurrent(t *testing.T) {
	const submissions = 10
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	materialIDs := make([]string, submissions)
	errs := make([]error, submissions)
	var wg sync.WaitGroup
	for i := 0; i < submissions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := env.service.SubmitSpec(context.Background(), projectID, fmt.Sprintf("agent:cto-%d", i), nil,
				fmt.Sprintf("# 方案 v%d\n\n并发提交 %d\n", i, i))
			if err != nil {
				errs[i] = err
				return
			}
			materialIDs[i] = result.Spec.MaterialID
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	chain := env.specChain(t, projectID)
	if len(chain.Versions) != submissions {
		t.Fatalf("chain length = %d, want %d", len(chain.Versions), submissions)
	}
	seenVersions := make(map[int]string, submissions)
	seenIDs := make(map[string]struct{}, submissions)
	for i, link := range chain.Versions {
		if want := i + 1; link.Material.Version != want {
			t.Errorf("versions[%d] = %d, want %d (no gaps, no forks)", i, link.Material.Version, want)
		}
		if _, duplicate := seenVersions[link.Material.Version]; duplicate {
			t.Errorf("version %d appears twice", link.Material.Version)
		}
		seenVersions[link.Material.Version] = link.Material.MaterialID
		if _, duplicate := seenIDs[link.Material.MaterialID]; duplicate {
			t.Errorf("material %s appears twice", link.Material.MaterialID)
		}
		seenIDs[link.Material.MaterialID] = struct{}{}

		if i == 0 {
			if link.PreviousMaterialID != "" {
				t.Errorf("root previous = %q, want empty", link.PreviousMaterialID)
			}
			continue
		}
		if want := chain.Versions[i-1].Material.MaterialID; link.PreviousMaterialID != want {
			t.Errorf("versions[%d].previous = %q, want %q", i, link.PreviousMaterialID, want)
		}
	}

	head := chain.Versions[len(chain.Versions)-1].Material
	if got := env.specPointer(t, projectID); got != head.MaterialID {
		t.Errorf("spec pointer = %q, want the v%d material %q", got, head.Version, head.MaterialID)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusSpecReview {
		t.Errorf("status = %d, want %d", state.Status, domain.StatusSpecReview)
	}

	// The pointer moving only forward is not enough: the project version must
	// have advanced once per submission on top of the accept bump.
	wantVersion := 1 /* create */ + 1 /* accept */ + submissions
	if state := env.projectState(t, projectID); state.Version != wantVersion {
		t.Errorf("project version = %d, want %d", state.Version, wantVersion)
	}
}

// TestSubmitSpecDedup checks content addressing: the same body twice yields one
// object file and two material rows.
func TestSubmitSpecDedup(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	const body = "# 方案\n\n同一份正文\n"
	first := mustSubmitSpec(t, env, projectID, nil, body)
	second := mustSubmitSpec(t, env, projectID, nil, body)

	if first.Spec.SHA256 != second.Spec.SHA256 {
		t.Fatalf("sha = %q / %q, want identical", first.Spec.SHA256, second.Spec.SHA256)
	}
	if first.Spec.MaterialID == second.Spec.MaterialID {
		t.Fatal("two versions must have distinct material ids")
	}
	if second.Spec.Version != 2 || second.PreviousMaterialID != first.Spec.MaterialID {
		t.Errorf("v2 = %+v previous %q, want version 2 on top of %q", second.Spec, second.PreviousMaterialID, first.Spec.MaterialID)
	}
	if got := env.count(t, "SELECT COUNT(*) FROM materials WHERE kind = ?", domain.MaterialKindSpec); got != 2 {
		t.Errorf("kind=2 rows = %d, want 2", got)
	}
	if got := env.count(t, "SELECT COUNT(*) FROM materials WHERE sha256 = ?", first.Spec.SHA256); got != 2 {
		t.Errorf("rows for one sha = %d, want 2", got)
	}
	// Requirement (1 object) + one shared spec object.
	if got := countObjectFiles(t, env.dataDir); got != 2 {
		t.Errorf("object files = %d, want 2", got)
	}
}

// TestProjectDetailSpecExcerpt covers the detail preview: null without a spec,
// and the first 200 runes of the current body afterwards, never cut mid-rune.
func TestProjectDetailSpecExcerpt(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	detail, err := env.service.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if detail.Spec != nil || detail.SpecExcerpt != nil {
		t.Fatalf("spec = %+v, excerpt = %v; want both nil before the first submission", detail.Spec, detail.SpecExcerpt)
	}

	// 199 CJK runes then an emoji straddling position 200: the excerpt ends on
	// the emoji, not inside its 4-byte encoding.
	body := strings.Repeat("方", specExcerptRunes-1) + "🚀" + strings.Repeat("尾", 60)
	submitted := mustSubmitSpec(t, env, projectID, nil, body)

	detail, err = env.service.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if detail.Spec == nil || detail.Spec.MaterialID != submitted.Spec.MaterialID {
		t.Fatalf("spec = %+v, want the submitted version", detail.Spec)
	}
	if detail.SpecExcerpt == nil {
		t.Fatal("spec_excerpt = nil, want a preview")
	}
	excerpt := *detail.SpecExcerpt
	if !utf8.ValidString(excerpt) {
		t.Fatalf("excerpt is not valid UTF-8: %q", excerpt)
	}
	if got := utf8.RuneCountInString(excerpt); got != specExcerptRunes {
		t.Errorf("excerpt runes = %d, want %d", got, specExcerptRunes)
	}
	if !strings.HasSuffix(excerpt, "🚀") {
		t.Errorf("excerpt = %q, want it to end on the emoji at rune %d", excerpt, specExcerptRunes)
	}
	if want := string([]rune(body)[:specExcerptRunes]); excerpt != want {
		t.Errorf("excerpt = %q, want %q", excerpt, want)
	}

	// A short body is previewed in full.
	short := mustSubmitSpec(t, env, projectID, nil, "# 短方案\n")
	detail, err = env.service.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if detail.SpecExcerpt == nil || *detail.SpecExcerpt != "# 短方案\n" {
		t.Errorf("excerpt = %v, want the whole short body", detail.SpecExcerpt)
	}
	if detail.Spec == nil || detail.Spec.Version != short.Spec.Version {
		t.Errorf("spec version = %v, want %d", detail.Spec, short.Spec.Version)
	}
}

// TestExcerptRunes pins the helper directly, including the boundaries.
func TestExcerptRunes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"empty", "", 3, ""},
		{"shorter than limit", "ab", 3, "ab"},
		{"exact", "abc", 3, "abc"},
		{"cut", "abcdef", 3, "abc"},
		{"zero limit", "abc", 0, ""},
		{"multibyte", "中文🚀x", 3, "中文🚀"},
		{"multibyte cut", "中文🚀x", 2, "中文"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := excerptRunes(tc.in, tc.limit); got != tc.want {
				t.Errorf("excerptRunes(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}
