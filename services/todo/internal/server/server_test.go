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
