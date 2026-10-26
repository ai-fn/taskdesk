package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/server"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/fsociety/todo/services/auth/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	stripewebhook "github.com/stripe/stripe-go/v82/webhook"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── package-level setup ───────────────────────────────────────────────────────

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown := testutil.StartPostgres()
	testPool = pool
	code := m.Run()
	teardown()
	os.Exit(code)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newServer creates a fresh Server with a newly generated RSA key and a clean DB.
// It returns the server and the Maker so tests can decode tokens independently.
func newServer(t *testing.T) (*server.Server, *tokens.Maker) {
	t.Helper()
	testutil.Truncate(t, testPool)

	pem, err := tokens.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	maker, err := tokens.NewMakerFromPEM(pem)
	if err != nil {
		t.Fatalf("NewMakerFromPEM: %v", err)
	}
	return server.New(store.New(testPool), maker, slog.Default(), nil, server.CodeConfig{
		TTL:            10 * time.Minute,
		ResendCooldown: 3 * time.Minute,
		MaxAttempts:    3,
	}, server.StripeConfig{}), maker
}

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	s, ok := status.FromError(err)
	if !ok {
		return codes.Unknown
	}
	return s.Code()
}

func signUp(t *testing.T, svc *server.Server, email, password, name string) *authv1.SignUpResponse {
	t.Helper()
	resp, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
		Email:      email,
		Password:   password,
		Name:       name,
		ClientInfo: &authv1.ClientInfo{IpAddress: "1.2.3.4", UserAgent: "Go-test/1.0"},
	})
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	return resp
}

func sessionID(t *testing.T, maker *tokens.Maker, accessToken string) string {
	t.Helper()
	claims, err := maker.Verify(accessToken)
	if err != nil {
		t.Fatalf("Verify access token: %v", err)
	}
	return claims.SessionID
}

// ── SignUp ────────────────────────────────────────────────────────────────────

func TestSignUp_Success(t *testing.T) {
	svc, _ := newServer(t)
	resp := signUp(t, svc, "a@test.com", "Passw0rd1", "Alice")

	if resp.UserId == "" || resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Errorf("incomplete response: %+v", resp)
	}
}

func TestSignUp_CreatesOneSession(t *testing.T) {
	svc, _ := newServer(t)
	resp := signUp(t, svc, "b@test.com", "Passw0rd1", "Bob")

	list, err := svc.ListSessions(context.Background(), &authv1.ListSessionsRequest{UserId: resp.UserId})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list.Sessions) != 1 {
		t.Errorf("expected 1 session after signup, got %d", len(list.Sessions))
	}
}

func TestSignUp_DuplicateEmail(t *testing.T) {
	svc, _ := newServer(t)
	signUp(t, svc, "dup@test.com", "Passw0rd1", "First")

	_, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
		Email: "dup@test.com", Password: "Passw0rd2", Name: "Second",
	})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("want AlreadyExists, got %v", err)
	}
}

func TestSignUp_MissingFields(t *testing.T) {
	svc, _ := newServer(t)
	cases := []struct{ email, password, name string }{
		{"", "Passw0rd", "Name"},
		{"a@b.com", "", "Name"},
		{"a@b.com", "Passw0rd", ""},
	}
	for _, tc := range cases {
		_, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
			Email: tc.email, Password: tc.password, Name: tc.name,
		})
		if grpcCode(err) != codes.InvalidArgument {
			t.Errorf("SignUp(%q,%q,%q): want InvalidArgument, got %v", tc.email, tc.password, tc.name, err)
		}
	}
}

// ── Login ─────────────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	svc, _ := newServer(t)
	signUp(t, svc, "c@test.com", "Secret12", "Carol")

	resp, err := svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "c@test.com", Password: "Secret12",
		ClientInfo: &authv1.ClientInfo{IpAddress: "5.6.7.8"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("expected access token")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, _ := newServer(t)
	signUp(t, svc, "d@test.com", "Rightpw1", "Dave")

	_, err := svc.Login(context.Background(), &authv1.LoginRequest{Email: "d@test.com", Password: "wrong"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("want Unauthenticated, got %v", err)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.Login(context.Background(), &authv1.LoginRequest{Email: "ghost@test.com", Password: "x"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("want Unauthenticated, got %v", err)
	}
}

func TestLogin_OAuthOnlyUser(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "goog123",
		Email: "oaonly@test.com", Name: "OA",
		ClientInfo: &authv1.ClientInfo{IpAddress: "1.1.1.1"},
	})
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}

	_, err = svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "oaonly@test.com", Password: "anything",
	})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("OAuth-only login: want Unauthenticated, got %v", err)
	}
}

