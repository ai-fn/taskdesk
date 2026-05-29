package handler

import (
	"encoding/json"
	"net/http"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// AccountHandler serves the /api/v1/account routes.
type AccountHandler struct {
	client authv1.AuthServiceClient
}

func NewAccountHandler(c authv1.AuthServiceClient) *AccountHandler {
	return &AccountHandler{client: c}
}

// GET /api/v1/account/me — returns the current user's profile incl. language.
func (h *AccountHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	resp, err := h.client.GetUserByID(r.Context(), &authv1.GetUserByIDRequest{UserId: userID})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	jsonOK(w, map[string]string{
		"user_id":  resp.User.UserId,
		"email":    resp.User.Email,
		"name":     resp.User.Name,
		"language": resp.User.Language,
	})
}

// PATCH /api/v1/account/language
func (h *AccountHandler) UpdateLanguage(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Language == "" {
		jsonError(w, "language is required", http.StatusBadRequest)
		return
	}

	_, err := h.client.UpdateUserLanguage(r.Context(), &authv1.UpdateUserLanguageRequest{
		UserId:   userID,
		Language: body.Language,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
