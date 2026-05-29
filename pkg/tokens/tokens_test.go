package tokens_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tokens"
)

func newMaker(t *testing.T) *tokens.Maker {
	t.Helper()
	pem, err := tokens.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	m, err := tokens.NewMakerFromPEM(pem)
	if err != nil {
		t.Fatalf("NewMakerFromPEM: %v", err)
	}
	return m
}

func TestGenerateKeyAndRoundTrip(t *testing.T) {
	pem, err := tokens.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(pem) == 0 {
		t.Fatal("expected non-empty PEM")
	}
	if _, err := tokens.NewMakerFromPEM(pem); err != nil {
		t.Fatalf("NewMakerFromPEM: %v", err)
	}
}

func TestNewMakerFromPEM_Invalid(t *testing.T) {
	cases := []struct {
		name string
		pem  []byte
	}{
		{"empty", []byte{}},
		{"garbage", []byte("not a pem block")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tokens.NewMakerFromPEM(tc.pem); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestCreateAccessToken_Claims(t *testing.T) {
	m := newMaker(t)
	tok, err := m.CreateAccessToken("u1", "a@b.com", "sid1")
	if err != nil {
		t.Fatalf("CreateAccessToken: %v", err)
	}

	claims, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", claims.UserID, "u1")
	}
	if claims.Email != "a@b.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "a@b.com")
	}
	if claims.SessionID != "sid1" {
		t.Errorf("SessionID = %q, want %q", claims.SessionID, "sid1")
	}
	// Claims.UserID has json:"sub" which shadows RegisteredClaims.Subject on parse,
	// so UserID is the canonical way to read the subject claim.
	if claims.UserID != "u1" {
		t.Errorf("UserID (sub) after parse = %q, want %q", claims.UserID, "u1")
	}
}

func TestCreateRefreshToken_Claims(t *testing.T) {
	m := newMaker(t)
	tok, err := m.CreateRefreshToken("u2", "b@c.com", "sid2")
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	claims, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.UserID != "u2" || claims.Email != "b@c.com" || claims.SessionID != "sid2" {
		t.Errorf("unexpected claims: %+v", claims)
	}
}

func TestAccessToken_TTL(t *testing.T) {
	m := newMaker(t)
	before := time.Now()
	tok, _ := m.CreateAccessToken("u", "e@x.com", "s")
	claims, _ := m.Verify(tok)

	ttl := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	// Allow 5 s of slack around the 15 min TTL.
	if ttl < tokens.AccessTokenTTL-5*time.Second || ttl > tokens.AccessTokenTTL+5*time.Second {
		t.Errorf("access TTL = %v, want ~%v", ttl, tokens.AccessTokenTTL)
	}
	_ = before
}

func TestRefreshToken_TTL(t *testing.T) {
	m := newMaker(t)
	tok, _ := m.CreateRefreshToken("u", "e@x.com", "s")
	claims, _ := m.Verify(tok)

	ttl := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if ttl < tokens.RefreshTokenTTL-5*time.Second || ttl > tokens.RefreshTokenTTL+5*time.Second {
		t.Errorf("refresh TTL = %v, want ~%v", ttl, tokens.RefreshTokenTTL)
	}
}

func TestVerify_WrongKey(t *testing.T) {
	m1 := newMaker(t)
	m2 := newMaker(t)

	tok, _ := m1.CreateAccessToken("u", "e@x.com", "s")
	if _, err := m2.Verify(tok); err == nil {
		t.Fatal("expected error verifying token signed by a different key")
	}
}

func TestVerify_Garbage(t *testing.T) {
	m := newMaker(t)
	cases := []string{"", "not.a.jwt", "eyJhbGciOiJSUzI1NiJ9.bad.sig"}
	for _, tc := range cases {
		if _, err := m.Verify(tc); err == nil {
			t.Errorf("Verify(%q): expected error, got nil", tc)
		}
	}
}

func TestKID_StableAcrossInstances(t *testing.T) {
	pem, _ := tokens.GenerateKey()
	m1, _ := tokens.NewMakerFromPEM(pem)
	m2, _ := tokens.NewMakerFromPEM(pem)

	if m1.KID() != m2.KID() {
		t.Errorf("KID mismatch: %q vs %q", m1.KID(), m2.KID())
	}
}

func TestKID_DifferentForDifferentKeys(t *testing.T) {
	pem1, _ := tokens.GenerateKey()
	pem2, _ := tokens.GenerateKey()
	m1, _ := tokens.NewMakerFromPEM(pem1)
	m2, _ := tokens.NewMakerFromPEM(pem2)

	if m1.KID() == m2.KID() {
		t.Error("expected different KIDs for different keys")
	}
}

func TestPublicKeyNE_NonEmpty(t *testing.T) {
	m := newMaker(t)
	n := m.PublicKeyN()
	e := m.PublicKeyE()

	if n == "" {
		t.Error("PublicKeyN is empty")
	}
	if e == "" {
		t.Error("PublicKeyE is empty")
	}
	// Should be valid base64url (no padding, no + or /).
	for _, c := range []string{n, e} {
		if strings.ContainsAny(c, "+/=") {
			t.Errorf("not base64url (contains +/=): %q", c)
		}
	}
}