// ── RefreshToken ──────────────────────────────────────────────────────────────

func TestRefreshToken_SameSID(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "e@test.com", "Passw0rd", "Eve")

	origSID := sessionID(t, maker, su.AccessToken)

	resp, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{
		RefreshToken: su.RefreshToken,
	})
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}

	newSID := sessionID(t, maker, resp.AccessToken)
	if newSID != origSID {
		t.Errorf("sid changed after refresh: was %q, now %q", origSID, newSID)
	}
}

func TestRefreshToken_AfterLogout(t *testing.T) {
	// After logout the session row is gone; a refresh with the old token
	// must fail even if the JWT signature is still valid.
	svc, _ := newServer(t)
	su := signUp(t, svc, "f@test.com", "Passw0rd", "Frank")

	if _, err := svc.Logout(context.Background(), &authv1.LogoutRequest{
		RefreshToken: su.RefreshToken,
	}); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	_, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{
		RefreshToken: su.RefreshToken,
	})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("refresh after logout: want Unauthenticated, got %v", err)
	}
}

func TestRefreshToken_GarbageToken(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{
		RefreshToken: "not.a.jwt",
	})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("garbage: want Unauthenticated, got %v", err)
	}
}

// ── ValidateToken ─────────────────────────────────────────────────────────────

func TestValidateToken_Success(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "g@test.com", "Passw0rd", "Grace")

	resp, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{
		AccessToken: su.AccessToken,
	})
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if resp.UserId != su.UserId {
		t.Errorf("UserId = %q, want %q", resp.UserId, su.UserId)
	}
	if resp.Email != "g@test.com" {
		t.Errorf("Email = %q, want g@test.com", resp.Email)
	}
	if resp.SessionId == "" {
		t.Error("expected non-empty SessionId")
	}
}

func TestValidateToken_RejectsRefreshToken(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "r@test.com", "Passw0rd1", "Rita")

	_, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{AccessToken: su.RefreshToken})
	if got := grpcCode(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestValidateToken_BadToken(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{AccessToken: "garbage"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("want Unauthenticated, got %v", err)
	}
}

// ── ValidateSession ───────────────────────────────────────────────────────────

func sessionActive(t *testing.T, svc *server.Server, sid string) bool {
	t.Helper()
	resp, err := svc.ValidateSession(context.Background(), &authv1.ValidateSessionRequest{SessionId: sid})
	if err != nil {
		t.Fatalf("ValidateSession(%q): %v", sid, err)
	}
	return resp.Active
}

func TestValidateSession_LiveThenRevoked(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "s1@test.com", "Passw0rd1", "Sam")
	sid := sessionID(t, maker, su.AccessToken)

	if !sessionActive(t, svc, sid) {
		t.Fatal("fresh session reported inactive")
	}
	if _, err := svc.RevokeSession(context.Background(), &authv1.RevokeSessionRequest{UserId: su.UserId, SessionId: sid}); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if sessionActive(t, svc, sid) {
		t.Error("revoked session still active")
	}
}

func TestValidateSession_InactiveAfterLogout(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "s2@test.com", "Passw0rd1", "Sue")
	sid := sessionID(t, maker, su.AccessToken)

	if _, err := svc.Logout(context.Background(), &authv1.LogoutRequest{RefreshToken: su.RefreshToken}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if sessionActive(t, svc, sid) {
		t.Error("session still active after logout")
	}
}

