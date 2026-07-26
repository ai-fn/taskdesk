package handler

import (
	"net/http"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SessionsHandler serves the /api/v1/sessions routes.
type SessionsHandler struct {
	client authv1.AuthServiceClient
}

func NewSessionsHandler(c authv1.AuthServiceClient) *SessionsHandler {
	return &SessionsHandler{client: c}
}

// GET /api/v1/sessions
func (h *SessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	currentSID := mw.SessionIDFromContext(r.Context())

	resp, err := h.client.ListSessions(r.Context(), &authv1.ListSessionsRequest{
		UserId:           userID,
		CurrentSessionId: currentSID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// DELETE /api/v1/sessions/{sessionID}
func (h *SessionsHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	sessionID := chi.URLParam(r, "sessionID")

	_, err := h.client.RevokeSession(r.Context(), &authv1.RevokeSessionRequest{
		UserId:    userID,
		SessionId: sessionID,
	})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.NotFound {
			jsonError(w, "session not found", http.StatusNotFound)
			return
		}
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/sessions/revoke-others
func (h *SessionsHandler) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	currentSID := mw.SessionIDFromContext(r.Context())

	resp, err := h.client.RevokeOtherSessions(r.Context(), &authv1.RevokeOtherSessionsRequest{
		UserId:           userID,
		CurrentSessionId: currentSID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}
