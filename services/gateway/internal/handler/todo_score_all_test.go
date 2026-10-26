package handler_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// scoredTodo answers every task-returning RPC with a scored task.
type scoredTodo struct{ fakeTodoClient }

func (scoredTodo) CreateTask(context.Context, *todov1.CreateTaskRequest, ...grpc.CallOption) (*todov1.CreateTaskResponse, error) {
	return &todov1.CreateTaskResponse{Task: sampleTask("t")}, nil
}
func (scoredTodo) UpdateTask(context.Context, *todov1.UpdateTaskRequest, ...grpc.CallOption) (*todov1.UpdateTaskResponse, error) {
	return &todov1.UpdateTaskResponse{Task: sampleTask("t")}, nil
}
func (scoredTodo) CompleteTask(context.Context, *todov1.CompleteTaskRequest, ...grpc.CallOption) (*todov1.CompleteTaskResponse, error) {
	return &todov1.CompleteTaskResponse{Task: sampleTask("t")}, nil
}
func (scoredTodo) RestoreTask(context.Context, *todov1.RestoreTaskRequest, ...grpc.CallOption) (*todov1.RestoreTaskResponse, error) {
	return &todov1.RestoreTaskResponse{Task: sampleTask("t")}, nil
}
func (scoredTodo) AddDependency(context.Context, *todov1.AddDependencyRequest, ...grpc.CallOption) (*todov1.AddDependencyResponse, error) {
	return &todov1.AddDependencyResponse{Task: sampleTask("t")}, nil
}
func (scoredTodo) ListDependencies(context.Context, *todov1.ListDependenciesRequest, ...grpc.CallOption) (*todov1.ListDependenciesResponse, error) {
	return &todov1.ListDependenciesResponse{DependsOn: []*todov1.Task{sampleTask("a")}, Blocking: []*todov1.Task{sampleTask("b")}}, nil
}
func (scoredTodo) GetDependencyTree(context.Context, *todov1.GetDependencyTreeRequest, ...grpc.CallOption) (*todov1.GetDependencyTreeResponse, error) {
	node := &todov1.DependencyTreeNode{Task: sampleTask("n")}
	return &todov1.GetDependencyTreeResponse{Root: sampleTask("r"), DependsOn: []*todov1.DependencyTreeNode{node}, Blocking: []*todov1.DependencyTreeNode{node}}, nil
}

func TestEveryTaskResponseStripsCompletionScoreForFreeUsers(t *testing.T) {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(validTokenWithPlan("free"))))
	h := handler.NewTodoHandler(&scoredTodo{})
	r.Post("/api/v1/tasks", h.Create)
	r.Patch("/api/v1/tasks/{taskID}", h.Update)
	r.Post("/api/v1/tasks/{taskID}/complete", h.Complete)
	r.Post("/api/v1/tasks/{taskID}/restore", h.Restore)
	r.Post("/api/v1/tasks/{taskID}/dependencies", h.AddDependency)
	r.Get("/api/v1/tasks/{taskID}/dependencies", h.ListDependencies)
	r.Get("/api/v1/tasks/{taskID}/dependency-tree", h.GetDependencyTree)

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/tasks", `{"title":"t"}`},
		{"PATCH", "/api/v1/tasks/t", `{"title":"t"}`},
		{"POST", "/api/v1/tasks/t/complete", ``},
		{"POST", "/api/v1/tasks/t/restore", ``},
		{"POST", "/api/v1/tasks/t/dependencies", `{"depends_on_id":"x"}`},
		{"GET", "/api/v1/tasks/t/dependencies", ``},
		{"GET", "/api/v1/tasks/t/dependency-tree", ``},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Authorization", u1Token())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code >= 300 {
			t.Errorf("%s %s: status %d: %s", tc.method, tc.path, w.Code, w.Body)
			continue
		}
		if body := w.Body.String(); strings.Contains(body, "High motivation") || strings.Contains(body, `"completion_score":85`) {
			t.Errorf("%s %s leaks the score to a free user: %s", tc.method, tc.path, body)
		}
	}
}
