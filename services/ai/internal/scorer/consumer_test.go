package scorer

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/fsociety/todo/pkg/events"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/grpc"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

// The embedded interfaces make any RPC a test does not set up nil-panic, and keep new RPCs from breaking this file.
type fakeAuthClient struct {
	authv1.AuthServiceClient
	getUserByID func(ctx context.Context, req *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error)
	listUserIDs func(ctx context.Context, req *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error)
}

func (f *fakeAuthClient) GetUserByID(ctx context.Context, req *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	if f.getUserByID == nil {
		panic("fakeAuthClient.GetUserByID not configured")
	}
	return f.getUserByID(ctx, req, opts...)
}
func (f *fakeAuthClient) ListUserIDs(ctx context.Context, req *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	if f.listUserIDs == nil {
		panic("fakeAuthClient.ListUserIDs not configured")
	}
	return f.listUserIDs(ctx, req, opts...)
}

type fakeTodoClient struct {
	todov1.TodoServiceClient
	getTask          func(ctx context.Context, req *todov1.GetTaskRequest, opts ...grpc.CallOption) (*todov1.GetTaskResponse, error)
	listTasks        func(ctx context.Context, req *todov1.ListTasksRequest, opts ...grpc.CallOption) (*todov1.ListTasksResponse, error)
	listDependencies func(ctx context.Context, req *todov1.ListDependenciesRequest, opts ...grpc.CallOption) (*todov1.ListDependenciesResponse, error)
	updateTaskScore  func(ctx context.Context, req *todov1.UpdateTaskScoreRequest, opts ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error)
}

func (f *fakeTodoClient) GetTask(ctx context.Context, req *todov1.GetTaskRequest, opts ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
	if f.getTask == nil {
		panic("fakeTodoClient.GetTask not configured")
	}
	return f.getTask(ctx, req, opts...)
}
func (f *fakeTodoClient) ListTasks(ctx context.Context, req *todov1.ListTasksRequest, opts ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
	if f.listTasks != nil {
		return f.listTasks(ctx, req, opts...)
	}
	return &todov1.ListTasksResponse{}, nil
}
func (f *fakeTodoClient) ListDependencies(ctx context.Context, req *todov1.ListDependenciesRequest, opts ...grpc.CallOption) (*todov1.ListDependenciesResponse, error) {
	if f.listDependencies != nil {
		return f.listDependencies(ctx, req, opts...)
	}
	return &todov1.ListDependenciesResponse{}, nil
}
func (f *fakeTodoClient) UpdateTaskScore(ctx context.Context, req *todov1.UpdateTaskScoreRequest, opts ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error) {
	if f.updateTaskScore == nil {
		panic("fakeTodoClient.UpdateTaskScore not configured")
	}
	return f.updateTaskScore(ctx, req, opts...)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func userByPlan(plan string) func(context.Context, *authv1.GetUserByIDRequest, ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	return func(_ context.Context, _ *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
		return &authv1.GetUserByIDResponse{
			User: &authv1.UserSummary{Plan: plan},
		}, nil
	}
}

// ── scoreTask plan gating ─────────────────────────────────────────────────────

func TestScoreTask_SkipsFreeUser(t *testing.T) {
	scoreCallCount := 0
	todo := &fakeTodoClient{
		updateTaskScore: func(_ context.Context, _ *todov1.UpdateTaskScoreRequest, _ ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error) {
			scoreCallCount++
			return &todov1.UpdateTaskScoreResponse{}, nil
		},
	}
	auth := &fakeAuthClient{getUserByID: userByPlan("free")}

	// Pass a nil Scorer — if the plan check works, Score() is never called.
	c := NewConsumer(nil, todo, auth, testLogger())
	ev := events.TaskEvent{UserID: "user-1", TaskID: "task-1"}

	if err := c.scoreTask(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scoreCallCount != 0 {
		t.Errorf("UpdateTaskScore should not be called for free users, called %d time(s)", scoreCallCount)
	}
}

func TestScoreTask_SkipsOnAuthError(t *testing.T) {
	scoreCallCount := 0
	todo := &fakeTodoClient{
		updateTaskScore: func(_ context.Context, _ *todov1.UpdateTaskScoreRequest, _ ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error) {
			scoreCallCount++
			return &todov1.UpdateTaskScoreResponse{}, nil
		},
	}
	auth := &fakeAuthClient{
		getUserByID: func(_ context.Context, _ *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
			return nil, errors.New("auth unavailable")
		},
	}

	c := NewConsumer(nil, todo, auth, testLogger())
	ev := events.TaskEvent{UserID: "user-1", TaskID: "task-1"}

	if err := c.scoreTask(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: scoreTask should return nil on auth error, got %v", err)
	}
	if scoreCallCount != 0 {
		t.Errorf("UpdateTaskScore should not be called when auth errors, called %d time(s)", scoreCallCount)
	}
}

func TestScoreTask_ProUserProceedsToScore(t *testing.T) {
	// Pro user → must reach GetTask. We verify this by having GetTask return an
	// error so the function fails after the plan check, confirming it was not
	// short-circuited by the free-user guard.
	auth := &fakeAuthClient{getUserByID: userByPlan("pro")}
	todo := &fakeTodoClient{
		getTask: func(_ context.Context, _ *todov1.GetTaskRequest, _ ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
			return nil, errors.New("downstream error")
		},
	}

	c := NewConsumer(nil, todo, auth, testLogger())
	ev := events.TaskEvent{UserID: "user-1", TaskID: "task-1"}

	err := c.scoreTask(context.Background(), ev)
	if err == nil {
		t.Fatal("expected an error after plan check passed (GetTask should have failed)")
	}
}

// ── Backfill plan filter ───────────────────────────────────────────────────────

func TestBackfill_UsesProPlanFilter(t *testing.T) {
	var capturedFilter string
	auth := &fakeAuthClient{
		listUserIDs: func(_ context.Context, req *authv1.ListUserIDsRequest, _ ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
			capturedFilter = req.PlanFilter
			// Return empty user list so backfill completes without doing real work.
			return &authv1.ListUserIDsResponse{}, nil
		},
	}
	todo := &fakeTodoClient{}

	c := NewConsumer(nil, todo, auth, testLogger())
	c.Backfill(context.Background())

	if capturedFilter != "pro" {
		t.Errorf("Backfill: want PlanFilter='pro', got %q", capturedFilter)
	}
}
