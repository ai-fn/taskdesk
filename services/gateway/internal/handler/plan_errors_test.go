package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingPlanStream struct {
	grpc.ClientStream
	err   error
	chunk *aiv1.PlanStreamChunk
}

func (s *failingPlanStream) Recv() (*aiv1.PlanStreamChunk, error) {
	if c := s.chunk; c != nil {
		s.chunk = nil
		return c, nil
	}
	return nil, s.err
}

type planAI struct {
	aiv1.AIServiceClient
	start, recv error
	chunk       *aiv1.PlanStreamChunk
}

func (p planAI) GeneratePlanStream(context.Context, *aiv1.GeneratePlanRequest, ...grpc.CallOption) (aiv1.AIService_GeneratePlanStreamClient, error) {
	if p.start != nil {
		return nil, p.start
	}
	return &failingPlanStream{err: p.recv, chunk: p.chunk}, nil
}

func TestGeneratePlan_SSEErrorsDoNotLeakUpstreamText(t *testing.T) {
	const secret = "anthropic: 401 invalid x-api-key sk-ant-123"
	for _, tc := range []struct {
		name string
		ai   planAI
		want string
	}{
		{"internal on recv", planAI{recv: status.Error(codes.Internal, "plan generation failed: "+secret)}, "plan generation failed"},
		{"plain error on recv", planAI{recv: errors.New(secret)}, "plan generation failed"},
		{"unavailable on start", planAI{start: status.Error(codes.Unavailable, "dial tcp 10.0.0.7: "+secret)}, "could not start generation"},
		{"error chunk from an older ai pod", planAI{chunk: &aiv1.PlanStreamChunk{Payload: &aiv1.PlanStreamChunk_Error{Error: "anthropic: " + secret}}}, "plan generation failed"},
		{"user-facing message kept", planAI{recv: status.Error(codes.FailedPrecondition, "no tasks to prioritize")}, "no tasks to prioritize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			r.Use(middleware.Authenticate(authVerifier(validTokenWithPlan("pro"))))
			r.Post("/api/v1/plan/generate", handler.NewPlanHandler(tc.ai).Generate)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/plan/generate", nil)
			req.Header.Set("Authorization", u1Token())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			body := w.Body.String()
			if strings.Contains(body, "sk-ant") || strings.Contains(body, "10.0.0.7") {
				t.Errorf("upstream text leaked: %s", body)
			}
			if !strings.Contains(body, `"error":"`+tc.want+`"`) {
				t.Errorf("body = %s, want error %q", body, tc.want)
			}
		})
	}
}
