package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

type GoogleProvider struct {
	cfg *oauth2.Config
}

// NewGoogle builds a Google OAuth provider. redirectURI is the full
// gateway callback URL (e.g. http://localhost:8080/api/v1/auth/oauth/google/callback).
func NewGoogle(clientID, clientSecret, redirectURI string) *GoogleProvider {
	return &GoogleProvider{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Endpoint:     google.Endpoint,
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
}

func (g *GoogleProvider) Name() string { return "google" }

func (g *GoogleProvider) AuthURL(state string) string {
	return g.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

func (g *GoogleProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	tok, err := g.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	client := g.cfg.Client(ctx, tok)
	resp, err := client.Get(googleUserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo status %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Sub           string `json:"sub"` // stable user ID
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		GivenName     string `json:"given_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	if raw.Sub == "" || raw.Email == "" {
		return nil, fmt.Errorf("userinfo missing sub/email")
	}
	if !raw.EmailVerified {
		return nil, fmt.Errorf("google email not verified")
	}

	name := raw.Name
	if name == "" {
		name = raw.GivenName
	}
	if name == "" {
		name = raw.Email
	}

	return &UserInfo{
		ProviderUserID: raw.Sub,
		Email:          raw.Email,
		Name:           name,
	}, nil
}
