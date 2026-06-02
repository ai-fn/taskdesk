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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeAuthClient struct {
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

// Stubs for the rest of the interface.
func (f *fakeAuthClient) SignUp(ctx context.Context, in *authv1.SignUpRequest, opts ...grpc.CallOption) (*authv1.SignUpResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.LoginResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RefreshToken(ctx context.Context, in *authv1.RefreshTokenRequest, opts ...grpc.CallOption) (*authv1.RefreshTokenResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) Logout(ctx context.Context, in *authv1.LogoutRequest, opts ...grpc.CallOption) (*authv1.LogoutResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) GetJWKS(ctx context.Context, in *authv1.GetJWKSRequest, opts ...grpc.CallOption) (*authv1.GetJWKSResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) ValidateToken(ctx context.Context, in *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) OAuthSignIn(ctx context.Context, in *authv1.OAuthSignInRequest, opts ...grpc.CallOption) (*authv1.OAuthSignInResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) ListSessions(ctx context.Context, in *authv1.ListSessionsRequest, opts ...grpc.CallOption) (*authv1.ListSessionsResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RevokeSession(ctx context.Context, in *authv1.RevokeSessionRequest, opts ...grpc.CallOption) (*authv1.RevokeSessionResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RevokeOtherSessions(ctx context.Context, in *authv1.RevokeOtherSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RequestPasswordReset(ctx context.Context, in *authv1.RequestPasswordResetRequest, opts ...grpc.CallOption) (*authv1.RequestPasswordResetResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) ResetPassword(ctx context.Context, in *authv1.ResetPasswordRequest, opts ...grpc.CallOption) (*authv1.ResetPasswordResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RequestEmailVerification(ctx context.Context, in *authv1.RequestEmailVerificationRequest, opts ...grpc.CallOption) (*authv1.RequestEmailVerificationResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) ConfirmEmail(ctx context.Context, in *authv1.ConfirmEmailRequest, opts ...grpc.CallOption) (*authv1.ConfirmEmailResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) UpdateUserLanguage(ctx context.Context, in *authv1.UpdateUserLanguageRequest, opts ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) RedeemPromoCode(ctx context.Context, in *authv1.RedeemPromoCodeRequest, opts ...grpc.CallOption) (*authv1.RedeemPromoCodeResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) GetSubscription(ctx context.Context, in *authv1.GetSubscriptionRequest, opts ...grpc.CallOption) (*authv1.GetSubscriptionResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) CreateStripeCheckoutSession(ctx context.Context, in *authv1.CreateStripeCheckoutSessionRequest, opts ...grpc.CallOption) (*authv1.CreateStripeCheckoutSessionResponse, error) {
	panic("not used")
}
func (f *fakeAuthClient) HandleStripeWebhook(ctx context.Context, in *authv1.HandleStripeWebhookRequest, opts ...grpc.CallOption) (*authv1.HandleStripeWebhookResponse, error) {
	panic("not used")
}

type fakeTodoClient struct {
	getTask         func(ctx context.Context, req *todov1.GetTaskRequest, opts ...grpc.CallOption) (*todov1.GetTaskResponse, error)
	listTasks       func(ctx context.Context, req *todov1.ListTasksRequest, opts ...grpc.CallOption) (*todov1.ListTasksResponse, error)
	listDependencies func(ctx context.Context, req *todov1.ListDependenciesRequest, opts ...grpc.CallOption) (*todov1.ListDependenciesResponse, error)
	updateTaskScore func(ctx context.Context, req *todov1.UpdateTaskScoreRequest, opts ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error)
	listUserIDs     func(ctx context.Context, req *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error)
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
func (f *fakeTodoClient) CreateTask(ctx context.Context, in *todov1.CreateTaskRequest, opts ...grpc.CallOption) (*todov1.CreateTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) UpdateTask(ctx context.Context, in *todov1.UpdateTaskRequest, opts ...grpc.CallOption) (*todov1.UpdateTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) DeleteTask(ctx context.Context, in *todov1.DeleteTaskRequest, opts ...grpc.CallOption) (*todov1.DeleteTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) CompleteTask(ctx context.Context, in *todov1.CompleteTaskRequest, opts ...grpc.CallOption) (*todov1.CompleteTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) RestoreTask(ctx context.Context, in *todov1.RestoreTaskRequest, opts ...grpc.CallOption) (*todov1.RestoreTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) PurgeTask(ctx context.Context, in *todov1.PurgeTaskRequest, opts ...grpc.CallOption) (*todov1.PurgeTaskResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) CreateList(ctx context.Context, in *todov1.CreateListRequest, opts ...grpc.CallOption) (*todov1.CreateListResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ListLists(ctx context.Context, in *todov1.ListListsRequest, opts ...grpc.CallOption) (*todov1.ListListsResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) UpdateList(ctx context.Context, in *todov1.UpdateListRequest, opts ...grpc.CallOption) (*todov1.UpdateListResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) DeleteList(ctx context.Context, in *todov1.DeleteListRequest, opts ...grpc.CallOption) (*todov1.DeleteListResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ReorderLists(ctx context.Context, in *todov1.ReorderListsRequest, opts ...grpc.CallOption) (*todov1.ReorderListsResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) CreateGroup(ctx context.Context, in *todov1.CreateGroupRequest, opts ...grpc.CallOption) (*todov1.CreateGroupResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ListGroups(ctx context.Context, in *todov1.ListGroupsRequest, opts ...grpc.CallOption) (*todov1.ListGroupsResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) UpdateGroup(ctx context.Context, in *todov1.UpdateGroupRequest, opts ...grpc.CallOption) (*todov1.UpdateGroupResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) DeleteGroup(ctx context.Context, in *todov1.DeleteGroupRequest, opts ...grpc.CallOption) (*todov1.DeleteGroupResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ReorderGroups(ctx context.Context, in *todov1.ReorderGroupsRequest, opts ...grpc.CallOption) (*todov1.ReorderGroupsResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) UpsertTaskEmbedding(ctx context.Context, in *todov1.UpsertTaskEmbeddingRequest, opts ...grpc.CallOption) (*todov1.UpsertTaskEmbeddingResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) DeleteTaskEmbedding(ctx context.Context, in *todov1.DeleteTaskEmbeddingRequest, opts ...grpc.CallOption) (*todov1.DeleteTaskEmbeddingResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) SearchTasks(ctx context.Context, in *todov1.SearchTasksRequest, opts ...grpc.CallOption) (*todov1.SearchTasksResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) AddDependency(ctx context.Context, in *todov1.AddDependencyRequest, opts ...grpc.CallOption) (*todov1.AddDependencyResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) RemoveDependency(ctx context.Context, in *todov1.RemoveDependencyRequest, opts ...grpc.CallOption) (*todov1.RemoveDependencyResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) GetDependencyTree(ctx context.Context, in *todov1.GetDependencyTreeRequest, opts ...grpc.CallOption) (*todov1.GetDependencyTreeResponse, error) {
	panic("not used")
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

func minimalTask() *todov1.Task {
	return &todov1.Task{
		Id:        "task-1",
		UserId:    "user-1",
		Title:     "Write docs",
		CreatedAt: timestamppb.Now(),
		UpdatedAt: timestamppb.Now(),
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
