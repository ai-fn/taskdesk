package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	if testPool == nil {
		t.Skip("Docker unavailable")
	}
	testutil.Truncate(t, testPool)
	return store.New(testPool)
}

func mustCreateTask(t *testing.T, s *store.Store, id, userID string) {
	t.Helper()
	now := time.Now().UTC()
	task := &domain.Task{
		ID: id, UserID: userID, Title: "task " + id,
		Priority: domain.PriorityMedium, Status: domain.StatusTodo,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask %s: %v", id, err)
	}
}

func vec(v float32) []float32 {
	out := make([]float32, 1024)
	out[0] = v
	return out
}

func ids(tasks []*domain.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestListUnembeddedTasks_ExcludesEmbeddedDeletedAndOtherUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, id := range []string{"t1", "t2", "t3", "t4"} {
		mustCreateTask(t, s, id, "u1")
	}
	mustCreateTask(t, s, "other", "u2")

	if err := s.UpsertTaskEmbedding(ctx, "t2", "u1", vec(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE todo.tasks SET deleted_at = now() WHERE id = 't3'`); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListUnembeddedTasks(ctx, "u1", "", 50)
	if err != nil {
		t.Fatalf("ListUnembeddedTasks: %v", err)
	}
	if want := []string{"t1", "t4"}; fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Errorf("tasks = %v, want %v", ids(got), want)
	}
}

func TestListUnembeddedTasks_KeysetPagination(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		mustCreateTask(t, s, fmt.Sprintf("p%d", i), "u1")
	}

	var all []string
	after := ""
	for pages := 0; pages < 10; pages++ {
		page, err := s.ListUnembeddedTasks(ctx, "u1", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ids(page)...)
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].ID
	}
	if want := []string{"p1", "p2", "p3", "p4", "p5"}; fmt.Sprint(all) != fmt.Sprint(want) {
		t.Errorf("paged ids = %v, want %v", all, want)
	}
}

func TestListUnembeddedTasks_EmptyOnceEmbedded(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "e1", "u1")
	if err := s.UpsertTaskEmbedding(ctx, "e1", "u1", vec(1)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListUnembeddedTasks(ctx, "u1", "", 50)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no tasks", ids(got), err)
	}
}
