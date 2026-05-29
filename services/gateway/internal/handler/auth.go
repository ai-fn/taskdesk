// Package handler contains HTTP handlers for each service domain.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const refreshCookieName = "refresh_token"

// AuthHandler bridges HTTP ↔ auth-service gRPC.
type AuthHandler struct {
	client     authv1.AuthServiceClient
	todoClient todov1.TodoServiceClient
	log        *slog.Logger
}

func NewAuthHandler(c authv1.AuthServiceClient, todo todov1.TodoServiceClient, log *slog.Logger) *AuthHandler {
	return &AuthHandler{client: c, todoClient: todo, log: log}
}

// POST /api/v1/auth/signup
func (h *AuthHandler) SignUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.client.SignUp(r.Context(), &authv1.SignUpRequest{
		Email:      req.Email,
		Password:   req.Password,
		Name:       req.Name,
		ClientInfo: clientInfoFromRequest(r),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	// Seed default "My lists" group — best-effort, don't fail signup if this errors.
	if _, err := h.todoClient.CreateGroup(r.Context(), &todov1.CreateGroupRequest{
		UserId: resp.UserId,
		Name:   "My lists",
	}); err != nil {
		h.log.Error("seed default group", "user_id", resp.UserId, "err", err)
	}

	setRefreshCookie(w, resp.RefreshToken)
	jsonOK(w, map[string]string{
		"user_id":      resp.UserId,
		"access_token": resp.AccessToken,
	})
}

// POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.client.Login(r.Context(), &authv1.LoginRequest{
		Email:      req.Email,
		Password:   req.Password,
		ClientInfo: clientInfoFromRequest(r),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	setRefreshCookie(w, resp.RefreshToken)
	jsonOK(w, map[string]string{
		"user_id":      resp.UserId,
		"access_token": resp.AccessToken,
	})
}

// POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		jsonError(w, "missing refresh_token cookie", http.StatusUnauthorized)
		return
	}

	resp, err := h.client.RefreshToken(r.Context(), &authv1.RefreshTokenRequest{
		RefreshToken: cookie.Value,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	setRefreshCookie(w, resp.RefreshToken)
	jsonOK(w, map[string]string{"access_token": resp.AccessToken})
}

// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var refreshToken string
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		refreshToken = cookie.Value
	}

	_, _ = h.client.Logout(r.Context(), &authv1.LogoutRequest{RefreshToken: refreshToken})

	// Clear the cookie regardless of backend result.
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Path:     "/",
	})
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		HttpOnly: true,
		Path:     "/api/v1/auth",
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
		SameSite: http.SameSiteStrictMode,
		// Secure: true — set this in production (HTTPS only)
	})
}

func handleGRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch st.Code() {
	case codes.InvalidArgument:
		jsonError(w, st.Message(), http.StatusBadRequest)
	case codes.AlreadyExists:
		jsonError(w, st.Message(), http.StatusConflict)
	case codes.Unauthenticated:
		jsonError(w, st.Message(), http.StatusUnauthorized)
	case codes.NotFound:
		jsonError(w, st.Message(), http.StatusNotFound)
	case codes.Unavailable:
		jsonError(w, "service unavailable", http.StatusServiceUnavailable)
	default:
		jsonError(w, "internal error", http.StatusInternalServerError)
	}
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// protoOK serializes a proto message using protojson so that well-known types
// like Timestamp are rendered as RFC-3339 strings instead of {seconds,nanos}.
func protoOK(w http.ResponseWriter, m proto.Message) {
	protoStatus(w, m, http.StatusOK)
}

func protoStatus(w http.ResponseWriter, m proto.Message, code int) {
	b, err := protojson.MarshalOptions{EmitUnpopulated: true, UseProtoNames: true, UseEnumNumbers: true}.Marshal(m)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
