package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/fsociety/todo/services/auth/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestSessionActive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u31", "act@example.com"))
	mustCreateSession(t, s, uuidSLive, "u31", "hashAct", time.Hour)
	if err := s.CreateSession(ctx, uuidSDead, "u31", "hashExp", "1.2.3.4", "TestAgent/1.0", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateSession (expired): %v", err)
	}

	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"live", uuidSLive, true},
		{"expired", uuidSDead, false},
		{"unknown", "00000000-0000-0000-0000-000000000000", false},
	}
	for _, tc := range cases {
		got, err := s.SessionActive(ctx, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("%s: SessionActive = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}

	if err := s.DeleteSessionByTokenHash(ctx, "hashAct"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SessionActive(ctx, uuidSLive); got {
		t.Error("deleted session still active")
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

func TestClaimUnverifiedAccount(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	pw := "bcrypt-hash"
	u := makeUser("u70", "claim@example.com")
	u.PasswordHash = &pw
	mustCreateUser(t, s, u)
	mustCreateSession(t, s, uuidS1, "u70", "hashClaim", time.Hour)
	if err := s.CreateVerificationCode(ctx, "00000000-0000-0000-0000-0000000000c1", "u70", "email_verify", "codehash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimUnverifiedAccount(ctx, "u70")
	if err != nil || !claimed {
		t.Fatalf("ClaimUnverifiedAccount = %v, %v; want true, nil", claimed, err)
	}
	got, err := s.GetUserByID(ctx, "u70")
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != nil || !got.EmailVerified {
		t.Errorf("after claim: password=%v verified=%v, want nil/true", got.PasswordHash, got.EmailVerified)
	}
	if sessions, _ := s.ListSessions(ctx, "u70"); len(sessions) != 0 {
		t.Errorf("sessions after claim = %d, want 0", len(sessions))
	}
	if _, err := s.GetActiveVerificationCode(ctx, "u70", "email_verify"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending code survived the claim: err = %v", err)
	}

	// Already verified now: a second claim must not touch the account.
	if claimed, err := s.ClaimUnverifiedAccount(ctx, "u70"); err != nil || claimed {
		t.Errorf("second claim = %v, %v; want false, nil", claimed, err)
	}
}

func TestClaimUnverifiedAccount_OAuthOnlyKeepsSessions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u72", "legacy@example.com")) // no password, unverified
	mustCreateSession(t, s, uuidS1, "u72", "hashLegacy", time.Hour)

	if claimed, err := s.ClaimUnverifiedAccount(ctx, "u72"); err != nil || !claimed {
		t.Fatalf("claim = %v, %v; want true, nil", claimed, err)
	}
	if sessions, _ := s.ListSessions(ctx, "u72"); len(sessions) != 1 {
		t.Errorf("OAuth-only account lost its sessions: %d", len(sessions))
	}
}

func TestCreateLoginSession_GuardedByPasswordHash(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	pw := "hash-1"
	u := makeUser("u73", "guard@example.com")
	u.PasswordHash = &pw
	mustCreateUser(t, s, u)
	exp := time.Now().Add(time.Hour)

	if err := s.CreateLoginSession(ctx, uuidS1, "u73", "tok1", "1.2.3.4", "UA", exp, "hash-1"); err != nil {
		t.Fatalf("matching hash: %v", err)
	}
	if err := s.CreateLoginSession(ctx, uuidS2, "u73", "tok2", "1.2.3.4", "UA", exp, "stale-hash"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("stale hash: err = %v, want ErrNotFound", err)
	}
	if _, err := s.ClaimUnverifiedAccount(ctx, "u73"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLoginSession(ctx, uuidS2, "u73", "tok2", "1.2.3.4", "UA", exp, "hash-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after claim: err = %v, want ErrNotFound", err)
	}
}

// A login racing a claim must never leave a session behind: it either lands before
// the claim (and is deleted by it) or after (and is refused).
func TestClaimUnverifiedAccount_NoSessionSurvivesRacingLogin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("race%d", i)
		pw := "hash"
		u := makeUser(id, id+"@example.com")
		u.PasswordHash = &pw
		mustCreateUser(t, s, u)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = s.CreateLoginSession(ctx, uuid.NewString(), id, "tok-"+id, "1.2.3.4", "UA", time.Now().Add(time.Hour), "hash")
		}()
		go func() {
			defer wg.Done()
			if _, err := s.ClaimUnverifiedAccount(ctx, id); err != nil {
				t.Errorf("claim: %v", err)
			}
		}()
		wg.Wait()

		if sessions, _ := s.ListSessions(ctx, id); len(sessions) != 0 {
			t.Fatalf("iteration %d: %d session(s) survived the claim", i, len(sessions))
		}
	}
}

func TestClaimUnverifiedAccount_VerifiedUntouched(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	pw := "bcrypt-hash"
	u := makeUser("u71", "verified@example.com")
	u.PasswordHash = &pw
	u.EmailVerified = true
	mustCreateUser(t, s, u)
	mustCreateSession(t, s, uuidS1, "u71", "hashV", time.Hour)

	if claimed, err := s.ClaimUnverifiedAccount(ctx, "u71"); err != nil || claimed {
		t.Fatalf("claim of verified account = %v, %v; want false, nil", claimed, err)
	}
	got, _ := s.GetUserByID(ctx, "u71")
	if got.PasswordHash == nil {
		t.Error("verified account lost its password")
	}
	if sessions, _ := s.ListSessions(ctx, "u71"); len(sessions) != 1 {
		t.Errorf("verified account sessions = %d, want 1", len(sessions))
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

// ── Subscription source_ref uniqueness ────────────────────────────────────────

func makeSub(id, userID, source, sourceRef string) *domain.Subscription {
	return &domain.Subscription{
		ID:        id,
		UserID:    userID,
		Plan:      "pro",
		Source:    source,
		SourceRef: sourceRef,
		Status:    "active",
		StartedAt: time.Now().UTC(),
	}
}

// TestCreateSubscriptionAndSetPlan_PromoSharedSourceRefAllowed verifies that
// two different users can each redeem the same promo code (same source_ref) —
// the partial unique index does not cover promo_code rows.
func TestCreateSubscriptionAndSetPlan_PromoSharedSourceRefAllowed(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("promo-u1", "promo1@test.com"))
	mustCreateUser(t, s, makeUser("promo-u2", "promo2@test.com"))

	const sharedPromoRef = "promo-code-uuid-shared"

	if err := s.CreateSubscriptionAndSetPlan(context.Background(),
		makeSub("sub-p1", "promo-u1", "promo_code", sharedPromoRef), "pro",
	); err != nil {
		t.Fatalf("first promo redemption: %v", err)
	}
	if err := s.CreateSubscriptionAndSetPlan(context.Background(),
		makeSub("sub-p2", "promo-u2", "promo_code", sharedPromoRef), "pro",
	); err != nil {
		t.Fatalf("second promo redemption (same source_ref, different user): %v", err)
	}
}

// TestCreateSubscriptionAndSetPlan_StripeSourceRefUnique verifies that two
// rows with source = 'stripe' and the same source_ref are rejected — the
// partial unique index enforces idempotency for Stripe webhook re-deliveries.
func TestCreateSubscriptionAndSetPlan_StripeSourceRefUnique(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("stripe-u1", "stripe1@test.com"))
	mustCreateUser(t, s, makeUser("stripe-u2", "stripe2@test.com"))

	const stripeSubID = "sub_stripe_123"

	if err := s.CreateSubscriptionAndSetPlan(context.Background(),
		makeSub("sub-s1", "stripe-u1", "stripe", stripeSubID), "pro",
	); err != nil {
		t.Fatalf("first stripe sub: %v", err)
	}

	err := s.CreateSubscriptionAndSetPlan(context.Background(),
		makeSub("sub-s2", "stripe-u2", "stripe", stripeSubID), "pro",
	)
	if !errors.Is(err, store.ErrSubscriptionAlreadyExists) {
		t.Errorf("duplicate stripe source_ref: want ErrSubscriptionAlreadyExists, got %v", err)
	}
}

// ── RedeemPromoCode Stripe guard (TOCTOU fix) ─────────────────────────────────

// mustCreatePromoCode inserts an unlimited, all-user promo code for testing.
func mustCreatePromoCode(t *testing.T, s *store.Store, id, code string) *domain.PromoCode {
	t.Helper()
	pc := &domain.PromoCode{
		ID:              id,
		Code:            code,
		MaxUsagePerUser: 1,
	}
	if err := s.CreatePromoCode(context.Background(), pc); err != nil {
		t.Fatalf("CreatePromoCode: %v", err)
	}
	return pc
}

// mustInsertStripeSubscription seeds an active Stripe subscription directly.
func mustInsertStripeSubscription(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO auth.subscriptions
			(id, user_id, plan, source, source_ref, status, started_at, created_at, updated_at)
		VALUES
			(gen_random_uuid()::text, $1, 'pro', 'stripe', gen_random_uuid()::text, 'active', NOW(), NOW(), NOW())`,
		userID,
	)
	if err != nil {
		t.Fatalf("mustInsertStripeSubscription: %v", err)
	}
}

// TestRedeemPromoCode_StripeSubBlocksInsideTransaction verifies that
// RedeemPromoCode rejects redemption when the user has an active Stripe
// subscription, even if that subscription was committed after the server's
// pre-check (TOCTOU scenario). The check is now inside the transaction.
func TestRedeemPromoCode_StripeSubBlocksInsideTransaction(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("toctou-user", "toctou@test.com"))
	pc := mustCreatePromoCode(t, s, "pc-toctou-id", "TOCTOU10")

	// Simulate a Stripe webhook that committed after the server pre-check.
	mustInsertStripeSubscription(t, testPool, "toctou-user")

	err := s.RedeemPromoCode(context.Background(), pc, "toctou-user", "toctou@test.com", "sub-toctou")
	if !errors.Is(err, store.ErrUserHasActiveStripeSubscription) {
		t.Errorf("want ErrUserHasActiveStripeSubscription, got %v", err)
	}
}

// ── RedeemPromoCode limit enforcement ─────────────────────────────────────────

// TestRedeemPromoCode_GlobalLimitEnforced verifies that once the global usage
// cap is reached no further redemptions are accepted, even by different users.
func TestRedeemPromoCode_GlobalLimitEnforced(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("glim-u1", "glim1@test.com"))
	mustCreateUser(t, s, makeUser("glim-u2", "glim2@test.com"))

	maxUses := 1
	pc := &domain.PromoCode{
		ID:              "pc-glim-id",
		Code:            "GLOBAL1",
		MaxUsageCount:   &maxUses,
		MaxUsagePerUser: 10,
	}
	if err := s.CreatePromoCode(context.Background(), pc); err != nil {
		t.Fatalf("CreatePromoCode: %v", err)
	}

	if err := s.RedeemPromoCode(context.Background(), pc, "glim-u1", "glim1@test.com", "sub-glim-1"); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	err := s.RedeemPromoCode(context.Background(), pc, "glim-u2", "glim2@test.com", "sub-glim-2")
	if !errors.Is(err, store.ErrPromoCodeExhausted) {
		t.Errorf("want ErrPromoCodeExhausted, got %v", err)
	}
}

// TestRedeemPromoCode_PerUserLimitEnforced verifies that a user cannot redeem
// the same promo code more times than MaxUsagePerUser allows.
func TestRedeemPromoCode_PerUserLimitEnforced(t *testing.T) {
	s := newStore(t)
	mustCreateUser(t, s, makeUser("plim-u1", "plim1@test.com"))

	// mustCreatePromoCode sets MaxUsagePerUser = 1.
	pc := mustCreatePromoCode(t, s, "pc-plim-id", "PERUSER1")

	if err := s.RedeemPromoCode(context.Background(), pc, "plim-u1", "plim1@test.com", "sub-plim-1"); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	err := s.RedeemPromoCode(context.Background(), pc, "plim-u1", "plim1@test.com", "sub-plim-2")
	if !errors.Is(err, store.ErrPromoCodePerUserLimitReached) {
		t.Errorf("want ErrPromoCodePerUserLimitReached, got %v", err)
	}
}

func TestClaimCodeAttempt_ParallelGuessesNeverExceedTheCap(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u80", "otp@example.com"))

	const maxAttempts, guessers, rounds = 3, 40, 10 // the race is probabilistic; rounds make it reliable
	for round := range rounds {
		if err := s.CreateVerificationCode(ctx, uuid.NewString(), "u80", "email_verify", "h", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		var granted atomic.Int32
		var wg sync.WaitGroup
		for range guessers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				vc, err := s.ClaimCodeAttempt(ctx, "u80", "email_verify", maxAttempts)
				switch {
				case err == nil:
					if vc.CodeHash != "h" {
						t.Errorf("hash = %q", vc.CodeHash)
					}
					granted.Add(1)
				case !errors.Is(err, store.ErrNotFound):
					t.Errorf("unexpected error: %v", err)
				}
			}()
		}
		wg.Wait()
		if got := granted.Load(); got != maxAttempts {
			t.Fatalf("round %d: %d guesses were evaluated, want exactly %d", round, got, maxAttempts)
		}
		// Burn the exhausted code so the next round gets a fresh latest one.
		if _, err := testPool.Exec(ctx, `UPDATE auth.verification_codes SET consumed_at = now() WHERE user_id = 'u80'`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestClaimCodeAttempt_OnlyTheLatestLiveCode(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u81", "otp2@example.com"))

	if _, err := s.ClaimCodeAttempt(ctx, "u81", "email_verify", 3); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no code: err = %v, want ErrNotFound", err)
	}

	mk := func(id, hash string, expires time.Duration) {
		t.Helper()
		if err := s.CreateVerificationCode(ctx, id, "u81", "email_verify", hash, time.Now().Add(expires)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // distinct created_at
	}
	mk("00000000-0000-0000-0000-0000000000e1", "old", time.Hour)
	mk("00000000-0000-0000-0000-0000000000e2", "expired", -time.Minute)

	vc, err := s.ClaimCodeAttempt(ctx, "u81", "email_verify", 3)
	if err != nil || vc.CodeHash != "old" || vc.Attempts != 1 {
		t.Fatalf("claim = %+v, %v; want the old live code with attempts=1", vc, err)
	}
	if err := s.ConsumeCode(ctx, vc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCodeAttempt(ctx, "u81", "email_verify", 3); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("consumed code: err = %v, want ErrNotFound", err)
	}
}

func TestSetStripeCustomerIDIfUnset_FirstWriterWins(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u90", "stripe@example.com"))

	got, err := s.SetStripeCustomerIDIfUnset(ctx, "u90", "cus_first")
	if err != nil || got != "cus_first" {
		t.Fatalf("first = %q, %v", got, err)
	}
	got, err = s.SetStripeCustomerIDIfUnset(ctx, "u90", "cus_second")
	if err != nil || got != "cus_first" {
		t.Errorf("second = %q, %v; want the first customer kept", got, err)
	}
	if _, err := s.SetStripeCustomerIDIfUnset(ctx, "nobody", "cus_x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}

	mustCreateUser(t, s, makeUser("u91", "stripe2@example.com"))
	var pgErr *pgconn.PgError
	if _, err := s.SetStripeCustomerIDIfUnset(ctx, "u91", "cus_first"); !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("customer attached to a second user: err = %v, want unique violation", err)
	}
}

func TestMoveSubscription_KeepsPlanInSyncAndRespectsOtherSources(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u92", "mix@example.com"))
	plan := func() string {
		t.Helper()
		u, err := s.GetUserByID(ctx, "u92")
		if err != nil {
			t.Fatal(err)
		}
		return u.Plan
	}

	if err := s.CreateSubscriptionAndSetPlan(ctx, makeSub("sub-m1", "u92", "stripe", "sub_mix"), "pro"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReactivateSubscriptionBySourceRef(ctx, "sub_mix"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reactivating an active row: err = %v, want ErrNotFound", err)
	}
	if uid, err := s.CancelSubscriptionBySourceRef(ctx, "sub_mix"); err != nil || uid != "u92" {
		t.Fatalf("cancel = %q, %v", uid, err)
	}
	if got := plan(); got != "free" {
		t.Errorf("plan after cancel = %q, want free (updated in the same transaction)", got)
	}
	if uid, err := s.ReactivateSubscriptionBySourceRef(ctx, "sub_mix"); err != nil || uid != "u92" {
		t.Fatalf("reactivate = %q, %v", uid, err)
	}
	if got := plan(); got != "pro" {
		t.Errorf("plan after reactivate = %q, want pro", got)
	}

	// A promo subscription keeps the user on pro when the Stripe one ends.
	if err := s.CreateSubscriptionAndSetPlan(ctx, makeSub("sub-m2", "u92", "promo_code", "promo-ref"), "pro"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelSubscriptionBySourceRef(ctx, "sub_mix"); err != nil {
		t.Fatal(err)
	}
	if got := plan(); got != "pro" {
		t.Errorf("plan with a live promo subscription = %q, want pro", got)
	}
}

func TestEmails_AreMatchedCaseInsensitively(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := makeUser("u100", "  Alice@Example.COM ")
	mustCreateUser(t, s, u)
	if u.Email != "alice@example.com" {
		t.Errorf("stored email = %q, want lowercased and trimmed", u.Email)
	}

	for _, probe := range []string{"alice@example.com", "ALICE@EXAMPLE.COM", " Alice@Example.com"} {
		got, err := s.GetUserByEmail(ctx, probe)
		if err != nil || got.ID != "u100" {
			t.Errorf("GetUserByEmail(%q) = %v, %v; want u100", probe, got, err)
		}
	}
	if err := s.CreateUser(ctx, makeUser("u101", "ALICE@example.com")); !errors.Is(err, store.ErrEmailTaken) {
		t.Errorf("same address in another case: err = %v, want ErrEmailTaken", err)
	}
	if err := s.CreateUserNoPassword(ctx, makeUser("u102", "Alice@example.com")); !errors.Is(err, store.ErrEmailTaken) {
		t.Errorf("oauth signup with another case: err = %v, want ErrEmailTaken", err)
	}
}

func TestEmails_LegacyMixedCaseRowIsStillFoundAndBlocksDuplicates(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// A row written before normalization.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO auth.users (id, email, name, language) VALUES ('legacy1', 'Bob@Example.com', 'Bob', 'en')`); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUserByEmail(ctx, "bob@example.com")
	if err != nil || got.ID != "legacy1" {
		t.Fatalf("GetUserByEmail = %v, %v; want the legacy row", got, err)
	}
	if err := s.CreateUser(ctx, makeUser("u103", "bob@example.com")); !errors.Is(err, store.ErrEmailTaken) {
		t.Errorf("duplicate of a legacy row: err = %v, want ErrEmailTaken", err)
	}
}

func TestRedeemPromoCode_AllowedEmailIgnoresCase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, makeUser("u104", "promo@example.com"))
	allowed, maxUse := "Promo@Example.com", 5
	pc := &domain.PromoCode{ID: uuid.NewString(), Code: "CASE1", AllowedEmail: &allowed, MaxUsageCount: &maxUse, MaxUsagePerUser: 1}
	if err := s.CreatePromoCode(ctx, pc); err != nil {
		t.Fatal(err)
	}
	if err := s.RedeemPromoCode(ctx, pc, "u104", "PROMO@example.com", uuid.NewString()); err != nil {
		t.Errorf("redeem with a different case: %v", err)
	}
}

