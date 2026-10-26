package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
)

// victim owns list/group/task; attacker must not be able to reference them.
func seedVictim(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.CreateGroup(ctx, &domain.ListGroup{ID: "g-victim", UserID: "victim", Name: "g", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateList(ctx, &domain.List{ID: "l-victim", UserID: "victim", Name: "l", Color: "#000", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, s, "t-victim", "victim")
}

func newTask(id, user string, list, parent *string) *domain.Task {
	now := time.Now().UTC()
	return &domain.Task{ID: id, UserID: user, Title: id, Priority: domain.PriorityMedium, Status: domain.StatusTodo,
		ListID: list, ParentTaskID: parent, CreatedAt: now, UpdatedAt: now}
}

func ptr(s string) *string { return &s }

func TestForeignAndMissingReferencesAreRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedVictim(t, s)
	mustCreateTask(t, s, "t-own", "attacker")

	for name, tk := range map[string]*domain.Task{
		"foreign list":   newTask("a1", "attacker", ptr("l-victim"), nil),
		"missing list":   newTask("a2", "attacker", ptr("nope"), nil),
		"foreign parent": newTask("a3", "attacker", nil, ptr("t-victim")),
		"missing parent": newTask("a4", "attacker", nil, ptr("nope")),
	} {
		if err := s.CreateTaskWithEvent(ctx, tk); !errors.Is(err, store.ErrRefNotFound) {
			t.Errorf("CreateTaskWithEvent(%s) = %v, want ErrRefNotFound", name, err)
		}
		if err := s.CreateTask(ctx, tk); !errors.Is(err, store.ErrRefNotFound) {
			t.Errorf("CreateTask(%s) = %v, want ErrRefNotFound", name, err)
		}
	}
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM todo.outbox WHERE payload->>'user_id' = 'attacker'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rejected creates left %d outbox events (err %v)", n, err)
	}

	own := newTask("a5", "attacker", nil, ptr("t-own"))
	if err := s.CreateTaskWithEvent(ctx, own); err != nil {
		t.Errorf("own parent rejected: %v", err)
	}
}

func TestForeignListCannotBeAssignedOnUpdate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedVictim(t, s)
	mustCreateTask(t, s, "t-own", "attacker")
	task, err := s.GetTask(ctx, "attacker", "t-own")
	if err != nil {
		t.Fatal(err)
	}
	task.ListID = ptr("l-victim")
	move := func(c *domain.Task) error { c.ListID = ptr("l-victim"); return nil }
	if _, err := s.ModifyTask(ctx, "attacker", "t-own", store.Notify{}, move); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("ModifyTask = %v, want ErrRefNotFound", err)
	}
	if _, err := s.UpdateTask(ctx, task); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("UpdateTask = %v, want ErrRefNotFound", err)
	}
	if got, _ := s.GetTask(ctx, "attacker", "t-own"); got.ListID != nil {
		t.Errorf("task moved into the victim's list: %v", *got.ListID)
	}
}

func TestForeignGroupCannotBeUsedForLists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedVictim(t, s)
	now := time.Now().UTC()
	l := &domain.List{ID: "l-att", UserID: "attacker", Name: "l", Color: "#000", GroupID: ptr("g-victim"), CreatedAt: now, UpdatedAt: now}
	if err := s.CreateList(ctx, l); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("CreateList with a foreign group = %v, want ErrRefNotFound", err)
	}
	l.GroupID = nil
	if err := s.CreateList(ctx, l); err != nil {
		t.Fatal(err)
	}
	l.GroupID = ptr("g-victim")
	if _, err := s.UpdateList(ctx, l, true); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("UpdateList with a foreign group = %v, want ErrRefNotFound", err)
	}
	l.GroupID = ptr("nope")
	if _, err := s.UpdateList(ctx, l, true); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("UpdateList with a missing group = %v, want ErrRefNotFound (not a 500)", err)
	}
}

// A child row planted under a victim's task (written before the ownership checks) must not
// change the victim's counts or block their auto-complete.
func TestForeignSubtaskDoesNotAffectTheVictim(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "p", "victim")
	seed(t, s, "victim", "p-child", domain.StatusDone, "p")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO todo.tasks (id, user_id, title, parent_task_id, status, priority)
		VALUES ('planted', 'attacker', 'x', 'p', 1, 2)`); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.ListTasks(ctx, store.ListFilter{UserID: "victim", PageSize: 50})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks = %d tasks, %v; want the one root", len(tasks), err)
	}
	got := tasks[0]
	if got.SubtaskCount != 1 || got.SubtaskDoneCount != 1 {
		t.Errorf("subtasks = %d/%d, want 1/1: the planted child inflated the count", got.SubtaskDoneCount, got.SubtaskCount)
	}
	// Reopen the child, then complete it: the parent must follow despite the planted open child.
	if _, err := testPool.Exec(ctx, `UPDATE todo.tasks SET status = 1 WHERE id = 'p-child'`); err != nil {
		t.Fatal(err)
	}
	done := func(c *domain.Task) error { c.Status = domain.StatusDone; return nil }
	if _, err := s.ModifyTask(ctx, "victim", "p-child", store.Notify{}, done); err != nil {
		t.Fatal(err)
	}
	if p, err := s.GetTask(ctx, "victim", "p"); err != nil || p.Status != domain.StatusDone {
		t.Errorf("parent = %+v, %v; want it completed despite the planted open child", p, err)
	}
}

// A task whose list_id was planted before the checks existed stays editable and completable.
func TestRowsWithAForeignListStayEditableUntilTheListChanges(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedVictim(t, s)
	mustCreateTask(t, s, "t-legacy", "attacker")
	if _, err := testPool.Exec(ctx, `UPDATE todo.tasks SET list_id = 'l-victim' WHERE id = 't-legacy'`); err != nil {
		t.Fatal(err)
	}
	rename := func(c *domain.Task) error { c.Title = "renamed"; return nil }
	if _, err := s.ModifyTask(ctx, "attacker", "t-legacy", store.Notify{}, rename); err != nil {
		t.Errorf("title edit on a legacy row: %v", err)
	}
	moveElsewhere := func(c *domain.Task) error { c.ListID = ptr("l-other-victim-list"); return nil }
	if _, err := s.ModifyTask(ctx, "attacker", "t-legacy", store.Notify{}, moveElsewhere); !errors.Is(err, store.ErrRefNotFound) {
		t.Errorf("moving to another unowned list = %v, want ErrRefNotFound", err)
	}
}
