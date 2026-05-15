package handler

import (
	"net/http"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// PlanHandler bridges HTTP ↔ ai-service gRPC.
type PlanHandler struct {
	client aiv1.AIServiceClient
}

func NewPlanHandler(c aiv1.AIServiceClient) *PlanHandler {
	return &PlanHandler{client: c}
}

// GET /api/v1/plan — returns the user's latest daily plan.
func (h *PlanHandler) GetLatest(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	resp, err := h.client.GetLatestPlan(r.Context(), &aiv1.GetLatestPlanRequest{UserId: userID})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}
