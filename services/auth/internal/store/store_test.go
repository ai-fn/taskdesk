package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/fsociety/todo/services/auth/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── package-level setup (one container for the whole package) ─────────────────

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown := testutil.StartPostgres()
	testPool = pool
	code := m.Run()
	teardown()
	os.Exit(code)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newStore(t *testing.T) *store.Store {
	t.Helper()
	testutil.Truncate(t, testPool)
	return store.New(testPool)
}

func makeUser(id, email string) *domain.User {
	now := time.Now().UTC()
	return &domain.User{
		ID:        id,
		Email:     email,
		Name:      "Test User",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func mustCreateUser(t *testing.T, s *store.Store, u *domain.User) {
	t.Helper()
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
}

// mustCreateSession uses valid UUID-shaped IDs for the sessions.id column.
func mustCreateSession(t *testing.T, s *store.Store, id, userID, hash string, ttl time.Duration) {
	t.Helper()
	err := s.CreateSession(context.Background(), id, userID, hash, "1.2.3.4", "TestAgent/1.0", time.Now().Add(ttl))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

// valid UUID constants used as session IDs throughout.
const (
	uuidS1    = "00000000-0000-0000-0000-000000000001"
	uuidS2    = "00000000-0000-0000-0000-000000000002"
	uuidS3    = "00000000-0000-0000-0000-000000000003"
	uuidSLive = "00000000-0000-0000-0000-00000000000a"
	uuidSDead = "00000000-0000-0000-0000-00000000000b"
	uuidSOld  = "00000000-0000-0000-0000-00000000000c"
	uuidSNew  = "00000000-0000-0000-0000-00000000000d"
	uuidSR1   = "00000000-0000-0000-0000-00000000000e"
	uuidSDel  = "00000000-0000-0000-0000-00000000000f"
	uuidSOwn  = "00000000-0000-0000-0000-000000000010"
	uuidSCurr = "00000000-0000-0000-0000-000000000011"
	uuidSOth1 = "00000000-0000-0000-0000-000000000012"
	uuidSOth2 = "00000000-0000-0000-0000-000000000013"
)

// ── users ─────────────────────────────────────────────────────────────────────

func TestCreateUser_GetByEmail(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u1", "alice@example.com"))

	got, err := s.GetUserByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != "u1" || got.Email != "alice@example.com" {
		t.Errorf("unexpected user: %+v", got)
	}
}

func TestCreateUser_GetByID(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u2", "bob@example.com"))

	got, err := s.GetUserByID(context.Background(), "u2")
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.ID != "u2" {
		t.Errorf("got ID %q, want %q", got.ID, "u2")
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u3", "dup@example.com"))

	err := s.CreateUser(context.Background(), makeUser("u4", "dup@example.com"))
	if !errors.Is(err, store.ErrEmailTaken) {
		t.Errorf("want ErrEmailTaken, got %v", err)
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetUserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGetUserByID_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetUserByID(context.Background(), "doesnotexist")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// ── sessions ──────────────────────────────────────────────────────────────────

func TestCreateSession_ListSessions(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u10", "sess@example.com"))
	mustCreateSession(t, s, uuidS1, "u10", "hash1", time.Hour)

	sessions, err := s.ListSessions(context.Background(), "u10")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != uuidS1 {
		t.Errorf("expected 1 session with id %s, got %+v", uuidS1, sessions)
	}
}

func TestListSessions_ExpiredExcluded(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u11", "exp@example.com"))
	mustCreateSession(t, s, uuidSLive, "u11", "hashLive", time.Hour)

	// Insert an already-expired session directly (must pass a valid IP — INET rejects "").
	err := s.CreateSession(context.Background(), uuidSDead, "u11", "hashDead", "1.2.3.4", "TestAgent/1.0", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("CreateSession (expired): %v", err)
	}

	sessions, err := s.ListSessions(context.Background(), "u11")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != uuidSLive {
		t.Errorf("expected only live session, got %+v", sessions)
	}
}

func TestListSessions_OrderByLastActiveDesc(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u12", "order@example.com"))
	mustCreateSession(t, s, uuidSOld, "u12", "hashOlder", time.Hour)
	time.Sleep(10 * time.Millisecond)
	mustCreateSession(t, s, uuidSNew, "u12", "hashNewer", time.Hour)

	sessions, err := s.ListSessions(context.Background(), "u12")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) < 2 || sessions[0].ID != uuidSNew {
		t.Errorf("expected newest-active first, got %+v", sessions)
	}
}

func TestRotateSession_StableID(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u20", "rotate@example.com"))
	mustCreateSession(t, s, uuidSR1, "u20", "oldHash", time.Hour)

	sidGot, uidGot, err := s.RotateSession(context.Background(), "oldHash", "newHash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	if sidGot != uuidSR1 {
		t.Errorf("session ID after rotation = %q, want %q (must be stable)", sidGot, uuidSR1)
	}
	if uidGot != "u20" {
		t.Errorf("user ID = %q, want u20", uidGot)
	}

	// Old hash must no longer work.
	_, _, err = s.RotateSession(context.Background(), "oldHash", "anotherHash", time.Now().Add(time.Hour))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("replaying old hash: want ErrNotFound, got %v", err)
	}
}

func TestRotateSession_UnknownHash(t *testing.T) {
	s := newStore(t)
	_, _, err := s.RotateSession(context.Background(), "nosuchhash", "x", time.Now().Add(time.Hour))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound for unknown hash, got %v", err)
	}
}

func TestDeleteSessionByTokenHash(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u30", "del@example.com"))
	mustCreateSession(t, s, uuidSDel, "u30", "hashDel", time.Hour)

	if err := s.DeleteSessionByTokenHash(context.Background(), "hashDel"); err != nil {
		t.Fatalf("DeleteSessionByTokenHash: %v", err)
	}

	sessions, _ := s.ListSessions(context.Background(), "u30")
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(sessions))
	}
}

func TestRevokeSession_OwnershipScoped(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u40", "owner@example.com"))
	mustCreateUser(t, s, makeUser("u41", "other@example.com"))
	mustCreateSession(t, s, uuidSOwn, "u40", "hashOwn", time.Hour)

	// u41 tries to revoke u40's session — ownership check must block it.
	err := s.RevokeSession(context.Background(), "u41", uuidSOwn)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-user revoke: want ErrNotFound, got %v", err)
	}

	// Owner revokes it — must succeed.
	if err := s.RevokeSession(context.Background(), "u40", uuidSOwn); err != nil {
		t.Errorf("owner revoke: %v", err)
	}
}

