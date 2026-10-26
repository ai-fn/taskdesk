// Package middleware provides HTTP middleware for the gateway.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/fsociety/todo/pkg/tz"
	"github.com/fsociety/todo/services/gateway/internal/auth"
)

type contextKey string

const (
	ctxUserID           contextKey = "user_id"
	ctxEmail            contextKey = "email"
	ctxEmailVerified    contextKey = "email_verified"
	ctxSessionID        contextKey = "session_id"
	ctxLanguage         contextKey = "language"
	ctxTimeZone         contextKey = "time_zone"
	ctxSubscriptionPlan contextKey = "subscription_plan"
)

// Authenticate validates the Bearer token from the Authorization header.
// On success it stores user claims in the request context; on failure returns 401.
//
// Verification is local (cached signing key) and the user profile comes from a
// short-TTL cache — see internal/auth.Verifier. gRPC to auth-service happens
// only on a cache miss. A miss that can't reach auth-service yields 503; an
// invalid/expired token yields 401.
func Authenticate(verifier *auth.Verifier) func(http.Handler) http.Handler {
	return authenticate(verifier, false)
}

// AuthenticateWS is Authenticate for the WebSocket upgrade, whose browser API cannot set
// headers: it also takes ?token=. Keep it to that route, as query strings end up in access logs.
func AuthenticateWS(verifier *auth.Verifier) func(http.Handler) http.Handler {
	return authenticate(verifier, true)
}

func authenticate(verifier *auth.Verifier, allowQueryToken bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r, allowQueryToken)
			if token == "" {
				http.Error(w, "missing Authorization header", http.StatusUnauthorized)
				return
			}

			id, err := verifier.Verify(r.Context(), token)
			if err != nil {
				var unavail *auth.UnavailableError
				if errors.As(err, &unavail) {
					http.Error(w, "auth service unavailable", http.StatusServiceUnavailable)
					return
				}
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}

			plan := id.Plan
			if plan == "" {
				plan = "free"
			}
			ctx := context.WithValue(r.Context(), ctxUserID, id.UserID)
			ctx = context.WithValue(ctx, ctxEmail, id.Email)
			ctx = context.WithValue(ctx, ctxEmailVerified, id.EmailVerified)
			ctx = context.WithValue(ctx, ctxSessionID, id.SessionID)
			ctx = context.WithValue(ctx, ctxLanguage, id.Language)
			ctx = context.WithValue(ctx, ctxTimeZone, id.TimeZone)
			ctx = context.WithValue(ctx, ctxSubscriptionPlan, plan)
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

// NotifyEmailFromContext returns the address to email the user at, or "" while it is
// unverified: signup accepts any address, so mailing it would reach a third party.
func NotifyEmailFromContext(ctx context.Context) string {
	if v, _ := ctx.Value(ctxEmailVerified).(bool); v {
		return EmailFromContext(ctx)
	}
	return ""
}

// SessionIDFromContext returns the authenticated user's current session ID.
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxSessionID).(string)
	return v
}

// TimeZoneFromContext returns the authenticated user's IANA time zone ("UTC" if absent or invalid).
func TimeZoneFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxTimeZone).(string)
	return tz.Normalize(v)
}

// LanguageFromContext returns the authenticated user's preferred language ("en" if absent).
func LanguageFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxLanguage).(string)
	if v == "" {
		return "en"
	}
	return v
}

// SubscriptionPlanFromContext returns the authenticated user's plan ("free" | "pro").
func SubscriptionPlanFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxSubscriptionPlan).(string)
	if v == "" {
		return "free"
	}
	return v
}

// RequirePlan returns a middleware that rejects requests from users whose plan
// does not match the required plan. Must be used after Authenticate.
func RequirePlan(plan string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if SubscriptionPlanFromContext(r.Context()) != plan {
				http.Error(w, "pro subscription required", http.StatusPaymentRequired)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearer(r *http.Request, allowQuery bool) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if allowQuery {
		return r.URL.Query().Get("token")
	}
	return ""
}