func TestValidateSession_OtherSessionsRevoked(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "s3@test.com", "Passw0rd1", "Sid")
	keep := sessionID(t, maker, su.AccessToken)
	login, err := svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "s3@test.com", Password: "Passw0rd1",
		ClientInfo: &authv1.ClientInfo{IpAddress: "9.9.9.9"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	other := sessionID(t, maker, login.AccessToken)

	if _, err := svc.RevokeOtherSessions(context.Background(), &authv1.RevokeOtherSessionsRequest{UserId: su.UserId, CurrentSessionId: keep}); err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if !sessionActive(t, svc, keep) || sessionActive(t, svc, other) {
		t.Errorf("keep active=%v other active=%v, want true/false", sessionActive(t, svc, keep), sessionActive(t, svc, other))
	}
}

func TestValidateSession_BadInput(t *testing.T) {
	svc, _ := newServer(t)
	if sessionActive(t, svc, "not-a-uuid") {
		t.Error("non-UUID session reported active")
	}
	_, err := svc.ValidateSession(context.Background(), &authv1.ValidateSessionRequest{})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("empty id: want InvalidArgument, got %v", err)
	}
}

// ── Logout ────────────────────────────────────────────────────────────────────

func TestLogout_RemovesSession(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "h@test.com", "Passw0rd", "Hank")

	if _, err := svc.Logout(context.Background(), &authv1.LogoutRequest{
		RefreshToken: su.RefreshToken,
	}); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	list, _ := svc.ListSessions(context.Background(), &authv1.ListSessionsRequest{UserId: su.UserId})
	if len(list.Sessions) != 0 {
		t.Errorf("expected 0 sessions after logout, got %d", len(list.Sessions))
	}
}

// ── ListSessions ──────────────────────────────────────────────────────────────

func TestListSessions_CurrentFlag(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "i@test.com", "Passw0rd", "Iris")

	sid := sessionID(t, maker, su.AccessToken)
	list, err := svc.ListSessions(context.Background(), &authv1.ListSessionsRequest{
		UserId:           su.UserId,
		CurrentSessionId: sid,
	})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list.Sessions) != 1 || !list.Sessions[0].Current {
		t.Errorf("expected 1 session marked current, got %+v", list.Sessions)
	}
}

func TestListSessions_UAParsesBrowser(t *testing.T) {
	svc, _ := newServer(t)
	resp, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
		Email: "j@test.com", Password: "Passw0rd", Name: "Jake",
		ClientInfo: &authv1.ClientInfo{
			IpAddress: "1.2.3.4",
			UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
		},
	})
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	list, _ := svc.ListSessions(context.Background(), &authv1.ListSessionsRequest{UserId: resp.UserId})
	if len(list.Sessions) == 0 {
		t.Fatal("no sessions returned")
	}
	sess := list.Sessions[0]
	if sess.Device == "" || sess.Os == "" {
		t.Errorf("expected device/os populated: device=%q os=%q browser=%q", sess.Device, sess.Os, sess.Browser)
	}
}

// ── RevokeSession ─────────────────────────────────────────────────────────────

func TestRevokeSession_NotFound(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "k@test.com", "Passw0rd", "Kate")

	_, err := svc.RevokeSession(context.Background(), &authv1.RevokeSessionRequest{
		UserId:    su.UserId,
		SessionId: "00000000-0000-0000-0000-000000000099",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("want NotFound, got %v", err)
	}
}

// ── RevokeOtherSessions ───────────────────────────────────────────────────────

func TestRevokeOtherSessions_Count(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "l@test.com", "Passw0rd", "Lena")

	// Add a second session via Login.
	svc.Login(context.Background(), &authv1.LoginRequest{ //nolint:errcheck
		Email: "l@test.com", Password: "Passw0rd",
		ClientInfo: &authv1.ClientInfo{IpAddress: "9.9.9.9"},
	})

	sid := sessionID(t, maker, su.AccessToken)
	resp, err := svc.RevokeOtherSessions(context.Background(), &authv1.RevokeOtherSessionsRequest{
		UserId:           su.UserId,
		CurrentSessionId: sid,
	})
	if err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if resp.Revoked != 1 {
		t.Errorf("revoked = %d, want 1", resp.Revoked)
	}
}

// ── OAuthSignIn ───────────────────────────────────────────────────────────────

