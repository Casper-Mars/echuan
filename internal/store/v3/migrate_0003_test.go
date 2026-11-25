package v3

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	"github.com/Casper-Mars/echuan/migrations"
)

// 0003_kind_from_config.sql, read side.
//
// The migration must be invisible to the task surface: the rows /v1/tasks
// renders (title, description_md, context_md, depends_on) have to be byte-for-
// byte the same before and after it, and the new kind_parallel column has to be
// snapshotted by CreateTask so a new card keeps the stage's open_cards budget.

// migrationsThrough replays the embedded migration chain up to version v.
func migrationsThrough(t *testing.T, v int) fstest.MapFS {
	t.Helper()

	list, err := migrations.Load(migrations.FS)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	fsys := fstest.MapFS{}
	for _, m := range list {
		if m.Version > v {
			break
		}
		fsys[m.Name] = &fstest.MapFile{Data: m.SQL}
	}
	return fsys
}

// testStoreAtV2 opens a store migrated to 0002 only, i.e. the schema the
// previous release shipped, and seeds one product plus one open project.
func testStoreAtV2(t *testing.T) (*storedb.Store, string) {
	t.Helper()

	st, err := storedb.Open(storedb.Config{Path: filepath.Join(t.TempDir(), "state.db"), ReadPoolSize: 4})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := st.MigrateFS(context.Background(), migrationsThrough(t, 2)); err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}

	seedProduct(t, st, "APP")
	project, err := CreateProject(context.Background(), st, CreateProjectInput{
		ProductID: "APP", Name: "项目", ContextMD: "背景", Now: testNow,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return st, project.ProjectID
}

// taskSurface is what /v1/tasks answers per card: the fields the migration is
// forbidden to touch, keyed by the business id.
type taskSurface struct {
	TaskID        string
	Kind          enum.TaskKind
	Title         string
	DescriptionMD string
	ContextMD     string
	DependsOn     []string
}

func readTaskSurface(t *testing.T, st *storedb.Store, projectID string) []taskSurface {
	t.Helper()

	ctx := context.Background()
	tasks, err := ListTasks(ctx, st.Read(), TaskFilter{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	out := make([]taskSurface, 0, len(tasks))
	for _, task := range tasks {
		deps, err := ListDependencies(ctx, st.Read(), task.TaskID)
		if err != nil {
			t.Fatalf("dependencies of %s: %v", task.TaskID, err)
		}
		out = append(out, taskSurface{
			TaskID:        task.TaskID,
			Kind:          task.Kind,
			Title:         task.Title,
			DescriptionMD: task.DescriptionMD,
			ContextMD:     task.ContextMD,
			DependsOn:     deps,
		})
	}
	return out
}

// readTaskSurfaceSQL reads the same field set straight from SQLite. It is the
// "before" side of the comparison: the pre-0003 schema has no kind_parallel, and
// the new taskColumns would not even parse against it (a v2 database is migrated
// at startup, before any read).
func readTaskSurfaceSQL(t *testing.T, st *storedb.Store, projectID string) []taskSurface {
	t.Helper()

	ctx := context.Background()
	rows, err := st.Write().QueryContext(ctx, `
		SELECT t.task_id, t.kind, t.title, t.description_md, t.context_md
		  FROM tasks t JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ?
		 ORDER BY t.priority DESC, t.id ASC`, projectID)
	if err != nil {
		t.Fatalf("snapshot tasks: %v", err)
	}
	out := make([]taskSurface, 0, 8)
	for rows.Next() {
		var item taskSurface
		if err := rows.Scan(&item.TaskID, &item.Kind, &item.Title, &item.DescriptionMD, &item.ContextMD); err != nil {
			rows.Close()
			t.Fatalf("scan task: %v", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate tasks: %v", err)
	}
	// The write pool is a single connection: the task rows must be drained
	// before the dependency queries can borrow it.
	rows.Close()

	for i := range out {
		deps, err := st.Write().QueryContext(ctx, `
			SELECT u.task_id
			  FROM task_deps d
			  JOIN tasks t ON t.id = d.task_id
			  JOIN tasks u ON u.id = d.depends_on_task_id
			 WHERE t.task_id = ?
			 ORDER BY u.task_id ASC`, out[i].TaskID)
		if err != nil {
			t.Fatalf("snapshot deps of %s: %v", out[i].TaskID, err)
		}
		out[i].DependsOn = make([]string, 0, 2)
		for deps.Next() {
			var dep string
			if err := deps.Scan(&dep); err != nil {
				deps.Close()
				t.Fatalf("scan dep of %s: %v", out[i].TaskID, err)
			}
			out[i].DependsOn = append(out[i].DependsOn, dep)
		}
		if err := deps.Err(); err != nil {
			deps.Close()
			t.Fatalf("iterate deps of %s: %v", out[i].TaskID, err)
		}
		deps.Close()
	}
	return out
}

// TestMigrate0003TaskSurfaceUnchanged pins the "same snapshot, same fields"
// acceptance point: the four fields /v1/tasks renders are identical when read
// from the 0002 schema and when read back through ListTasks / ListDependencies
// (the query path behind GET /v1/tasks) after 0003.
func TestMigrate0003TaskSurfaceUnchanged(t *testing.T) {
	st, projectID := testStoreAtV2(t)
	ctx := context.Background()

	// Seeded with raw SQL: this database is still at 0002, so the tasks table has
	// no kind_parallel column yet — exactly the state a real upgrade runs on.
	var projectPK int64
	if err := st.Write().QueryRowContext(ctx,
		`SELECT id FROM projects WHERE project_id = ?`, projectID).Scan(&projectPK); err != nil {
		t.Fatalf("project pk: %v", err)
	}
	seedCard := func(taskID string, kind enum.TaskKind, dependsOn ...string) {
		t.Helper()
		res, err := st.Write().ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, kind, title, description_md, context_md,
			                   priority, status, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID, projectPK, kind, "标题 "+taskID, "正文 "+taskID,
			"由上游自动派生 "+taskID, int(kind), enum.Ready, testNow, testNow)
		if err != nil {
			t.Fatalf("seed %s: %v", taskID, err)
		}
		pk, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed %s id: %v", taskID, err)
		}
		for _, dep := range dependsOn {
			var depPK int64
			if err := st.Write().QueryRowContext(ctx,
				`SELECT id FROM tasks WHERE task_id = ?`, dep).Scan(&depPK); err != nil {
				t.Fatalf("resolve dep %s: %v", dep, err)
			}
			if _, err := st.Write().ExecContext(ctx,
				`INSERT INTO task_deps (task_id, depends_on_task_id) VALUES (?, ?)`, pk, depPK); err != nil {
				t.Fatalf("seed dep %s -> %s: %v", taskID, dep, err)
			}
		}
	}
	seedCard("t_req", enum.Requirement)
	seedCard("t_design", enum.Design, "t_req")
	seedCard("t_breakdown", enum.Breakdown, "t_design")
	seedCard("t_dev_1", enum.Dev, "t_breakdown")
	seedCard("t_dev_2", enum.Dev, "t_breakdown")

	before := readTaskSurfaceSQL(t, st, projectID)
	if len(before) != 5 {
		t.Fatalf("tasks before = %d, want 5", len(before))
	}

	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.OpenReadPool(ctx); err != nil {
		t.Fatalf("open read pool: %v", err)
	}

	if after := readTaskSurface(t, st, projectID); !reflect.DeepEqual(before, after) {
		t.Fatalf("task surface changed across 0003:\nbefore = %+v\nafter  = %+v", before, after)
	}

	// The snapshot column reflects the old behaviour: only dev keeps several
	// open cards per project.
	for _, want := range []struct {
		taskID   string
		parallel int
	}{
		{"t_req", 0}, {"t_design", 0}, {"t_breakdown", 0}, {"t_dev_1", 1}, {"t_dev_2", 1},
	} {
		task, err := GetTask(ctx, st.Read(), want.taskID)
		if err != nil {
			t.Fatalf("get %s: %v", want.taskID, err)
		}
		if task.KindParallel != want.parallel {
			t.Errorf("%s kind_parallel = %d, want %d", want.taskID, task.KindParallel, want.parallel)
		}
	}
}

// TestCreateTaskKindParallelSnapshot pins the write path: the new card stores
// the open_cards snapshot, an explicit value wins, and the cap the index
// enforces follows that value.
func TestCreateTaskKindParallelSnapshot(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP", Name: "项目", Now: testNow,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	create := func(taskID string, kind enum.TaskKind, parallel *int) (Task, error) {
		return CreateTask(ctx, st, CreateTaskInput{
			ProjectID: project.ProjectID, TaskID: taskID, Kind: kind,
			KindParallel: parallel, Title: "卡片", Now: testNow,
		})
	}

	// Built-in default flow: dev is open_cards: multiple, everything else single.
	task, err := create("t_dev_1", enum.Dev, nil)
	if err != nil {
		t.Fatalf("create dev: %v", err)
	}
	if task.KindParallel != 1 {
		t.Errorf("dev kind_parallel = %d, want 1", task.KindParallel)
	}
	if _, err := create("t_dev_2", enum.Dev, nil); err != nil {
		t.Fatalf("second open dev card: %v", err)
	}

	task, err = create("t_req", enum.Requirement, nil)
	if err != nil {
		t.Fatalf("create requirement: %v", err)
	}
	if task.KindParallel != 0 {
		t.Errorf("requirement kind_parallel = %d, want 0", task.KindParallel)
	}
	if _, err := create("t_req_2", enum.Requirement, nil); !errors.Is(err, storedb.ErrUniqueViolation) {
		t.Fatalf("second open requirement card error = %v, want ErrUniqueViolation", err)
	}

	// An explicit snapshot (a configured flow with a parallel non-dev stage) wins.
	parallel := 1
	for i := 1; i <= 2; i++ {
		if _, err := create(fmt.Sprintf("t_security_%d", i), enum.Requirement, &parallel); err != nil {
			t.Fatalf("explicit parallel card %d: %v", i, err)
		}
	}

	// Only 0/1 are legal.
	bad := 2
	if _, err := create("t_bad", enum.Requirement, &bad); err == nil {
		t.Fatal("kind_parallel = 2 was accepted, want an error")
	}
}