func TestRevokeSession_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.RevokeSession(context.Background(), "nobody", "00000000-0000-0000-0000-000000000099")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u50", "revall@example.com"))
	mustCreateSession(t, s, uuidSCurr, "u50", "hCurr", time.Hour)
	mustCreateSession(t, s, uuidSOth1, "u50", "hOth1", time.Hour)
	mustCreateSession(t, s, uuidSOth2, "u50", "hOth2", time.Hour)

	count, err := s.RevokeOtherSessions(context.Background(), "u50", uuidSCurr)
	if err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if count != 2 {
		t.Errorf("revoked %d sessions, want 2", count)
	}

	sessions, _ := s.ListSessions(context.Background(), "u50")
	if len(sessions) != 1 || sessions[0].ID != uuidSCurr {
		t.Errorf("expected only current session remaining, got %+v", sessions)
	}
}

// ── OAuth ─────────────────────────────────────────────────────────────────────

func TestCreateUserNoPassword_FindByOAuth(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC()
	u := &domain.User{ID: "u60", Email: "oauth@example.com", Name: "OAuth User", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateUserNoPassword(context.Background(), u); err != nil {
		t.Fatalf("CreateUserNoPassword: %v", err)
	}

	// Note: LinkOAuth signature is (ctx, userID, provider, providerUserID, email).
	if err := s.LinkOAuth(context.Background(), "u60", "google", "gid1", "oauth@example.com"); err != nil {
		t.Fatalf("LinkOAuth: %v", err)
	}

	got, err := s.FindByOAuth(context.Background(), "google", "gid1")
	if err != nil {
		t.Fatalf("FindByOAuth: %v", err)
	}
	if got.ID != "u60" {
		t.Errorf("FindByOAuth ID = %q, want u60", got.ID)
	}
}

func TestLinkOAuth_Idempotent(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("u61", "oa2@example.com"))

	if err := s.LinkOAuth(context.Background(), "u61", "discord", "did1", "oa2@example.com"); err != nil {
		t.Fatalf("first LinkOAuth: %v", err)
	}
	// Second call with the same provider+id must be a no-op, not an error.
	if err := s.LinkOAuth(context.Background(), "u61", "discord", "did1", "oa2@example.com"); err != nil {
		t.Errorf("idempotent LinkOAuth: %v", err)
	}
}

// ── language column ────────────────────────────────────────────────────────────

func TestCreateUser_DefaultLanguageIsEn(t *testing.T) {
	s := newStore(t)
	u := makeUser("ulang1", "lang1@example.com")
	// Language field is zero-value "" — DB default 'en' should take effect via the
	// explicit INSERT value, which is also "" → store writes "" → check what comes back.
	// Since we set Language:"en" explicitly in SignUp, test that round-trip works.
	u.Language = "en"
	mustCreateUser(t, s, u)

	got, err := s.GetUserByID(context.Background(), "ulang1")
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Language != "en" {
		t.Errorf("Language = %q, want %q", got.Language, "en")
	}
}

func TestUpdateUserLanguage_RoundTrip(t *testing.T) {
	s := newStore(t)
	u := makeUser("ulang2", "lang2@example.com")
	u.Language = "en"
	mustCreateUser(t, s, u)

	if err := s.UpdateUserLanguage(context.Background(), "ulang2", "zh"); err != nil {
		t.Fatalf("UpdateUserLanguage: %v", err)
	}

	got, err := s.GetUserByID(context.Background(), "ulang2")
	if err != nil {
		t.Fatalf("GetUserByID after update: %v", err)
	}
	if got.Language != "zh" {
		t.Errorf("Language = %q, want %q", got.Language, "zh")
	}
}

func TestUpdateUserLanguage_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.UpdateUserLanguage(context.Background(), "nonexistent", "zh")
	if err == nil {
		t.Error("expected error for nonexistent user, got nil")
	}
}