func TestOAuthSignIn_FreshUser(t *testing.T) {
	svc, _ := newServer(t)
	resp, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "fresh-gid",
		Email: "fresh@test.com", Name: "Fresh",
		ClientInfo: &authv1.ClientInfo{IpAddress: "1.1.1.1"},
	})
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	if !resp.Created || resp.AccessToken == "" {
		t.Errorf("unexpected response: created=%v token=%q", resp.Created, resp.AccessToken)
	}
}

func TestOAuthSignIn_ExistingLink(t *testing.T) {
	svc, _ := newServer(t)
	first, _ := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "ex-gid",
		Email: "existing@test.com", Name: "Existing",
		ClientInfo: &authv1.ClientInfo{IpAddress: "1.1.1.1"},
	})

	second, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "ex-gid",
		Email: "existing@test.com", Name: "Existing",
		ClientInfo: &authv1.ClientInfo{IpAddress: "2.2.2.2"},
	})
	if err != nil {
		t.Fatalf("second OAuthSignIn: %v", err)
	}
	if second.Created || second.UserId != first.UserId {
		t.Errorf("expected same user, created=false: created=%v uid=%q want=%q", second.Created, second.UserId, first.UserId)
	}
}

// ── UpdateUserLanguage ────────────────────────────────────────────────────────

func TestUpdateUserLanguage_Success(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "lang@test.com", "Passw0rd1", "Lang User")

	_, err := svc.UpdateUserLanguage(context.Background(), &authv1.UpdateUserLanguageRequest{
		UserId:   su.UserId,
		Language: "zh",
	})
	if err != nil {
		t.Fatalf("UpdateUserLanguage: %v", err)
	}
}

func TestUpdateUserLanguage_UnsupportedLang_InvalidArgument(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "lang2@test.com", "Passw0rd1", "Lang User 2")

	_, err := svc.UpdateUserLanguage(context.Background(), &authv1.UpdateUserLanguageRequest{
		UserId:   su.UserId,
		Language: "fr", // not in supported set
	})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestUpdateUserLanguage_MissingFields(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.UpdateUserLanguage(context.Background(), &authv1.UpdateUserLanguageRequest{})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty request, got %v", err)
	}
}

func TestOAuthSignIn_EmailMatch(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "m@test.com", "Passw0rd", "Mia")

	resp, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "github", ProviderUserId: "gh123",
		Email: "m@test.com", Name: "Mia",
		ClientInfo: &authv1.ClientInfo{IpAddress: "3.3.3.3"},
	})
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	if resp.Created || resp.UserId != su.UserId {
		t.Errorf("expected existing user linked: created=%v uid=%q want=%q", resp.Created, resp.UserId, su.UserId)
	}
}

func TestOAuthSignIn_ClaimsUnverifiedAccount(t *testing.T) {
	svc, _ := newServer(t)
	// Attacker pre-registers the victim's email with a password they know.
	attacker := signUp(t, svc, "victim@test.com", "Passw0rd1", "Attacker")

	resp, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "g-victim",
		Email: "victim@test.com", Name: "Victim",
		ClientInfo: &authv1.ClientInfo{IpAddress: "4.4.4.4"},
	})
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	if resp.UserId != attacker.UserId {
		t.Fatalf("expected the existing account to be linked, got %q want %q", resp.UserId, attacker.UserId)
	}

	_, err = svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "victim@test.com", Password: "Passw0rd1",
		ClientInfo: &authv1.ClientInfo{IpAddress: "6.6.6.6"},
	})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("attacker password login after claim: want Unauthenticated, got %v", err)
	}
	if _, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: attacker.RefreshToken}); grpcCode(err) != codes.Unauthenticated {
		t.Errorf("attacker's old session after claim: want Unauthenticated, got %v", err)
	}
	got, err := svc.GetUserByID(context.Background(), &authv1.GetUserByIDRequest{UserId: attacker.UserId})
	if err != nil || !got.User.EmailVerified {
		t.Errorf("claimed account should be email-verified: %+v, %v", got, err)
	}
}

