package server_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/server"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown := testutil.StartPostgres()
	testPool = pool
	code := m.Run()
	teardown()
	os.Exit(code)
}

func newServer(t *testing.T) (*server.Server, *store.Store) {
	t.Helper()
	if testPool == nil {
		t.Skip("Docker unavailable")
	}
	testutil.Truncate(t, testPool)
	st := store.New(testPool)
	return server.New(st, slog.Default()), st
}

func seedTasks(t *testing.T, st *store.Store, userID string, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		err := st.CreateTask(context.Background(), &domain.Task{
			ID: fmt.Sprintf("%s-%03d", userID, i), UserID: userID, Title: "t",
			Priority: domain.PriorityMedium, Status: domain.StatusTodo, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}
}

func TestListUnembeddedTasks_RequiresUserID(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.ListUnembeddedTasks(context.Background(), &todov1.ListUnembeddedTasksRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestListUnembeddedTasks_PageSizeAndToken(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 5)
	seedTasks(t, st, "u2", 3)
	ctx := context.Background()

	full, err := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Tasks) != 2 || full.NextPageToken != "u1-001" {
		t.Fatalf("full page: %d tasks, token %q; want 2 tasks, token u1-001", len(full.Tasks), full.NextPageToken)
	}

	var got []string
	token := ""
	for i := 0; i < 5; i++ {
		page, err := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Tasks {
			got = append(got, task.Id)
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	if len(got) != 5 {
		t.Errorf("paged %d tasks, want 5 and no other user's tasks: %v", len(got), got)
	}
	for _, id := range got {
		if id[:2] != "u1" {
			t.Errorf("leaked task %s from another user", id)
		}
	}
}

func TestListUnembeddedTasks_DefaultAndMaxPageSize(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 205)
	ctx := context.Background()

	def, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1"})
	if len(def.Tasks) != 50 {
		t.Errorf("default page = %d, want 50", len(def.Tasks))
	}
	big, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 1000})
	if len(big.Tasks) != 200 {
		t.Errorf("clamped page = %d, want 200", len(big.Tasks))
	}
	short, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 200, PageToken: big.NextPageToken})
	if len(short.Tasks) != 5 || short.NextPageToken != "" {
		t.Errorf("last page = %d tasks, token %q; want 5 and no token", len(short.Tasks), short.NextPageToken)
	}
}

// A total that is an exact multiple of the page size ends with one empty page.
func TestListUnembeddedTasks_ExactMultipleEndsWithEmptyPage(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 4)
	ctx := context.Background()

	p1, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 2})
	p2, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 2, PageToken: p1.NextPageToken})
	if len(p2.Tasks) != 2 || p2.NextPageToken == "" {
		t.Fatalf("second page = %d tasks, token %q; want 2 tasks and a token", len(p2.Tasks), p2.NextPageToken)
	}
	p3, _ := svc.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{UserId: "u1", PageSize: 2, PageToken: p2.NextPageToken})
	if len(p3.Tasks) != 0 || p3.NextPageToken != "" {
		t.Errorf("third page = %d tasks, token %q; want empty and no token", len(p3.Tasks), p3.NextPageToken)
	}
}

