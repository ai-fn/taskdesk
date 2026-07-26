package server

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	ua "github.com/mileusna/useragent"
)

func TestHashToken(t *testing.T) {
	const input = "my-refresh-token"

	got := hashToken(input)

	// Recompute expected value.
	h := sha256.Sum256([]byte(input))
	want := hex.EncodeToString(h[:])

	if got != want {
		t.Errorf("hashToken(%q) = %q, want %q", input, got, want)
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	tok := "some-token"
	first := hashToken(tok)
	second := hashToken(tok)
	if first != second {
		t.Error("hashToken is not deterministic")
	}
}

func TestHashToken_DifferentInputs(t *testing.T) {
	if hashToken("a") == hashToken("b") {
		t.Error("hashToken produced same hash for different inputs")
	}
}

func TestDeviceLabel(t *testing.T) {
	cases := []struct {
		uaStr string
		want  string
	}{
		// Mobile
		{
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
			"Mobile",
		},
		// Desktop
		{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
			"Desktop",
		},
		// Bot (Googlebot)
		{
			"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			"Bot",
		},
		// Unknown / unrecognised
		{
			"",
			"Unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.uaStr[:min(len(tc.uaStr), 30)], func(t *testing.T) {
			parsed := ua.Parse(tc.uaStr)
			got := deviceLabel(parsed)
			if got != tc.want {
				t.Errorf("deviceLabel(%q) = %q, want %q (Desktop=%v Mobile=%v Tablet=%v Bot=%v)",
					tc.uaStr, got, tc.want, parsed.Desktop, parsed.Mobile, parsed.Tablet, parsed.Bot)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
