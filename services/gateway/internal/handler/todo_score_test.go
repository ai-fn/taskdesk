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
// Only methods exercised by these tests need non-nil func fields; others panic.
type fakeTodoClient struct {
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

// Stubs for unused interface methods.
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
func (f *fakeTodoClient) UpdateTaskScore(ctx context.Context, in *todov1.UpdateTaskScoreRequest, opts ...grpc.CallOption) (*todov1.UpdateTaskScoreResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) AddDependency(ctx context.Context, in *todov1.AddDependencyRequest, opts ...grpc.CallOption) (*todov1.AddDependencyResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) RemoveDependency(ctx context.Context, in *todov1.RemoveDependencyRequest, opts ...grpc.CallOption) (*todov1.RemoveDependencyResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ListDependencies(ctx context.Context, in *todov1.ListDependenciesRequest, opts ...grpc.CallOption) (*todov1.ListDependenciesResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) GetDependencyTree(ctx context.Context, in *todov1.GetDependencyTreeRequest, opts ...grpc.CallOption) (*todov1.GetDependencyTreeResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) CompleteMultipartUpload(ctx context.Context, in *todov1.CompleteMultipartUploadRequest, opts ...grpc.CallOption) (*todov1.CompleteMultipartUploadResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) CreateAttachmentUploadURL(ctx context.Context, in *todov1.CreateAttachmentUploadURLRequest, opts ...grpc.CallOption) (*todov1.CreateAttachmentUploadURLResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ConfirmAttachment(ctx context.Context, in *todov1.ConfirmAttachmentRequest, opts ...grpc.CallOption) (*todov1.ConfirmAttachmentResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) ListAttachments(ctx context.Context, in *todov1.ListAttachmentsRequest, opts ...grpc.CallOption) (*todov1.ListAttachmentsResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) GetAttachmentDownloadURL(ctx context.Context, in *todov1.GetAttachmentDownloadURLRequest, opts ...grpc.CallOption) (*todov1.GetAttachmentDownloadURLResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) DeleteAttachment(ctx context.Context, in *todov1.DeleteAttachmentRequest, opts ...grpc.CallOption) (*todov1.DeleteAttachmentResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) InitiateMultipartUpload(ctx context.Context, in *todov1.InitiateMultipartUploadRequest, opts ...grpc.CallOption) (*todov1.InitiateMultipartUploadResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) GetUploadPartURL(ctx context.Context, in *todov1.GetUploadPartURLRequest, opts ...grpc.CallOption) (*todov1.GetUploadPartURLResponse, error) {
	panic("not used")
}
func (f *fakeTodoClient) UpdateScanStatus(ctx context.Context, in *todov1.UpdateScanStatusRequest, opts ...grpc.CallOption) (*todov1.UpdateScanStatusResponse, error) {
	panic("not used")
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