func TestOAuthSignIn_ExistingLinkClaimsUnverifiedAccount(t *testing.T) {
	svc, _ := newServer(t)
	// State left by the old bug: attacker's password account already linked to the victim's identity.
	attacker := signUp(t, svc, "linked@test.com", "Passw0rd1", "Attacker")
	if err := store.New(testPool).LinkOAuth(context.Background(), attacker.UserId, "google", "g-linked", "linked@test.com"); err != nil {
		t.Fatalf("LinkOAuth: %v", err)
	}

	if _, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "g-linked",
		Email: "linked@test.com", Name: "Victim",
		ClientInfo: &authv1.ClientInfo{IpAddress: "4.4.4.4"},
	}); err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	_, err := svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "linked@test.com", Password: "Passw0rd1",
		ClientInfo: &authv1.ClientInfo{IpAddress: "6.6.6.6"},
	})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("attacker password login after claim: want Unauthenticated, got %v", err)
	}
}

func TestOAuthSignIn_LegacyUnverifiedOAuthUserKeepsSession(t *testing.T) {
	svc, maker := newServer(t)
	req := &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "g-legacy",
		Email: "legacy@test.com", Name: "Legacy",
		ClientInfo: &authv1.ClientInfo{IpAddress: "4.4.4.4"},
	}
	first, err := svc.OAuthSignIn(context.Background(), req)
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	// Created before OAuth users were marked verified.
	if _, err := testPool.Exec(context.Background(), `UPDATE auth.users SET email_verified = false WHERE id = $1`, first.UserId); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.OAuthSignIn(context.Background(), req); err != nil {
		t.Fatalf("second OAuthSignIn: %v", err)
	}
	got, err := svc.GetUserByID(context.Background(), &authv1.GetUserByIDRequest{UserId: first.UserId})
	if err != nil || !got.User.EmailVerified {
		t.Errorf("legacy user should be verified after sign-in: %+v, %v", got, err)
	}
	if !sessionActive(t, svc, sessionID(t, maker, first.AccessToken)) {
		t.Error("OAuth-only user's earlier session was revoked")
	}
}

func TestOAuthSignIn_VerifiedAccountKeepsPassword(t *testing.T) {
	svc, maker := newServer(t)
	su := signUp(t, svc, "real@test.com", "Passw0rd1", "Real")
	// Mark the email verified the way ConfirmEmail does.
	if err := store.New(testPool).SetEmailVerified(context.Background(), su.UserId); err != nil {
		t.Fatalf("SetEmailVerified: %v", err)
	}

	if _, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "g-real",
		Email: "real@test.com", Name: "Real",
		ClientInfo: &authv1.ClientInfo{IpAddress: "4.4.4.4"},
	}); err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	if _, err := svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "real@test.com", Password: "Passw0rd1",
		ClientInfo: &authv1.ClientInfo{IpAddress: "5.5.5.5"},
	}); err != nil {
		t.Errorf("password login on a verified account should still work: %v", err)
	}
	if !sessionActive(t, svc, sessionID(t, maker, su.AccessToken)) {
		t.Error("verified account's session was revoked")
	}
}

func TestOAuthSignIn_FreshUserIsVerified(t *testing.T) {
	svc, _ := newServer(t)
	resp, err := svc.OAuthSignIn(context.Background(), &authv1.OAuthSignInRequest{
		Provider: "google", ProviderUserId: "g-fresh",
		Email: "fresh@test.com", Name: "Fresh",
		ClientInfo: &authv1.ClientInfo{IpAddress: "4.4.4.4"},
	})
	if err != nil {
		t.Fatalf("OAuthSignIn: %v", err)
	}
	got, err := svc.GetUserByID(context.Background(), &authv1.GetUserByIDRequest{UserId: resp.UserId})
	if err != nil || !got.User.EmailVerified {
		t.Errorf("OAuth-created user should be verified: %+v, %v", got, err)
	}
}

// ── Password policy ─────────────────────────────────────────────────────────

func TestSignUp_WeakPassword(t *testing.T) {
	svc, _ := newServer(t)
	cases := []struct {
		name, password string
	}{
		{"too short", "Ab1!"},
		{"lowercase only", "abcdefgh"}, // score 1
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
				Email: tc.name + "@test.com", Password: tc.password, Name: "Weak",
			})
			if grpcCode(err) != codes.InvalidArgument {
				t.Errorf("SignUp(%q): want InvalidArgument, got %v", tc.password, err)
			}
		})
	}
}