func outboxPayload(t *testing.T, eventType string) (email, lang string, n int) {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT payload->>'email', payload->>'language' FROM todo.outbox WHERE event_type=$1`, eventType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e, l *string
		if err := rows.Scan(&e, &l); err != nil {
			t.Fatal(err)
		}
		n++
		email, lang = deref(e), deref(l)
	}
	return email, lang, n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestCompleteTask_StampsRecipientOnEvent(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 1)
	_, err := svc.CompleteTask(context.Background(), &todov1.CompleteTaskRequest{UserId: "u1", TaskId: "u1-000", Language: "zh", Email: "a@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if email, lang, n := outboxPayload(t, "task.completed"); n != 1 || email != "a@x.com" || lang != "zh" {
		t.Errorf("task.completed events=%d email=%q lang=%q", n, email, lang)
	}
}

func TestUpdateTask_CompletedOnlyOnTransitionToDone(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 1)
	ctx := context.Background()
	update := func(req *todov1.UpdateTaskRequest) {
		t.Helper()
		req.UserId, req.TaskId = "u1", "u1-000"
		if _, err := svc.UpdateTask(ctx, req); err != nil {
			t.Fatal(err)
		}
	}

	update(&todov1.UpdateTaskRequest{Status: todov1.Status_STATUS_DONE, Language: "en", Email: "a@x.com"})
	if email, _, n := outboxPayload(t, "task.completed"); n != 1 || email != "a@x.com" {
		t.Fatalf("after completing: task.completed events=%d email=%q, want 1 and a@x.com", n, email)
	}

	// Editing a task that is already done must not re-mail the user.
	update(&todov1.UpdateTaskRequest{Title: "renamed", Language: "en", Email: "a@x.com"})
	update(&todov1.UpdateTaskRequest{Status: todov1.Status_STATUS_DONE, Language: "en", Email: "a@x.com"})
	if _, _, n := outboxPayload(t, "task.completed"); n != 1 {
		t.Errorf("task.completed events=%d after re-edits of a done task, want still 1", n)
	}
	if email, _, n := outboxPayload(t, "task.updated"); n != 2 || email != "" {
		t.Errorf("task.updated events=%d email=%q, want 2 without an email", n, email)
	}

	// Reopening and completing again is a fresh transition.
	update(&todov1.UpdateTaskRequest{Status: todov1.Status_STATUS_TODO})
	update(&todov1.UpdateTaskRequest{Status: todov1.Status_STATUS_DONE, Language: "en", Email: "a@x.com"})
	if _, _, n := outboxPayload(t, "task.completed"); n != 2 {
		t.Errorf("task.completed events=%d after reopen and complete, want 2", n)
	}
}

func TestCompleteTask_AlreadyDoneEmitsNothing(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u1", 1)
	req := &todov1.CompleteTaskRequest{UserId: "u1", TaskId: "u1-000", Language: "en", Email: "a@x.com"}
	for i := 0; i < 2; i++ {
		resp, err := svc.CompleteTask(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Task.Status != todov1.Status_STATUS_DONE {
			t.Fatalf("call %d: status = %v, want DONE", i, resp.Task.Status)
		}
	}
	if _, _, n := outboxPayload(t, "task.completed"); n != 1 {
		t.Errorf("task.completed events=%d after completing twice, want 1", n)
	}
}

func TestCreateTask_ForeignOrMissingReferencesAreNotFound(t *testing.T) {
	svc, st := newServer(t)
	ctx := context.Background()
	victimList, err := svc.CreateList(ctx, &todov1.CreateListRequest{UserId: "victim", Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	seedTasks(t, st, "victim", 1)

	for name, req := range map[string]*todov1.CreateTaskRequest{
		"foreign list":   {UserId: "attacker", Title: "x", ListId: victimList.List.Id},
		"missing list":   {UserId: "attacker", Title: "x", ListId: "nope"},
		"foreign parent": {UserId: "attacker", Title: "x", ParentTaskId: "victim-000"},
	} {
		if _, err := svc.CreateTask(ctx, req); status.Code(err) != codes.NotFound {
			t.Errorf("%s: code = %v, want NotFound (not Internal)", name, status.Code(err))
		}
	}
	if _, err := svc.CreateList(ctx, &todov1.CreateListRequest{UserId: "attacker", Name: "l", GroupId: "nope"}); status.Code(err) != codes.NotFound {
		t.Errorf("list with a missing group: code = %v, want NotFound", status.Code(err))
	}
}

func TestListTasks_TokenFollowsTheEffectivePageSize(t *testing.T) {
	svc, st := newServer(t)
	seedTasks(t, st, "u", 250)
	ctx := context.Background()

	var seen int
	token := ""
	for i := range 5 {
		resp, err := svc.ListTasks(ctx, &todov1.ListTasksRequest{UserId: "u", PageSize: 5000, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		seen += len(resp.Tasks)
		if i == 0 && (len(resp.Tasks) != 200 || resp.NextPageToken == "") {
			t.Fatalf("first page: %d tasks, token %q; an oversized request must clamp to 200 and still page", len(resp.Tasks), resp.NextPageToken)
		}
		if token = resp.NextPageToken; token == "" {
			break
		}
	}
	if seen != 250 {
		t.Errorf("walked %d tasks, want 250", seen)
	}
}

func TestUpdateTask_MaskedFieldsCanBeCleared(t *testing.T) {
	svc, _ := newServer(t)
	ctx := context.Background()
	due := timestamppb.New(time.Now().Add(24 * time.Hour))
	created, err := svc.CreateTask(ctx, &todov1.CreateTaskRequest{UserId: "u", Title: "t", Description: "d", Tags: []string{"a", "b"}, DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.Id

	// Without a mask the empty values mean "unchanged".
	got, err := svc.UpdateTask(ctx, &todov1.UpdateTaskRequest{UserId: "u", TaskId: id, Title: "t2"})
	if err != nil || got.Task.Description != "d" || len(got.Task.Tags) != 2 || got.Task.DueAt == nil {
		t.Fatalf("unmasked update changed fields: %+v, %v", got.GetTask(), err)
	}

	got, err = svc.UpdateTask(ctx, &todov1.UpdateTaskRequest{UserId: "u", TaskId: id, UpdateMask: []string{"description", "tags", "due_at"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Description != "" || len(got.Task.Tags) != 0 || got.Task.DueAt != nil {
		t.Errorf("after clearing: description=%q tags=%v due=%v", got.Task.Description, got.Task.Tags, got.Task.DueAt)
	}
	// ...and it persists.
	again, err := svc.GetTask(ctx, &todov1.GetTaskRequest{UserId: "u", TaskId: id})
	if err != nil || again.Task.Description != "" || len(again.Task.Tags) != 0 || again.Task.DueAt != nil {
		t.Errorf("cleared values did not persist: %+v, %v", again.GetTask(), err)
	}
}
