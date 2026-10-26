package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func authWithZone(zone string) *fakeAuthClient {
	return &fakeAuthClient{
		getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
			return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
				UserId: req.UserId, Email: "u1@test.com", Plan: "pro", Language: "en", TimeZone: zone,
			}}, nil
		},
	}
}

func TestListTasks_DueTodayCarriesUserTimeZone(t *testing.T) {
	for _, tc := range []struct{ profile, want string }{
		{"Asia/Shanghai", "Asia/Shanghai"},
		{"", "UTC"},
		{"Mars/Olympus", "UTC"},
	} {
		var got *todov1.ListTasksRequest
		todo := &fakeTodoClient{listTasks: func(_ context.Context, req *todov1.ListTasksRequest, _ ...grpc.CallOption) (*todov1.ListTasksResponse, error) {
			got = req
			return &todov1.ListTasksResponse{}, nil
		}}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?due_today=true", nil)
		req.Header.Set("Authorization", u1Token())
		w := httptest.NewRecorder()
		buildTodoRouter(authWithZone(tc.profile), todo).ServeHTTP(w, req)
		if w.Code != http.StatusOK || got == nil || !got.DueTodayOnly || got.TimeZone != tc.want {
			t.Errorf("profile %q: status %d, req %+v; want due_today with zone %q", tc.profile, w.Code, got, tc.want)
		}
	}
}

type fakeAIClient struct {
	aiv1.AIServiceClient
	parseTask func(context.Context, *aiv1.ParseTaskRequest, ...grpc.CallOption) (*aiv1.ParseTaskResponse, error)
}

func (f *fakeAIClient) ParseTask(ctx context.Context, in *aiv1.ParseTaskRequest, opts ...grpc.CallOption) (*aiv1.ParseTaskResponse, error) {
	return f.parseTask(ctx, in, opts...)
}

func TestParse_TodayAndZoneAreTheUsers(t *testing.T) {
	var got *aiv1.ParseTaskRequest
	ai := &fakeAIClient{parseTask: func(_ context.Context, in *aiv1.ParseTaskRequest, _ ...grpc.CallOption) (*aiv1.ParseTaskResponse, error) {
		got = in
		return &aiv1.ParseTaskResponse{Task: &aiv1.ParsedTask{Title: "t"}}, nil
	}}
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(authWithZone("Pacific/Kiritimati"))))
	r.Post("/api/v1/tasks/parse", handler.NewParseHandler(ai).Parse)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/parse", bytes.NewBufferString(`{"text":"buy milk"}`))
	req.Header.Set("Authorization", u1Token())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	want := time.Now().In(time.FixedZone("", 14*3600)).Format("2006-01-02")
	if w.Code != http.StatusOK || got == nil || got.TimeZone != "Pacific/Kiritimati" || got.Today != want {
		t.Errorf("status %d, req %+v; want zone Pacific/Kiritimati and today %s", w.Code, got, want)
	}
}

func timeZoneRouter(fake *fakeAuthClient) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(fake)))
	h := handler.NewAccountHandler(fake)
	r.Patch("/api/v1/account/time-zone", h.UpdateTimeZone)
	r.Get("/api/v1/account/me", h.Me)
	return r
}

func TestUpdateTimeZone(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		grpcErr    error
		want       int
		forwarded  bool
	}{
		{"success", `{"time_zone":"Asia/Shanghai"}`, nil, http.StatusNoContent, true},
		{"rejected upstream", `{"time_zone":"Mars/Olympus"}`, status.Error(codes.InvalidArgument, "unknown time zone"), http.StatusBadRequest, true},
		{"missing field", `{}`, nil, http.StatusBadRequest, false},
		{"bad json", `{`, nil, http.StatusBadRequest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var forwarded *authv1.UpdateUserTimeZoneRequest
			fake := &fakeAuthClient{updateUserTimeZone: func(_ context.Context, in *authv1.UpdateUserTimeZoneRequest, _ ...grpc.CallOption) (*authv1.UpdateUserTimeZoneResponse, error) {
				forwarded = in
				return &authv1.UpdateUserTimeZoneResponse{}, tc.grpcErr
			}}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/account/time-zone", bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", u1Token())
			w := httptest.NewRecorder()
			timeZoneRouter(fake).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", w.Code, tc.want, w.Body.String())
			}
			if (forwarded != nil) != tc.forwarded {
				t.Errorf("forwarded = %+v, want forwarded=%v", forwarded, tc.forwarded)
			}
			if forwarded != nil && forwarded.UserId != "u1" {
				t.Errorf("user id = %q, want the token's user", forwarded.UserId)
			}
		})
	}
}

func TestMeReturnsTimeZone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	req.Header.Set("Authorization", u1Token())
	w := httptest.NewRecorder()
	timeZoneRouter(authWithZone("Asia/Shanghai")).ServeHTTP(w, req)
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body["time_zone"] != "Asia/Shanghai" {
		t.Errorf("body = %v, err %v; want time_zone Asia/Shanghai", body, err)
	}
}