func TestMigration000012_LowercasesOnlyWhatCannotCollide(t *testing.T) {
	_ = newStore(t)
	ctx := context.Background()
	sql, err := os.ReadFile("../../../../migrations/auth/000012_users_email_lowercase.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{{"m1", "Zed@Example.com"}, {"m2", "Dup@Example.com"}, {"m3", "dup@example.com"}} {
		if _, err := testPool.Exec(ctx, `INSERT INTO auth.users (id, email, name, language) VALUES ($1, $2, 'n', 'en')`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testPool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("migration: %v", err)
	}

	emails := map[string]string{}
	rows, err := testPool.Query(ctx, `SELECT id, email FROM auth.users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			t.Fatal(err)
		}
		emails[id] = email
	}
	if emails["m1"] != "zed@example.com" {
		t.Errorf("non-colliding row = %q, want lowercased", emails["m1"])
	}
	if emails["m2"] != "Dup@Example.com" || emails["m3"] != "dup@example.com" {
		t.Errorf("colliding rows were rewritten: %q / %q (they need a human merge)", emails["m2"], emails["m3"])
	}
}

func TestCreateUser_EmptyEmailAfterNormalizationIsRejected(t *testing.T) {
	s := newStore(t)
	if err := s.CreateUser(context.Background(), makeUser("u110", "  ")); !errors.Is(err, store.ErrInvalidEmail) {
		t.Errorf("err = %v, want ErrInvalidEmail", err)
	}
}
