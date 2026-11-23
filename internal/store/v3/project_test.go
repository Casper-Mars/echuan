package v3

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

func TestCreateProjectAndLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")

	project, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP",
		Name:      "登录改造",
		ContextMD: "issue #12；仓库画像见附件",
		Actor:     "cto",
		Now:       testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.ProjectID != "APP-1" {
		t.Errorf("project id = %q, want APP-1", project.ProjectID)
	}
	if project.SeqNo != 1 {
		t.Errorf("seq = %d, want 1", project.SeqNo)
	}
	if project.Status != enum.ProjectOpen {
		t.Errorf("status = %s, want open", project.Status)
	}
	if project.ContextMD == "" {
		t.Error("context_md was dropped")
	}

	second, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP", Name: "第二个", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}
	if second.ProjectID != "APP-2" {
		t.Errorf("second project id = %q, want APP-2", second.ProjectID)
	}

	open := enum.ProjectOpen
	list, err := ListProjects(ctx, st.Read(), &open, "", 0, 10)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("open projects = %d, want 2", len(list))
	}

	if err := UpdateProjectContext(ctx, st, "APP-1", "登录改造 v2", "新上下文", "cto", testNow); err != nil {
		t.Fatalf("UpdateProjectContext: %v", err)
	}
	updated, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "登录改造 v2" || updated.ContextMD != "新上下文" {
		t.Errorf("updated = %+v", updated)
	}

	if err := CloseProject(ctx, st, "APP-1", false, "cto", testNow); err != nil {
		t.Fatalf("CloseProject: %v", err)
	}
	closed, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject closed: %v", err)
	}
	if closed.Status != enum.ProjectClosed || closed.ClosedAt == nil {
		t.Errorf("closed = %+v", closed)
	}

	if err := ReopenProject(ctx, st, "APP-1", "cto", testNow); err != nil {
		t.Fatalf("ReopenProject: %v", err)
	}
	reopened, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject reopened: %v", err)
	}
	if reopened.Status != enum.ProjectOpen || reopened.ClosedAt != nil {
		t.Errorf("reopened = %+v", reopened)
	}
}

func TestCloseProjectRequiresNoActiveTasks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP", Name: "关闭测试", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID,
		TaskID:    "t_req",
		Kind:      enum.Requirement,
		Title:     "需求",
		Now:       testNow,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := CloseProject(ctx, st, project.ProjectID, false, "cto", testNow); !errors.Is(err, ErrProjectNotEmpty) {
		t.Fatalf("CloseProject error = %v, want ErrProjectNotEmpty", err)
	}

	if err := CloseProject(ctx, st, project.ProjectID, true, "cto", testNow); err != nil {
		t.Fatalf("force CloseProject: %v", err)
	}

	task, err := GetTask(ctx, st.Read(), "t_req")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Cancelled {
		t.Errorf("task status = %s, want cancelled", task.Status)
	}

	events, err := ListEvents(ctx, st.Read(), "t_req", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Event == enum.EventCancelled {
			found = true
		}
	}
	if !found {
		t.Error("cancelled event missing")
	}
}

// newProject creates one project for an already seeded product and returns it.
func newProject(t *testing.T, st *storedb.Store, productCode, name string) Project {
	t.Helper()
	project, err := CreateProject(context.Background(), st, CreateProjectInput{
		ProductID: productCode, Name: name, Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject %s/%s: %v", productCode, name, err)
	}
	return project
}

// TestListProjectsNewestFirst pins the list order: ORDER BY p.id DESC, so the
// first row is the newest project (the issue #133 ask).
func TestListProjectsNewestFirst(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")

	first := newProject(t, st, "APP", "一")
	second := newProject(t, st, "APP", "二")
	third := newProject(t, st, "APP", "三")

	list, err := ListProjects(ctx, st.Read(), nil, "", 0, 10)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("projects = %d, want 3", len(list))
	}
	if list[0].ID != third.ID {
		t.Errorf("first id = %d, want newest %d (descending order)", list[0].ID, third.ID)
	}
	want := []string{third.ProjectID, second.ProjectID, first.ProjectID}
	for i, project := range list {
		if project.ProjectID != want[i] {
			t.Errorf("position %d = %s, want %s", i, project.ProjectID, want[i])
		}
		if i > 0 && list[i-1].ID <= project.ID {
			t.Errorf("ids not strictly descending at %d: %d then %d", i, list[i-1].ID, project.ID)
		}
	}
}

