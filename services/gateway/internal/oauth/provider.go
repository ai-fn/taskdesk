// Package oauth implements provider-agnostic OAuth 2.0 sign-in for the gateway.
//
// Each provider hides its OAuth2 client + userinfo endpoint behind a tiny
// interface so adding a new provider (GitHub, Apple, etc.) is a single new
// file in this package plus a registry entry at startup.
package oauth

import (
	"context"
	"fmt"
)

// UserInfo is the canonical shape returned from any provider's userinfo
// endpoint. It carries exactly what auth-service.OAuthSignIn needs.
type UserInfo struct {
	ProviderUserID string // stable, provider-issued ID (Google: sub, Discord: id)
	Email          string
	Name           string
}

// Provider is the strategy contract every OAuth integration implements.
type Provider interface {
	Name() string                                                 // "google" | "discord"
	AuthURL(state string) string                                  // URL to redirect the browser to
	Exchange(ctx context.Context, code string) (*UserInfo, error) // code → tokens → userinfo
}

// Registry maps provider names to implementations. Populated at startup
// from gateway config. Missing credentials → provider not registered (the
// Get lookup returns nil and the handler 404s gracefully).
type Registry map[string]Provider

// Get returns the registered provider or an error. The caller should map
// the error to a 404 / "provider not configured" user-facing response.
func (r Registry) Get(name string) (Provider, error) {
	if p, ok := r[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("oauth provider %q not configured", name)
}
