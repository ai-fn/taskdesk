package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// recordingTodo captures the recipient fields the gateway forwards.
type recordingTodo struct {
	todov1.TodoServiceClient
	language, email string
	listReq         *todov1.ListTasksRequest
	updateReq       *todov1.UpdateTaskRequest
}

func (f *recordingTodo) ListTasks(_ context.Context, in *todov1.ListTasksRequest, _ ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
	f.listReq = in
	return &todov1.ListTasksResponse{}, nil
}

func (f *recordingTodo) CompleteTask(_ context.Context, in *todov1.CompleteTaskRequest, _ ...grpc.CallOption) (*todov1.CompleteTaskResponse, error) {
	f.language, f.email = in.Language, in.Email
	return &todov1.CompleteTaskResponse{Task: &todov1.Task{Id: in.TaskId}}, nil
}

func (f *recordingTodo) UpdateTask(_ context.Context, in *todov1.UpdateTaskRequest, _ ...grpc.CallOption) (*todov1.UpdateTaskResponse, error) {
	f.updateReq = in
	f.language, f.email = in.Language, in.Email
	return &todov1.UpdateTaskResponse{Task: &todov1.Task{Id: in.TaskId}}, nil
}

func TestCompleteAndUpdate_ForwardOnlyVerifiedEmail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verified  bool
		wantEmail string
	}{
		{"verified", true, "u1@test.com"},
		{"unverified", false, ""},
	} {
		for _, route := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/tasks/t1/complete", ""},
			{http.MethodPatch, "/api/v1/tasks/t1", `{"status":3}`},
		} {
			t.Run(tc.name+" "+route.method, func(t *testing.T) {
				fa := &fakeAuthClient{
					getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
						return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
							UserId: req.UserId, Email: "u1@test.com", EmailVerified: tc.verified, Language: "zh", Plan: "free",
						}}, nil
					},
				}
				todo := &recordingTodo{}
				h := handler.NewTodoHandler(todo)
				r := chi.NewRouter()
				r.Use(middleware.Authenticate(authVerifier(fa)))
				r.Post("/api/v1/tasks/{taskID}/complete", h.Complete)
				r.Patch("/api/v1/tasks/{taskID}", h.Update)

				req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				req.Header.Set("Authorization", u1Token())
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
				}
				if todo.email != tc.wantEmail || todo.language != "zh" {
					t.Errorf("forwarded email=%q language=%q, want %q and zh", todo.email, todo.language, tc.wantEmail)
				}
			})
		}
	}
}

func TestListTasks_ForwardsAndClampsPageSize(t *testing.T) {
	for query, want := range map[string]int32{
		"":                0, // server default
		"?page_size=25":   25,
		"?page_size=200":  200,
		"?page_size=5000": 200,
		"?page_size=0":    0,
		"?page_size=-3":   0,
		"?page_size=lots": 0,
	} {
		t.Run(query, func(t *testing.T) {
			fa := &fakeAuthClient{getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
				return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{UserId: req.UserId, Plan: "free"}}, nil
			}}
			todo := &recordingTodo{}
			h := handler.NewTodoHandler(todo)
			r := chi.NewRouter()
			r.Use(middleware.Authenticate(authVerifier(fa)))
			r.Get("/api/v1/tasks", h.List)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks"+query, nil)
			req.Header.Set("Authorization", u1Token())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK || todo.listReq == nil {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body)
			}
			if todo.listReq.PageSize != want {
				t.Errorf("forwarded page_size = %d, want %d", todo.listReq.PageSize, want)
			}
		})
	}
}

func TestUpdateTask_PresentKeysApplyEvenWhenEmptyAndAbsentKeysDoNot(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantMask   []string
		wantDue    bool
	}{
		{"clear all three", `{"due_at":null,"description":"","tags":[]}`, []string{"description", "tags", "due_at"}, false},
		{"set a due date", `{"due_at":"2026-12-01T10:00:00Z"}`, []string{"due_at"}, true},
		{"title only touches nothing else", `{"title":"x"}`, nil, false},
		{"flag toggle", `{"flagged":true}`, []string{"flagged"}, false},
		{"description alone", `{"description":"x"}`, []string{"description"}, false},
		{"tags alone", `{"tags":["a"]}`, []string{"tags"}, false},
		{"null description and tags mean absent", `{"description":null,"tags":null}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa := &fakeAuthClient{getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
				return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{UserId: req.UserId, Plan: "free"}}, nil
			}}
			todo := &recordingTodo{}
			h := handler.NewTodoHandler(todo)
			r := chi.NewRouter()
			r.Use(middleware.Authenticate(authVerifier(fa)))
			r.Patch("/api/v1/tasks/{taskID}", h.Update)

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/t1", strings.NewReader(tc.body))
			req.Header.Set("Authorization", u1Token())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK || todo.updateReq == nil {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body)
			}
			got := append([]string(nil), todo.updateReq.UpdateMask...)
			sort.Strings(got)
			want := append([]string(nil), tc.wantMask...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("update_mask = %v, want %v", got, want)
			}
			if (todo.updateReq.DueAt != nil) != tc.wantDue {
				t.Errorf("due_at set = %v, want %v", todo.updateReq.DueAt != nil, tc.wantDue)
			}
		})
	}

	t.Run("malformed due_at is a 400", func(t *testing.T) {
		fa := &fakeAuthClient{getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
			return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{UserId: req.UserId, Plan: "free"}}, nil
		}}
		h := handler.NewTodoHandler(&recordingTodo{})
		r := chi.NewRouter()
		r.Use(middleware.Authenticate(authVerifier(fa)))
		r.Patch("/api/v1/tasks/{taskID}", h.Update)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/t1", strings.NewReader(`{"due_at":"tomorrow"}`))
		req.Header.Set("Authorization", u1Token())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", rec.Code)
		}
	})
}
