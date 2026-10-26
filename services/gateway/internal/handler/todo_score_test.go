package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeTodoClient implements todov1.TodoServiceClient for tests.
// Only methods exercised by these tests need non-nil func fields; others nil-panic via the embedded interface.
type fakeTodoClient struct {
	todov1.TodoServiceClient
	listTasks func(ctx context.Context, req *todov1.ListTasksRequest, opts ...grpc.CallOption) (*todov1.ListTasksResponse, error)
	getTask   func(ctx context.Context, req *todov1.GetTaskRequest, opts ...grpc.CallOption) (*todov1.GetTaskResponse, error)
}

func (f *fakeTodoClient) ListTasks(ctx context.Context, req *todov1.ListTasksRequest, opts ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
	if f.listTasks == nil {
		panic("fakeTodoClient.ListTasks not configured")
	}
	return f.listTasks(ctx, req, opts...)
}
func (f *fakeTodoClient) GetTask(ctx context.Context, req *todov1.GetTaskRequest, opts ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
	if f.getTask == nil {
		panic("fakeTodoClient.GetTask not configured")
	}
	return f.getTask(ctx, req, opts...)
}

// sampleTask returns a task with completion_score and completion_insight set,
// simulating what todo-service would return for a scored task.
func sampleTask(id string) *todov1.Task {
	return &todov1.Task{
		Id:                id,
		UserId:            "u1",
		Title:             "Write tests",
		CompletionScore:   85,
		CompletionInsight: "High motivation",
		CreatedAt:         timestamppb.Now(),
		UpdatedAt:         timestamppb.Now(),
	}
}

// buildTodoRouter wires the Authenticate middleware + TodoHandler onto a chi router.
func buildTodoRouter(auth *fakeAuthClient, todo *fakeTodoClient) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(auth)))
	h := handler.NewTodoHandler(todo)
	r.Get("/api/v1/tasks", h.List)
	r.Get("/api/v1/tasks/{taskID}", h.Get)
	return r
}

// validTokenWithPlan returns a fakeAuthClient that serves user "u1" with the
// given subscription plan. Identity comes from the minted bearer token (u1Token);
// the plan flows through the Verifier's profile lookup (GetUserByID).
func validTokenWithPlan(plan string) *fakeAuthClient {
	return &fakeAuthClient{
		getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
			return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
				UserId: req.UserId, Email: "u1@test.com", Plan: plan, Language: "en",
			}}, nil
		},
	}
}

// ── List (GET /api/v1/tasks) ──────────────────────────────────────────────────

func TestListTasks_CompletionScore_ProUser(t *testing.T) {
	todo := &fakeTodoClient{
		listTasks: func(_ context.Context, _ *todov1.ListTasksRequest, _ ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
			return &todov1.ListTasksResponse{Tasks: []*todov1.Task{sampleTask("t1")}}, nil
		},
	}
	r := buildTodoRouter(validTokenWithPlan("pro"), todo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Authorization", u1Token())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tasks []struct {
			CompletionScore   int    `json:"completion_score"`
			CompletionInsight string `json:"completion_insight"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(body.Tasks))
	}
	if body.Tasks[0].CompletionScore != 85 {
		t.Errorf("pro user: want completion_score=85, got %d", body.Tasks[0].CompletionScore)
	}
	if body.Tasks[0].CompletionInsight != "High motivation" {
		t.Errorf("pro user: want completion_insight='High motivation', got %q", body.Tasks[0].CompletionInsight)
	}
}

func TestListTasks_CompletionScore_FreeUser(t *testing.T) {
	todo := &fakeTodoClient{
		listTasks: func(_ context.Context, _ *todov1.ListTasksRequest, _ ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
			return &todov1.ListTasksResponse{Tasks: []*todov1.Task{sampleTask("t1")}}, nil
		},
	}
	r := buildTodoRouter(validTokenWithPlan("free"), todo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Authorization", u1Token())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tasks []struct {
			CompletionScore   int    `json:"completion_score"`
			CompletionInsight string `json:"completion_insight"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(body.Tasks))
	}
	if body.Tasks[0].CompletionScore != 0 {
		t.Errorf("free user: want completion_score=0, got %d", body.Tasks[0].CompletionScore)
	}
	if body.Tasks[0].CompletionInsight != "" {
		t.Errorf("free user: want completion_insight='', got %q", body.Tasks[0].CompletionInsight)
	}
}

// ── Get (GET /api/v1/tasks/{taskID}) ─────────────────────────────────────────

func TestGetTask_CompletionScore_ProUser(t *testing.T) {
	todo := &fakeTodoClient{
		getTask: func(_ context.Context, _ *todov1.GetTaskRequest, _ ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
			return &todov1.GetTaskResponse{Task: sampleTask("t1")}, nil
		},
	}
	r := buildTodoRouter(validTokenWithPlan("pro"), todo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/t1", nil)
	req.Header.Set("Authorization", u1Token())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var task struct {
		CompletionScore   int    `json:"completion_score"`
		CompletionInsight string `json:"completion_insight"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.CompletionScore != 85 {
		t.Errorf("pro user: want completion_score=85, got %d", task.CompletionScore)
	}
	if task.CompletionInsight != "High motivation" {
		t.Errorf("pro user: want completion_insight='High motivation', got %q", task.CompletionInsight)
	}
}

func TestGetTask_CompletionScore_FreeUser(t *testing.T) {
	todo := &fakeTodoClient{
		getTask: func(_ context.Context, _ *todov1.GetTaskRequest, _ ...grpc.CallOption) (*todov1.GetTaskResponse, error) {
			return &todov1.GetTaskResponse{Task: sampleTask("t1")}, nil
		},
	}
	r := buildTodoRouter(validTokenWithPlan("free"), todo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/t1", nil)
	req.Header.Set("Authorization", u1Token())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var task struct {
		CompletionScore   int    `json:"completion_score"`
		CompletionInsight string `json:"completion_insight"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.CompletionScore != 0 {
		t.Errorf("free user: want completion_score=0, got %d", task.CompletionScore)
	}
	if task.CompletionInsight != "" {
		t.Errorf("free user: want completion_insight='', got %q", task.CompletionInsight)
	}
}
