package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// PlanHandler bridges HTTP ↔ ai-service gRPC.
type PlanHandler struct {
	client aiv1.AIServiceClient
}

func NewPlanHandler(c aiv1.AIServiceClient) *PlanHandler {
	return &PlanHandler{client: c}
}

// generateTimeout bounds how long we'll wait for Claude end-to-end. Long enough
// for a slow streaming response, short enough that a hang doesn't pin an SSE
// connection (and a gateway goroutine) open forever.
const generateTimeout = 90 * time.Second

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
// as Server-Sent Events.
//
// Event shapes:
//
//	data: {"type":"text","delta":"..."}    — word-by-word narrative from Claude
//	data: {"type":"done","plan":{...}}     — final structured plan
//	data: {"type":"error","error":"..."}   — on failure
func (h *PlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithTimeout(r.Context(), generateTimeout)
	defer cancel()

	grpcStream, err := h.client.GeneratePlanStream(ctx, &aiv1.GeneratePlanRequest{
		UserId:   userID,
		Language: mw.LanguageFromContext(r.Context()),
		Email:    mw.NotifyEmailFromContext(r.Context()),
	})
	if err != nil {
		writeSSEError(w, flusher, "could not start generation: "+err.Error())
		return
	}

	for {
		chunk, err := grpcStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Client gone — don't bother writing, the connection is dead.
			if errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				writeSSEError(w, flusher, "plan generation timed out")
			} else if st, ok := status.FromError(err); ok && st.Message() != "" {
				writeSSEError(w, flusher, st.Message())
			} else {
				writeSSEError(w, flusher, err.Error())
			}
			return
		}

		switch p := chunk.Payload.(type) {
		case *aiv1.PlanStreamChunk_TextDelta:
			msg, _ := json.Marshal(map[string]string{"type": "text", "delta": p.TextDelta})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				// Client gone — nothing more we can do with this stream.
				return
			}
			flusher.Flush()

		case *aiv1.PlanStreamChunk_Plan:
			planJSON, err := protojson.MarshalOptions{
				EmitUnpopulated: true,
				UseProtoNames:   true,
				UseEnumNumbers:  true,
			}.Marshal(p.Plan)
			if err != nil {
				writeSSEError(w, flusher, "marshal failed")
				return
			}
			if _, err := fmt.Fprintf(w, "data: {\"type\":\"done\",\"plan\":%s}\n\n", planJSON); err != nil {
				return
			}
			flusher.Flush()
			return

		case *aiv1.PlanStreamChunk_Error:
			writeSSEError(w, flusher, p.Error)
			return
		}
	}

	// Stream EOF without ever emitting a terminal chunk — synthesise an error
	// so the frontend never silently loses the user's streamed narrative.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeSSEError(w, flusher, "plan generation timed out")
	} else {
		writeSSEError(w, flusher, "stream ended without a plan")
	}
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	payload, _ := json.Marshal(map[string]string{"type": "error", "error": msg})
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		// Client gone — nothing more we can do with this stream.
		return
	}
	flusher.Flush()
}