// Weak-password and reuse checks both run before OTP code verification, so
// these cases need no seeded code.

func TestResetPassword_WeakPassword(t *testing.T) {
	svc, _ := newServer(t)
	signUp(t, svc, "weakreset@test.com", "Passw0rd1", "Wendy")

	_, err := svc.ResetPassword(context.Background(), &authv1.ResetPasswordRequest{
		Email: "weakreset@test.com", Code: "000000", NewPassword: "abcdefgh",
	})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("ResetPassword weak: want InvalidArgument, got %v", err)
	}
}

func TestResetPassword_RejectsReuse(t *testing.T) {
	svc, _ := newServer(t)
	signUp(t, svc, "reuse@test.com", "Passw0rd1", "Rita")

	_, err := svc.ResetPassword(context.Background(), &authv1.ResetPasswordRequest{
		Email: "reuse@test.com", Code: "000000", NewPassword: "Passw0rd1",
	})
	if grpcCode(err) != codes.InvalidArgument {
		t.Fatalf("ResetPassword reuse: want InvalidArgument, got %v", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "different") {
		t.Errorf("expected reuse message, got %q", msg)
	}
}

// ── CreateStripeCheckoutSession ───────────────────────────────────────────────

// insertActiveSubscription seeds an active subscription row for the given user
// directly via testPool. auth.subscriptions is FK-referenced from auth.users,
// so the CASCADE truncation in newServer cleans it up between tests automatically.
func insertActiveSubscription(t *testing.T, userID, source string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO auth.subscriptions
			(id, user_id, plan, source, source_ref, status, started_at, created_at, updated_at)
		VALUES
			(gen_random_uuid()::text, $1, 'pro', $2, gen_random_uuid()::text, 'active', NOW(), NOW(), NOW())`,
		userID, source,
	)
	if err != nil {
		t.Fatalf("insertActiveSubscription(%q, %q): %v", userID, source, err)
	}
}

// TestCreateStripeCheckoutSession_StripeSubBlocks verifies that a user who
// already has an active Stripe subscription is rejected with AlreadyExists.
func TestCreateStripeCheckoutSession_StripeSubBlocks(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "stripe-existing@test.com", "Passw0rd1", "Stripe User")

	insertActiveSubscription(t, su.UserId, "stripe")

	_, err := svc.CreateStripeCheckoutSession(context.Background(), &authv1.CreateStripeCheckoutSessionRequest{
		UserId: su.UserId,
		Email:  "stripe-existing@test.com",
	})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("existing Stripe sub: want AlreadyExists, got %v", err)
	}
}

// TestCreateStripeCheckoutSession_PromoSubDoesNotBlock verifies that a promo-code
// subscription does NOT prevent the user from starting a Stripe checkout.
// Stripe is not configured in tests, so the call proceeds past the guard and
// hits the Unimplemented sentinel — not AlreadyExists.
func TestCreateStripeCheckoutSession_PromoSubDoesNotBlock(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "promo-existing@test.com", "Passw0rd1", "Promo User")

	insertActiveSubscription(t, su.UserId, "promo_code")

	_, err := svc.CreateStripeCheckoutSession(context.Background(), &authv1.CreateStripeCheckoutSessionRequest{
		UserId: su.UserId,
		Email:  "promo-existing@test.com",
	})
	if grpcCode(err) == codes.AlreadyExists {
		t.Error("promo subscription must not block Stripe checkout; got AlreadyExists")
	}
	if grpcCode(err) != codes.Unimplemented {
		t.Errorf("want Unimplemented (stripe not configured), got %v", err)
	}
}

// TestCreateStripeCheckoutSession_NoSubReachesStripeGate verifies that a user
// with no subscriptions passes the guard and reaches the Stripe-not-configured
// sentinel (not AlreadyExists).
func TestCreateStripeCheckoutSession_NoSubReachesStripeGate(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "nosub-checkout@test.com", "Passw0rd1", "No Sub")

	_, err := svc.CreateStripeCheckoutSession(context.Background(), &authv1.CreateStripeCheckoutSessionRequest{
		UserId: su.UserId,
		Email:  "nosub-checkout@test.com",
	})
	if grpcCode(err) != codes.Unimplemented {
		t.Errorf("no subscription: want Unimplemented, got %v", err)
	}
}

// TestCreateStripeCheckoutSession_MissingFields verifies validation fires before
// any DB or Stripe call.
func TestCreateStripeCheckoutSession_MissingFields(t *testing.T) {
	svc, _ := newServer(t)
	cases := []struct{ userID, email string }{
		{"", "a@b.com"},
		{"user-1", ""},
	}
	for _, tc := range cases {
		_, err := svc.CreateStripeCheckoutSession(context.Background(), &authv1.CreateStripeCheckoutSessionRequest{
			UserId: tc.userID,
			Email:  tc.email,
		})
		if grpcCode(err) != codes.InvalidArgument {
			t.Errorf("missing(%q,%q): want InvalidArgument, got %v", tc.userID, tc.email, err)
		}
	}
}

// ── ListUserIDs filter (Bug #3 regression) ────────────────────────────────────

// TestListUserIDs_EmptyFilterIncludesAllPlans verifies that an empty PlanFilter
// returns users on every plan. Bug #3 was a hardcoded "pro" filter in the
// scheduler that silently excluded all free-plan users from plan generation.
func TestListUserIDs_EmptyFilterIncludesAllPlans(t *testing.T) {
	svc, _ := newServer(t)

	signUp(t, svc, "free-user@test.com", "Passw0rd1", "Free User")
	proResp := signUp(t, svc, "pro-user@test.com", "Passw0rd1", "Pro User")

	// Upgrade one user to 'pro' directly — avoids needing a live Stripe key.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE auth.users SET plan = 'pro' WHERE id = $1`, proResp.UserId,
	); err != nil {
		t.Fatalf("upgrade user plan: %v", err)
	}

	// Empty filter must return all users (both free and pro).
	all, err := svc.ListUserIDs(context.Background(), &authv1.ListUserIDsRequest{})
	if err != nil {
		t.Fatalf("ListUserIDs(empty): %v", err)
	}
	if len(all.Users) < 2 {
		t.Errorf("empty filter: want ≥2 users, got %d", len(all.Users))
	}

	// "pro" filter must return only the upgraded user.
	proOnly, err := svc.ListUserIDs(context.Background(), &authv1.ListUserIDsRequest{PlanFilter: "pro"})
	if err != nil {
		t.Fatalf("ListUserIDs(pro): %v", err)
	}
	if len(proOnly.Users) != 1 || proOnly.Users[0].UserId != proResp.UserId {
		t.Errorf("pro filter: want 1 user (%s), got %+v", proResp.UserId, proOnly.Users)
	}
}

