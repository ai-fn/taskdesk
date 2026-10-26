// Package handler contains HTTP handlers for each service domain.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const refreshCookieName = "refresh_token"

// AuthHandler bridges HTTP ↔ auth-service gRPC.
type AuthHandler struct {
	client            authv1.AuthServiceClient
	todoClient        todov1.TodoServiceClient
	log               *slog.Logger
	emailLimiter      *RateLimiter // 5 attempts / minute per email (login/signup)
	ipLimiter         *RateLimiter // 20 attempts / minute per IP (login/signup)
	resetEmailLimiter *RateLimiter // 3 attempts / 10 min per email (password reset)
	resetIPLimiter    *RateLimiter // 10 attempts / 10 min per IP (password reset)
	// Separate from resetEmailLimiter so requesting a code doesn't spend the guessing budget.
	resetConfirmLimiter *RateLimiter // 10 attempts / 10 min per email (reset confirm)
}

func NewAuthHandler(c authv1.AuthServiceClient, todo todov1.TodoServiceClient, log *slog.Logger) *AuthHandler {
	return &AuthHandler{
		client:     c,
		todoClient: todo,
		log:        log,
		// 5 logins per minute per email — targets credential stuffing.
		emailLimiter: NewRateLimiter(rate.Limit(5.0/60.0), 5, 15*time.Minute, 10*time.Minute),
		// 20 logins per minute per IP — targets origin scanning.
		ipLimiter: NewRateLimiter(rate.Limit(20.0/60.0), 20, 15*time.Minute, 10*time.Minute),
		// 3 reset attempts per 10 min per email — tighter, reset is expensive.
		resetEmailLimiter: NewRateLimiter(rate.Limit(3.0/600.0), 3, 30*time.Minute, 15*time.Minute),
		// 10 reset attempts per 10 min per IP.
		resetIPLimiter: NewRateLimiter(rate.Limit(10.0/600.0), 10, 30*time.Minute, 15*time.Minute),
		// 10 guesses per 10 min per email: about three codes' worth of OTP attempts.
		resetConfirmLimiter: NewRateLimiter(rate.Limit(10.0/600.0), 10, 30*time.Minute, 15*time.Minute),
	}
}

// Stop shuts down the background cleanup goroutines started by the rate limiters.
// Call it after the HTTP server has stopped accepting requests.
func (h *AuthHandler) Stop() {
	h.emailLimiter.Stop()
	h.ipLimiter.Stop()
	h.resetEmailLimiter.Stop()
	h.resetIPLimiter.Stop()
	h.resetConfirmLimiter.Stop()
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

	// Rate-limit before touching the database. clientInfoFromRequest is called
	// once and reused for both the limiter check and the gRPC request.
	info := clientInfoFromRequest(r)
	if !h.ipLimiter.Allow(info.IpAddress) {
		h.log.Warn("signup rate limited by IP", "ip", info.IpAddress)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if !h.emailLimiter.Allow(req.Email) {
		h.log.Warn("signup rate limited by email", "email", req.Email)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	resp, err := h.client.SignUp(r.Context(), &authv1.SignUpRequest{
		Email:      req.Email,
		Password:   req.Password,
		Name:       req.Name,
		ClientInfo: info,
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

	// Check IP first (no normalization), then email (tighter limit).
	// Both checks happen before bcrypt — which is intentionally slow — so a
	// blocked attacker never reaches the expensive path.
	info := clientInfoFromRequest(r)
	if !h.ipLimiter.Allow(info.IpAddress) {
		h.log.Warn("login rate limited by IP", "ip", info.IpAddress)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if !h.emailLimiter.Allow(req.Email) {
		h.log.Warn("login rate limited by email", "email", req.Email)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	resp, err := h.client.Login(r.Context(), &authv1.LoginRequest{
		Email:      req.Email,
		Password:   req.Password,
		ClientInfo: info,
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

// POST /api/v1/auth/password/forgot
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	info := clientInfoFromRequest(r)
	if !h.resetIPLimiter.Allow(info.IpAddress) {
		h.log.Warn("forgot password rate limited by IP", "ip", info.IpAddress)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if req.Email != "" && !h.resetEmailLimiter.Allow(req.Email) {
		h.log.Warn("forgot password rate limited by email", "email", req.Email)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// Always returns OK — no email enumeration.
	_, err := h.client.RequestPasswordReset(r.Context(), &authv1.RequestPasswordResetRequest{
		Email: req.Email,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/password/reset
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	info := clientInfoFromRequest(r)
	if !h.resetIPLimiter.Allow(info.IpAddress) {
		h.log.Warn("reset password rate limited by IP", "ip", info.IpAddress)
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	// Rotating IPs must not lift the per-account cap on code guesses.
	if req.Email != "" && !h.resetConfirmLimiter.Allow(strings.ToLower(strings.TrimSpace(req.Email))) {
		h.log.Warn("reset password rate limited by email")
		jsonError(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	_, err := h.client.ResetPassword(r.Context(), &authv1.ResetPasswordRequest{
		Email:       req.Email,
		Code:        req.Code,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
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
	case codes.PermissionDenied:
		jsonError(w, st.Message(), http.StatusForbidden)
	case codes.FailedPrecondition:
		jsonError(w, st.Message(), http.StatusUnprocessableEntity)
	case codes.ResourceExhausted:
		jsonError(w, st.Message(), http.StatusTooManyRequests)
	case codes.Unavailable:
		jsonError(w, "service unavailable", http.StatusServiceUnavailable)
	case codes.Unimplemented:
		jsonError(w, st.Message(), http.StatusNotImplemented)
	default:
		jsonError(w, "internal error", http.StatusInternalServerError)
	}
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // best-effort: client may have disconnected
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
	_, _ = w.Write(b) // best-effort: client may have disconnected
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg}) // best-effort: client may have disconnected
}