// TestListProjectsProductFilter covers the three product_id shapes: a real
// product id narrows the page, a blank value is the same as "not passed", and
// an unknown product id returns an empty page instead of an error.
func TestListProjectsProductFilter(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	seedProduct(t, st, "WEB")

	appFirst := newProject(t, st, "APP", "APP 一")
	appSecond := newProject(t, st, "APP", "APP 二")
	web := newProject(t, st, "WEB", "WEB 一")

	t.Run("matching product", func(t *testing.T) {
		list, err := ListProjects(ctx, st.Read(), nil, "APP", 0, 10)
		if err != nil {
			t.Fatalf("ListProjects : %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("APP projects = %d, want 2", len(list))
		}
		if list[0].ID != appSecond.ID || list[1].ID != appFirst.ID {
			t.Errorf("APP order = [%d %d], want [%d %d]",
				list[0].ID, list[1].ID, appSecond.ID, appFirst.ID)
		}
		for _, project := range list {
			if project.ProductCode != "APP" {
				t.Errorf("project %s leaked into the APP filter", project.ProjectID)
			}
		}
	})

	t.Run("blank means no filter", func(t *testing.T) {
		for _, blank := range []string{"", "   ", "\t"} {
			list, err := ListProjects(ctx, st.Read(), nil, blank, 0, 10)
			if err != nil {
				t.Fatalf("ListProjects %q: %v", blank, err)
			}
			if len(list) != 3 {
				t.Fatalf("blank %q projects = %d, want 3 (filter skipped)", blank, len(list))
			}
			if list[0].ID != web.ID {
				t.Errorf("blank %q first id = %d, want %d", blank, list[0].ID, web.ID)
			}
		}
	})

	t.Run("unknown product is an empty page", func(t *testing.T) {
		list, err := ListProjects(ctx, st.Read(), nil, "NOPE", 0, 10)
		if err != nil {
			t.Fatalf("ListProjects unknown product: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("unknown product items = %d, want 0", len(list))
		}
	})
}

// TestListProjectsAfterIDPagesTowardsOlder walks the keyset cursor down from
// the newest page: consecutive pages must not repeat or skip a project.
func TestListProjectsAfterIDPagesTowardsOlder(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")

	var all []int64
	for i := 1; i <= 5; i++ {
		all = append(all, newProject(t, st, "APP", fmt.Sprintf("项目-%d", i)).ID)
	}

	var seen []int64
	afterID := int64(0) // 0 = first (newest) page
	for page := 0; page < 4; page++ {
		list, err := ListProjects(ctx, st.Read(), nil, "", afterID, 2)
		if err != nil {
			t.Fatalf("ListProjects page %d (after_id=%d): %v", page, afterID, err)
		}
		if len(list) == 0 {
			break
		}
		for _, project := range list {
			seen = append(seen, project.ID)
		}
		afterID = list[len(list)-1].ID
	}

	if len(seen) != len(all) {
		t.Fatalf("paged ids = %v, want %d ids %v", seen, len(all), all)
	}
	unique := map[int64]bool{}
	for i, id := range seen {
		if unique[id] {
			t.Errorf("id %d repeated while paging: %v", id, seen)
		}
		unique[id] = true
		want := all[len(all)-1-i] // newest first
		if id != want {
			t.Errorf("position %d = %d, want %d (pages must not overlap or skip)", i, id, want)
		}
		if i > 0 && seen[i-1] <= id {
			t.Errorf("paged ids not descending at %d: %d then %d", i, seen[i-1], id)
		}
	}
}

// TestListProjectsStatusAndProductCombined covers the status + product_id
// combination: both predicates apply to the same page.
func TestListProjectsStatusAndProductCombined(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	seedProduct(t, st, "WEB")

	appClosed := newProject(t, st, "APP", "APP 关闭")
	appOpen := newProject(t, st, "APP", "APP 进行中")
	webClosed := newProject(t, st, "WEB", "WEB 关闭")
	for _, project := range []Project{appClosed, webClosed} {
		if err := CloseProject(ctx, st, project.ProjectID, false, "cto", testNow); err != nil {
			t.Fatalf("CloseProject %s: %v", project.ProjectID, err)
		}
	}

	open := enum.ProjectOpen
	openApp, err := ListProjects(ctx, st.Read(), &open, "APP", 0, 10)
	if err != nil {
		t.Fatalf("ListProjects open+APP: %v", err)
	}
	if len(openApp) != 1 || openApp[0].ID != appOpen.ID {
		t.Errorf("open+APP = %+v, want only %s", openApp, appOpen.ProjectID)
	}

	closed := enum.ProjectClosed
	closedApp, err := ListProjects(ctx, st.Read(), &closed, "APP", 0, 10)
	if err != nil {
		t.Fatalf("ListProjects closed+APP: %v", err)
	}
	if len(closedApp) != 1 || closedApp[0].ID != appClosed.ID {
		t.Errorf("closed+APP = %+v, want only %s", closedApp, appClosed.ProjectID)
	}

	openWeb, err := ListProjects(ctx, st.Read(), &open, "WEB", 0, 10)
	if err != nil {
		t.Fatalf("ListProjects open+WEB: %v", err)
	}
	if len(openWeb) != 0 {
		t.Errorf("open+WEB = %+v, want empty (WEB project is closed)", openWeb)
	}
}
