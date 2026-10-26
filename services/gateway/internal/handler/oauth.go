package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/oauth"
	"github.com/go-chi/chi/v5"
)

const (
	oauthStateCookie = "oauth_state"
	oauthStateMaxAge = 10 * time.Minute
)

// OAuthHandler runs the Authorization Code flow for any registered provider.
//
//	GET /api/v1/auth/oauth/{provider}            → Start
//	GET /api/v1/auth/oauth/{provider}/callback   → Callback
type OAuthHandler struct {
	client    authv1.AuthServiceClient
	providers oauth.Registry
	log       *slog.Logger

	cookieSecure bool
}

func NewOAuthHandler(c authv1.AuthServiceClient, providers oauth.Registry, log *slog.Logger, cookieSecure bool) *OAuthHandler {
	return &OAuthHandler{client: c, providers: providers, log: log, cookieSecure: cookieSecure}
}

// Start kicks off the OAuth flow: generate a CSRF `state`, stash it in a
// short-lived HttpOnly cookie, and redirect the browser to the provider.
//
// The cookie value is "{provider}.{state}" so the callback can verify both
// the random nonce AND that the user didn't somehow swap providers mid-flow.
func (h *OAuthHandler) Start(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	prov, err := h.providers.Get(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	state, err := randomState()
	if err != nil {
		h.log.Error("oauth: gen state", "err", err)
		redirectLogin(w, r, "state_gen_failed")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    name + "." + state,
		HttpOnly: true,
		Path:     "/api/v1/auth",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode, // Lax: cookie sent on cross-site redirect-back
	})

	http.Redirect(w, r, prov.AuthURL(state), http.StatusFound)
}

// Callback verifies state, exchanges the code, calls auth-service to find
// or create the user, sets the refresh cookie, and redirects to "/".
//
// Any failure short-circuits to /login?oauth_error=<reason> so the SPA can
// surface a friendly toast. Reasons are coarse on purpose — provider-side
// detail is logged server-side, not exposed to the user.
func (h *OAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	prov, err := h.providers.Get(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Provider returned an error (user denied consent etc.)
	if providerErr := r.URL.Query().Get("error"); providerErr != "" {
		h.log.Info("oauth: provider returned error", "provider", name, "err", providerErr)
		redirectLogin(w, r, "provider_denied")
		return
	}

	urlState := r.URL.Query().Get("state")
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value == "" {
		h.log.Warn("oauth: missing state cookie")
		redirectLogin(w, r, "state_mismatch")
		return
	}
	// Clear the state cookie regardless of outcome.
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: "", MaxAge: -1, Path: "/api/v1/auth", HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})

	cookieProvider, cookieState, ok := strings.Cut(cookie.Value, ".")
	if !ok || cookieProvider != name {
		h.log.Warn("oauth: provider mismatch", "cookie", cookieProvider, "url", name)
		redirectLogin(w, r, "state_mismatch")
		return
	}
	// Constant-time compare to defend against timing leaks.
	if subtle.ConstantTimeCompare([]byte(cookieState), []byte(urlState)) != 1 {
		h.log.Warn("oauth: state mismatch")
		redirectLogin(w, r, "state_mismatch")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		redirectLogin(w, r, "missing_code")
		return
	}

	info, err := prov.Exchange(r.Context(), code)
	if err != nil {
		h.log.Error("oauth: exchange", "provider", name, "err", err)
		redirectLogin(w, r, "exchange_failed")
		return
	}

	resp, err := h.client.OAuthSignIn(r.Context(), &authv1.OAuthSignInRequest{
		Provider:       name,
		ProviderUserId: info.ProviderUserID,
		Email:          info.Email,
		Name:           info.Name,
		ClientInfo:     clientInfoFromRequest(r),
	})
	if err != nil {
		h.log.Error("oauth: auth-service signin", "provider", name, "err", err)
		redirectLogin(w, r, "signin_failed")
		return
	}

	setRefreshCookie(w, resp.RefreshToken, h.cookieSecure)
	h.log.Info("oauth: success",
		"provider", name,
		"user_id", resp.UserId,
		"created", resp.Created,
	)

	// Redirect to "/"; the SPA's mount-time refresh picks up the cookie.
	http.Redirect(w, r, "/", http.StatusFound)
}

// randomState returns 32 cryptographically-random bytes, base64url-encoded.
func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// redirectLogin sends the user back to /login with a coarse error code in
// the query string. Browser-friendly, no JSON.
func redirectLogin(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, "/login?oauth_error="+reason, http.StatusFound)
}
