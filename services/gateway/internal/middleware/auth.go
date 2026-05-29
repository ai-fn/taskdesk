// Package middleware provides HTTP middleware for the gateway.
package middleware

import (
	"context"
	"net/http"
	"strings"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	ctxUserID    contextKey = "user_id"
	ctxEmail     contextKey = "email"
	ctxSessionID contextKey = "session_id"
	ctxLanguage  contextKey = "language"
)

// Authenticate validates the Bearer token from the Authorization header
// or the ?token= query param (used by WebSocket clients which cannot set headers).
// On success it stores user claims in the request context; on failure returns 401.
func Authenticate(authClient authv1.AuthServiceClient) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				http.Error(w, "missing Authorization header", http.StatusUnauthorized)
				return
			}

			resp, err := authClient.ValidateToken(r.Context(), &authv1.ValidateTokenRequest{
				AccessToken: token,
			})
			if err != nil {
				if st, ok := status.FromError(err); ok && st.Code() == codes.Unauthenticated {
					http.Error(w, "invalid or expired token", http.StatusUnauthorized)
					return
				}
				http.Error(w, "auth service unavailable", http.StatusServiceUnavailable)
				return
			}

			ctx := context.WithValue(r.Context(), ctxUserID, resp.UserId)
			ctx = context.WithValue(ctx, ctxEmail, resp.Email)
			ctx = context.WithValue(ctx, ctxSessionID, resp.SessionId)
			ctx = context.WithValue(ctx, ctxLanguage, resp.Language)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext returns the authenticated user's ID.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// EmailFromContext returns the authenticated user's email.
func EmailFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxEmail).(string)
	return v
}

// SessionIDFromContext returns the authenticated user's current session ID.
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxSessionID).(string)
	return v
}

// LanguageFromContext returns the authenticated user's preferred language ("en" if absent).
func LanguageFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxLanguage).(string)
	if v == "" {
		return "en"
	}
	return v
}

func extractBearer(r *http.Request) string {
	// Prefer Authorization header; fall back to ?token= for WebSocket upgrades.
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}
