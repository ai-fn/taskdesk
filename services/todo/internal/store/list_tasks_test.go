package store_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
)

func seed(t *testing.T, s *store.Store, userID, id string, status domain.Status, parent string) {
	t.Helper()
	now := time.Now().UTC()
	task := &domain.Task{
		ID: id, UserID: userID, Title: id, Priority: domain.PriorityMedium, Status: status,
		CreatedAt: now, UpdatedAt: now,
	}
	if parent != "" {
		task.ParentTaskID = &parent
	}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask %s: %v", id, err)
	}
}

// seedUserGraph gives userID a parent with 3 children (1 done, 1 soft-deleted),
// a task blocked by an open dependency, and one whose dependency is done.
func seedUserGraph(t *testing.T, s *store.Store, userID string) {
	t.Helper()
	ctx := context.Background()
	id := func(n string) string { return userID + "-" + n }

	seed(t, s, userID, id("parent"), domain.StatusTodo, "")
	seed(t, s, userID, id("c-done"), domain.StatusDone, id("parent"))
	seed(t, s, userID, id("c-open"), domain.StatusTodo, id("parent"))
	seed(t, s, userID, id("c-gone"), domain.StatusTodo, id("parent"))
	if _, err := testPool.Exec(ctx, `UPDATE todo.tasks SET deleted_at = now() WHERE id = $1`, id("c-gone")); err != nil {
		t.Fatal(err)
	}
	seed(t, s, userID, id("blocked"), domain.StatusTodo, "")
	seed(t, s, userID, id("open-dep"), domain.StatusTodo, "")
	seed(t, s, userID, id("free"), domain.StatusTodo, "")
	seed(t, s, userID, id("done-dep"), domain.StatusDone, "")
	for _, e := range [][2]string{{"blocked", "open-dep"}, {"free", "done-dep"}} {
		if err := s.AddDependency(ctx, userID, id(e[0]), id(e[1])); err != nil {
			t.Fatalf("AddDependency %v: %v", e, err)
		}
	}
}

func TestListTasks_SubtaskAndDependencyAggregatesPerUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedUserGraph(t, s, "u1")
	seedUserGraph(t, s, "u2") // other users' rows must not leak into u1's aggregates

	tasks, err := s.ListTasks(ctx, store.ListFilter{UserID: "u1", PageSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*domain.Task{}
	for _, tk := range tasks {
		byID[tk.ID] = tk
	}
	if len(byID) != 5 { // subtasks are excluded from the root listing
		t.Fatalf("root tasks = %d, want 5: %v", len(byID), ids(tasks))
	}

	if p := byID["u1-parent"]; p.SubtaskCount != 2 || p.SubtaskDoneCount != 1 {
		t.Errorf("parent subtasks = %d/%d done, want 2 total and 1 done", p.SubtaskCount, p.SubtaskDoneCount)
	}
	if b := byID["u1-blocked"]; !b.Blocked || !reflect.DeepEqual(b.DependsOnTaskIDs, []string{"u1-open-dep"}) {
		t.Errorf("blocked = %v deps = %v, want true and [u1-open-dep]", b.Blocked, b.DependsOnTaskIDs)
	}
	if f := byID["u1-free"]; f.Blocked || !reflect.DeepEqual(f.DependsOnTaskIDs, []string{"u1-done-dep"}) {
		t.Errorf("free = blocked %v deps = %v, want false and [u1-done-dep]", f.Blocked, f.DependsOnTaskIDs)
	}
	if n := byID["u1-open-dep"]; n.Blocked || n.SubtaskCount != 0 || len(n.DependsOnTaskIDs) != 0 {
		t.Errorf("plain task = %+v, want no aggregates", n)
	}

	unblocked, err := s.ListTasks(ctx, store.ListFilter{UserID: "u1", PageSize: 200, ExcludeBlocked: true})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(unblocked)
	sort.Strings(got)
	if want := []string{"u1-done-dep", "u1-free", "u1-open-dep", "u1-parent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ExcludeBlocked = %v, want %v", got, want)
	}

	parent := "u1-parent"
	kids, err := s.ListTasks(ctx, store.ListFilter{UserID: "u1", PageSize: 200, ParentTaskID: &parent})
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 2 {
		t.Errorf("children = %v, want the 2 live subtasks", ids(kids))
	}
}

func TestListDependencies_AggregatesPerUser(t *testing.T) {
	s := newStore(t)
	seedUserGraph(t, s, "u1")
	seedUserGraph(t, s, "u2")

	dependsOn, blocking, err := s.ListDependencies(context.Background(), "u1", "u1-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOn) != 1 || dependsOn[0].ID != "u1-open-dep" || dependsOn[0].Blocked {
		t.Errorf("dependsOn = %v, want [u1-open-dep] unblocked", ids(dependsOn))
	}
	if len(blocking) != 0 {
		t.Errorf("blocking = %v, want none", ids(blocking))
	}

	_, blocking, err = s.ListDependencies(context.Background(), "u1", "u1-open-dep")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 1 || blocking[0].ID != "u1-blocked" || !blocking[0].Blocked {
		t.Errorf("blocking = %v, want [u1-blocked] flagged blocked", ids(blocking))
	}
}

// Guards against a whole-table GROUP BY: the laterals must probe per row through indexes.
func TestListTasks_PlanProbesSubtasksAndDependenciesByIndex(t *testing.T) {
	newStore(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO todo.tasks (id, user_id, title, priority, status, created_at, updated_at)
		SELECT 'p-' || g, 'u' || (g % 50), 't', 2, 1, now() - g * interval '1 second', now()
		FROM generate_series(1, 5000) g;
		INSERT INTO todo.tasks (id, user_id, title, priority, status, created_at, updated_at, parent_task_id)
		SELECT 'c-' || g, 'u' || (g % 50), 't', 2, 1, now(), now(), 'p-' || g
		FROM generate_series(1, 5000) g;
		INSERT INTO todo.task_dependencies (task_id, depends_on_id, user_id)
		SELECT 'p-' || g, 'p-' || (g + 1), 'u' || (g % 50)
		FROM generate_series(1, 4999) g;
		ANALYZE todo.tasks; ANALYZE todo.task_dependencies;`); err != nil {
		t.Fatal(err)
	}

	rows, err := testPool.Query(ctx, `EXPLAIN SELECT `+store.TaskListCols+` `+store.TaskListJoins+`
		WHERE t.user_id = 'u1' AND t.deleted_at IS NULL AND t.parent_task_id IS NULL
		ORDER BY t.created_at DESC, t.id DESC LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"idx_tasks_parent", "task_dependencies_pkey"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan does not use %s:\n%s", want, plan)
		}
	}
	for _, bad := range []string{"Seq Scan on tasks c", "Seq Scan on task_dependencies d", "Seq Scan on tasks t2", "GroupAggregate", "HashAggregate"} {
		if strings.Contains(plan, bad) {
			t.Errorf("plan contains %q, want index probes per row:\n%s", bad, plan)
		}
	}
}

func TestNormalizePageSize_Boundaries(t *testing.T) {
	for in, want := range map[int32]int32{-1: 50, 0: 50, 1: 1, 50: 50, 200: 200, 201: 200, 5000: 200} {
		if got := store.NormalizePageSize(in); got != want {
			t.Errorf("NormalizePageSize(%d) = %d, want %d", in, got, want)
		}
	}
}
