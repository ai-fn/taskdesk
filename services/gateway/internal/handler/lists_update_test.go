package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type fakeListClient struct {
	fakeTodoClient
	updateList func(*todov1.UpdateListRequest) (*todov1.UpdateListResponse, error)
}

func (f *fakeListClient) UpdateList(_ context.Context, in *todov1.UpdateListRequest, _ ...grpc.CallOption) (*todov1.UpdateListResponse, error) {
	return f.updateList(in)
}

func TestUpdateList_MaskNamesOnlyTheSentFields(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"name":"x"}`, nil},
		{`{"icon":""}`, []string{"icon"}},
		{`{"name":"x","color":"#fff","group_id":null,"icon":""}`, []string{"icon", "group_id"}},
		{`{"is_hidden":false,"group_id":null}`, []string{"is_hidden", "group_id"}},
	} {
		var got *todov1.UpdateListRequest
		todo := &fakeListClient{updateList: func(in *todov1.UpdateListRequest) (*todov1.UpdateListResponse, error) {
			got = in
			return &todov1.UpdateListResponse{List: &todov1.TaskList{}}, nil
		}}
		r := chi.NewRouter()
		r.Use(middleware.Authenticate(authVerifier(authWithZone("UTC"))))
		r.Patch("/api/v1/lists/{listID}", handler.NewListHandler(todo).Update)

		req := httptest.NewRequest(http.MethodPatch, "/api/v1/lists/l1", bytes.NewBufferString(tc.body))
		req.Header.Set("Authorization", u1Token())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK || got == nil || !slices.Equal(got.UpdateMask, tc.want) {
			t.Errorf("%s: status %d, mask %v, want %v", tc.body, w.Code, got.GetUpdateMask(), tc.want)
		}
	}
}
