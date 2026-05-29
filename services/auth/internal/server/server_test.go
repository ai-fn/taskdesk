package server_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/server"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/fsociety/todo/services/auth/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
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
	return server.New(store.New(testPool), maker, slog.Default()), maker
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
	resp := signUp(t, svc, "a@test.com", "pass123", "Alice")

	if resp.UserId == "" || resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Errorf("incomplete response: %+v", resp)
	}
}

func TestSignUp_CreatesOneSession(t *testing.T) {
	svc, _ := newServer(t)
	resp := signUp(t, svc, "b@test.com", "pass123", "Bob")

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
	signUp(t, svc, "dup@test.com", "pass123", "First")

	_, err := svc.SignUp(context.Background(), &authv1.SignUpRequest{
		Email: "dup@test.com", Password: "pass456", Name: "Second",
	})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("want AlreadyExists, got %v", err)
	}
}

func TestSignUp_MissingFields(t *testing.T) {
	svc, _ := newServer(t)
	cases := []struct{ email, password, name string }{
		{"", "pass", "Name"},
		{"a@b.com", "", "Name"},
		{"a@b.com", "pass", ""},
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
	signUp(t, svc, "c@test.com", "secret", "Carol")

	resp, err := svc.Login(context.Background(), &authv1.LoginRequest{
		Email: "c@test.com", Password: "secret",
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
	signUp(t, svc, "d@test.com", "right", "Dave")

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
	su := signUp(t, svc, "e@test.com", "pass", "Eve")

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
	su := signUp(t, svc, "f@test.com", "pass", "Frank")

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
	su := signUp(t, svc, "g@test.com", "pass", "Grace")

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

func TestValidateToken_BadToken(t *testing.T) {
	svc, _ := newServer(t)
	_, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{AccessToken: "garbage"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("want Unauthenticated, got %v", err)
	}
}

// ── Logout ────────────────────────────────────────────────────────────────────

func TestLogout_RemovesSession(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "h@test.com", "pass", "Hank")

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
	su := signUp(t, svc, "i@test.com", "pass", "Iris")

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
		Email: "j@test.com", Password: "pass", Name: "Jake",
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
	su := signUp(t, svc, "k@test.com", "pass", "Kate")

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
	su := signUp(t, svc, "l@test.com", "pass", "Lena")

	// Add a second session via Login.
	svc.Login(context.Background(), &authv1.LoginRequest{ //nolint:errcheck
		Email: "l@test.com", Password: "pass",
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

func TestOAuthSignIn_EmailMatch(t *testing.T) {
	svc, _ := newServer(t)
	su := signUp(t, svc, "m@test.com", "pass", "Mia")

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
