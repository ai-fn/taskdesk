package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

const (
	discordAuthURL     = "https://discord.com/oauth2/authorize"
	discordTokenURL    = "https://discord.com/api/oauth2/token"
	discordUserInfoURL = "https://discord.com/api/users/@me"
)

type DiscordProvider struct {
	cfg *oauth2.Config
}

func NewDiscord(clientID, clientSecret, redirectURI string) *DiscordProvider {
	return &DiscordProvider{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Endpoint: oauth2.Endpoint{
				AuthURL:  discordAuthURL,
				TokenURL: discordTokenURL,
			},
			Scopes: []string{"identify", "email"},
		},
	}
}

func (d *DiscordProvider) Name() string { return "discord" }

func (d *DiscordProvider) AuthURL(state string) string {
	return d.cfg.AuthCodeURL(state)
}

func (d *DiscordProvider) Exchange(ctx context.Context, code string) (*UserInfo, error) {
	tok, err := d.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	client := d.cfg.Client(ctx, tok)
	resp, err := client.Get(discordUserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo status %d: %s", resp.StatusCode, string(body))
	}

	// Discord's /users/@me payload (relevant fields).
	var raw struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		GlobalName    string `json:"global_name"`
		Email         string `json:"email"`
		Verified      bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	if raw.ID == "" || raw.Email == "" {
		return nil, fmt.Errorf("userinfo missing id/email")
	}
	if !raw.Verified {
		return nil, fmt.Errorf("discord email not verified")
	}

	name := raw.GlobalName
	if name == "" {
		name = raw.Username
	}
	if name == "" {
		name = raw.Email
	}

	return &UserInfo{
		ProviderUserID: raw.ID,
		Email:          raw.Email,
		Name:           name,
	}, nil
}
