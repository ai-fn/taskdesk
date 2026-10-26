package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
)

func completed(c *domain.Task) error { c.Status = domain.StatusDone; return nil }

func countEvents(t *testing.T, eventType string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM todo.outbox WHERE event_type = $1`, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestModifyTask_ConcurrentEditsDoNotLoseUpdates(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "t1", "u1")

	slowTitle := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{}, func(c *domain.Task) error {
			close(slowTitle) // holds the row lock while the other edit tries to start
			time.Sleep(300 * time.Millisecond)
			c.Title = "new title"
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-slowTitle
		_, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{}, func(c *domain.Task) error {
			c.Priority = domain.PriorityUrgent
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()

	got, err := s.GetTask(ctx, "u1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" || got.Priority != domain.PriorityUrgent {
		t.Errorf("task = %q / priority %v; both edits must survive", got.Title, got.Priority)
	}
}

func TestModifyTask_NoChangeWritesNothing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "t1", "u1")
	before := countEvents(t, "task.updated")
	if _, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{}, func(*domain.Task) error { return store.ErrNoChange }); err != nil {
		t.Fatal(err)
	}
	if after := countEvents(t, "task.updated"); after != before {
		t.Errorf("ErrNoChange emitted %d event(s)", after-before)
	}
}

func TestModifyTask_CompletionNeedsDependenciesDone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "dep", "u1")
	mustCreateTask(t, s, "t1", "u1")
	if err := s.AddDependency(ctx, "u1", "t1", "dep"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{}, completed); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("complete while blocked = %v, want ErrBlocked", err)
	}
	if _, err := s.ModifyTask(ctx, "u1", "dep", store.Notify{}, completed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{}, completed); err != nil {
		t.Errorf("complete once unblocked: %v", err)
	}
}

func TestModifyTask_LastSubtaskCompletesTheParentWithAnEvent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "p", "u1")
	seed(t, s, "u1", "c1", domain.StatusTodo, "p")
	seed(t, s, "u1", "c2", domain.StatusTodo, "p")

	if _, err := s.ModifyTask(ctx, "u1", "c1", store.Notify{}, completed); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetTask(ctx, "u1", "p"); p.Status == domain.StatusDone {
		t.Fatal("parent completed while a subtask is open")
	}
	if _, err := s.ModifyTask(ctx, "u1", "c2", store.Notify{Email: "u@x.com"}, completed); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetTask(ctx, "u1", "p"); p.Status != domain.StatusDone {
		t.Error("parent not completed after its last subtask")
	}
	if n := countEvents(t, "task.completed"); n != 3 {
		t.Errorf("task.completed events = %d, want 3 (c1, c2, and the parent)", n)
	}
}

func TestModifyTask_ParentWithOpenDependencyIsNotAutoCompleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "dep", "u1")
	mustCreateTask(t, s, "p", "u1")
	seed(t, s, "u1", "c1", domain.StatusTodo, "p")
	if err := s.AddDependency(ctx, "u1", "p", "dep"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModifyTask(ctx, "u1", "c1", store.Notify{}, completed); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetTask(ctx, "u1", "p"); p.Status == domain.StatusDone {
		t.Error("a blocked parent was auto-completed")
	}
}

// Without parent-first locking, two siblings finishing together each see the other still open.
func TestModifyTask_SiblingsCompletingTogetherCompleteTheParent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		testutil.Truncate(t, testPool)
		mustCreateTask(t, s, "p", "u1")
		seed(t, s, "u1", "c1", domain.StatusTodo, "p")
		seed(t, s, "u1", "c2", domain.StatusTodo, "p")

		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, id := range []string{"c1", "c2"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := s.ModifyTask(ctx, "u1", id, store.Notify{}, completed); err != nil {
					t.Error(err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if p, _ := s.GetTask(ctx, "u1", "p"); p.Status != domain.StatusDone {
			t.Fatalf("round %d: parent not completed after both subtasks finished", i)
		}
	}
}

func TestModifyTask_MissingOrDeletedTaskIsNotFound(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "t1", "u1")
	if err := s.DeleteTaskWithEvent(ctx, "u1", "t1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "nope"} {
		if _, err := s.ModifyTask(ctx, "u1", id, store.Notify{}, completed); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ModifyTask(%s) = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := s.ModifyTask(ctx, "someone-else", "t1", store.Notify{}, completed); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign user = %v, want ErrNotFound", err)
	}
}

// One click on the last subtask must produce one email: only the subtask's event carries the recipient.
func TestModifyTask_AutoCompletedParentEventHasNoRecipient(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "p", "u1")
	seed(t, s, "u1", "c1", domain.StatusTodo, "p")
	if _, err := s.ModifyTask(ctx, "u1", "c1", store.Notify{Language: "en", Email: "u@x.com"}, completed); err != nil {
		t.Fatal(err)
	}
	var withEmail, without int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE payload->>'email' <> ''), count(*) FILTER (WHERE COALESCE(payload->>'email','') = '')
		FROM todo.outbox WHERE event_type = 'task.completed'`).Scan(&withEmail, &without); err != nil {
		t.Fatal(err)
	}
	if withEmail != 1 || without != 1 {
		t.Errorf("task.completed events with email = %d, without = %d; want 1 and 1", withEmail, without)
	}
}
