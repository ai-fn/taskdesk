package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestModifyTask_StampsRecipientOnCompletedEvent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, "t1", "u1")
	done := func(c *domain.Task) error { c.Status = domain.StatusDone; c.UpdatedAt = time.Now().UTC(); return nil }
	if _, err := s.ModifyTask(ctx, "u1", "t1", store.Notify{Language: "zh", Email: "u@x.com"}, done); err != nil {
		t.Fatal(err)
	}

	var payload []byte
	if err := testPool.QueryRow(ctx, `SELECT payload FROM todo.outbox WHERE event_type='task.completed'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ev struct{ Email, Language string }
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Email != "u@x.com" || ev.Language != "zh" {
		t.Errorf("outbox payload = %s, want email u@x.com and language zh", payload)
	}
}

// unit returns a vector pointing along axis i.
func unit(i int) []float32 {
	out := make([]float32, 1024)
	out[i] = 1
	return out
}

func TestSearchTasksByEmbedding_RanksByCosineAndIsolatesUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, id := range []string{"near", "far"} {
		mustCreateTask(t, s, id, "u1")
	}
	mustCreateTask(t, s, "theirs", "u2")
	if err := s.UpsertTaskEmbedding(ctx, "near", "u1", unit(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTaskEmbedding(ctx, "far", "u1", unit(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTaskEmbedding(ctx, "theirs", "u2", unit(0)); err != nil {
		t.Fatal(err)
	}

	// Goes through the iterative-scan transaction path: fails if the pgvector image rejects the setting
	// with anything but "unknown parameter".
	got, err := s.SearchTasksByEmbedding(ctx, "u1", unit(0), 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"near", "far"}; fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Errorf("results = %v, want %v (nearest first, other user's task excluded)", ids(got), want)
	}
}

func TestIsUnknownSetting(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "42602"}, true},
		{&pgconn.PgError{Code: "42704"}, true},
		{fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "42602"}), true},
		{&pgconn.PgError{Code: "57014"}, false},
		{nil, false},
	} {
		if got := store.IsUnknownSetting(tc.err); got != tc.want {
			t.Errorf("IsUnknownSetting(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