// ── HandleStripeWebhook idempotency (Bug #6 regression) ──────────────────────

// newServerWithStripe creates a Server with StripeConfig set (no DB truncation
// of its own — the caller is responsible for calling newServer or truncating).
func newServerWithStripe(t *testing.T, stripeCfg server.StripeConfig) *server.Server {
	t.Helper()
	testutil.Truncate(t, testPool)
	pem, err := tokens.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	maker, err := tokens.NewMakerFromPEM(pem)
	if err != nil {
		t.Fatalf("NewMakerFromPEM: %v", err)
	}
	return server.New(store.New(testPool), maker, slog.Default(), nil, server.CodeConfig{
		TTL:            10 * time.Minute,
		ResendCooldown: 3 * time.Minute,
		MaxAttempts:    3,
	}, stripeCfg)
}

// TestHandleStripeWebhook_CheckoutIdempotent verifies that delivering the same
// checkout.session.completed event twice returns no error on either call.
// Bug #6 removed a redundant GetSubscriptionBySourceRef pre-check that would
// race; idempotency is now guaranteed atomically by the ON CONFLICT guard in
// CreateSubscriptionAndSetPlan.
func TestHandleStripeWebhook_CheckoutIdempotent(t *testing.T) {
	const webhookSecret = "whsec_test_idempotent_secret"
	svc := newServerWithStripe(t, server.StripeConfig{WebhookSecret: webhookSecret})

	su := signUp(t, svc, "idempotent@test.com", "Passw0rd1", "Idempotent User")

	// Seed stripe_customer_id directly — CreateStripeCheckoutSession needs a
	// live Stripe key so we bypass it here.
	const customerID = "cus_test_idempotent"
	if _, err := testPool.Exec(context.Background(),
		`UPDATE auth.users SET stripe_customer_id = $1 WHERE id = $2`,
		customerID, su.UserId,
	); err != nil {
		t.Fatalf("set stripe_customer_id: %v", err)
	}

	// Use GenerateTestSignedPayload to produce a valid HMAC signature.
	// api_version mismatch is now ignored (IgnoreAPIVersionMismatch: true).
	payloadBytes := []byte(`{"id":"evt_test_idem","object":"event","api_version":"2024-06-20","type":"checkout.session.completed","data":{"object":{"id":"cs_test_idem","object":"checkout.session","customer":"cus_test_idempotent","subscription":"sub_test_idempotent"}}}`)
	signed := stripewebhook.GenerateTestSignedPayload(&stripewebhook.UnsignedPayload{
		Payload: payloadBytes,
		Secret:  webhookSecret,
	})

	req := &authv1.HandleStripeWebhookRequest{
		Payload:   signed.Payload,
		Signature: signed.Header,
	}

	if _, err := svc.HandleStripeWebhook(context.Background(), req); err != nil {
		t.Fatalf("first webhook delivery: %v", err)
	}
	if _, err := svc.HandleStripeWebhook(context.Background(), req); err != nil {
		t.Errorf("second webhook delivery (idempotent): want nil, got %v", err)
	}
}

