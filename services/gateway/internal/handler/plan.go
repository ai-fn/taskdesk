package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"google.golang.org/protobuf/encoding/protojson"
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

// POST /api/v1/plan/generate — triggers plan generation and streams the result
// as Server-Sent Events so the client gets a heartbeat immediately and the
// plan when Claude finishes (which can take 15-60 s).
//
// Event shapes:
//
//	data: {"status":"generating"}          — sent right away to keep connection alive
//	data: {"status":"done","plan":{...}}   — sent when Claude returns
//	data: {"status":"error","error":"..."}  — sent on failure
func (h *PlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// SSE headers — disable all proxy/CDN buffering so events reach the browser.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Heartbeat: lets the browser know the connection is alive before the
	// Claude call blocks for potentially tens of seconds.
	fmt.Fprintf(w, "data: {\"status\":\"generating\"}\n\n")
	flusher.Flush()

	resp, err := h.client.GeneratePlan(r.Context(), &aiv1.GeneratePlanRequest{UserId: userID})
	if err != nil {
		if r.Context().Err() != nil {
			return // client cancelled — nothing to write
		}
		msg, _ := json.Marshal(map[string]string{"status": "error", "error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
		return
	}

	planJSON, err := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
		UseEnumNumbers:  true,
	}.Marshal(resp.Plan)
	if err != nil {
		fmt.Fprintf(w, "data: {\"status\":\"error\",\"error\":\"marshal failed\"}\n\n")
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: {\"status\":\"done\",\"plan\":%s}\n\n", planJSON)
	flusher.Flush()
}
