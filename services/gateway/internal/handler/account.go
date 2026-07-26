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
	jsonOK(w, map[string]any{
		"user_id":        resp.User.UserId,
		"email":          resp.User.Email,
		"name":           resp.User.Name,
		"language":       resp.User.Language,
		"email_verified": resp.User.EmailVerified,
	})
}

// POST /api/v1/account/email/verify/request
func (h *AccountHandler) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	_, err := h.client.RequestEmailVerification(r.Context(), &authv1.RequestEmailVerificationRequest{
		UserId: userID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/account/email/verify/confirm
func (h *AccountHandler) ConfirmEmail(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	_, err := h.client.ConfirmEmail(r.Context(), &authv1.ConfirmEmailRequest{
		UserId: userID,
		Code:   req.Code,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