// ── OTP attempt limit ─────────────────────────────────────────────────────────

func seedEmailCode(t *testing.T, userID, code string) {
	t.Helper()
	sum := sha256.Sum256([]byte(code))
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO auth.verification_codes (id, user_id, purpose, code_hash, expires_at)
		VALUES (gen_random_uuid(), $1, 'email_verify', $2, now() + interval '10 minutes')`,
		userID, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

func TestConfirmEmail_ParallelWrongGuessesBurnTheCodeAtTheCap(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "otp@test.com", "Passw0rd1", "Otto")
	seedEmailCode(t, su.UserId, "123456")

	var exhausted, wrong atomic.Int32
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ConfirmEmail(context.Background(), &authv1.ConfirmEmailRequest{UserId: su.UserId, Code: fmt.Sprintf("00000%d", i%10)})
			switch grpcCode(err) {
			case codes.ResourceExhausted:
				exhausted.Add(1)
			case codes.Unauthenticated:
				wrong.Add(1)
			default:
				t.Errorf("unexpected result: %v", err)
			}
		}()
	}
	wg.Wait()
	if exhausted.Load() != 1 || wrong.Load() != 29 {
		t.Errorf("exhausted=%d wrong=%d, want exactly 1 and 29: only MaxAttempts guesses may be evaluated", exhausted.Load(), wrong.Load())
	}

	_, err := svc.ConfirmEmail(context.Background(), &authv1.ConfirmEmailRequest{UserId: su.UserId, Code: "123456"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("correct code after the cap: want Unauthenticated, got %v", err)
	}
}

func TestConfirmEmail_CorrectCodeWithinCapSucceeds(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "otp2@test.com", "Passw0rd1", "Olga")
	seedEmailCode(t, su.UserId, "123456")

	for range 2 {
		if _, err := svc.ConfirmEmail(context.Background(), &authv1.ConfirmEmailRequest{UserId: su.UserId, Code: "000000"}); grpcCode(err) != codes.Unauthenticated {
			t.Fatalf("wrong guess: got %v", err)
		}
	}
	if _, err := svc.ConfirmEmail(context.Background(), &authv1.ConfirmEmailRequest{UserId: su.UserId, Code: "123456"}); err != nil {
		t.Fatalf("third attempt with the right code: %v", err)
	}
}
