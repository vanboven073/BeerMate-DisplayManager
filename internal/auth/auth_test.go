package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dbx.Open(dbx.Options{
		Path:   filepath.Join(t.TempDir(), "auth.db"),
		Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

// testHasher uses the real parameters but allows only one concurrent hash, which
// keeps the suite's peak memory bounded.
func testHasher() *Hasher { return NewHasher(1) }

// ---- password hashing ----------------------------------------------------

func TestHashAndVerify(t *testing.T) {
	h := testHasher()
	const pw = "correct-horse-battery-staple"

	enc, err := h.Hash(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$") {
		t.Errorf("unexpected hash format: %s", enc)
	}
	if strings.Contains(enc, pw) {
		t.Fatal("encoded hash contains the plaintext password")
	}
	if err := h.Verify(pw, enc); err != nil {
		t.Errorf("verify correct password: %v", err)
	}
	if err := h.Verify("wrong-password-entirely", enc); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("verify wrong password = %v, want ErrPasswordMismatch", err)
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	h := testHasher()
	const pw = "correct-horse-battery-staple"
	a, err := h.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical; salt is not random")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := testHasher()
	for _, bad := range []string{
		"", "not-a-hash", "$argon2id$", "$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=65536,t=2,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=2,p=4$!!!notbase64$aGFzaA",
	} {
		if err := h.Verify("whatever", bad); err == nil {
			t.Errorf("Verify(%q) returned nil, want an error", bad)
		}
	}
}

// The dummy hash exists purely to equalise timing between "unknown user" and
// "wrong password". If it does not decode, Verify bails out before doing any
// Argon2 work and the timing side channel it was meant to close reopens.
func TestDummyHashIsWellFormed(t *testing.T) {
	if _, _, _, err := decodeHash(dummyHash); err != nil {
		t.Fatalf("dummyHash does not decode (%v); the username-enumeration "+
			"timing defence in Authenticate would be a no-op", err)
	}
	h := testHasher()
	if err := h.Verify("anything at all", dummyHash); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("Verify against dummyHash = %v, want ErrPasswordMismatch", err)
	}
}

func TestPasswordStrengthPolicy(t *testing.T) {
	tests := []struct {
		pw      string
		wantErr bool
		reason  string
	}{
		{"correct-horse-battery-staple", false, "long passphrase"},
		{"Sf7#kQ2!mZp9", false, "12 chars mixed"},
		{"short", true, "too short"},
		{"elevenchar", true, "11 chars"},
		{"aaaaaaaaaaaaaaa", true, "single repeated character"},
		{"            ", true, "whitespace only"},
		{"password123", true, "common password"},
		{"beermate2026", true, "common password with digits"},
		{"BeerMate2026", true, "common password, different case"},
		{strings.Repeat("x", MaxPasswordLength+1), true, "over max length"},
	}
	for _, tt := range tests {
		err := ValidatePasswordStrength(tt.pw)
		if tt.wantErr && err == nil {
			t.Errorf("ValidatePasswordStrength(%q) = nil, want error (%s)", tt.pw, tt.reason)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ValidatePasswordStrength(%q) = %v, want nil (%s)", tt.pw, err, tt.reason)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	h := testHasher()
	enc, err := h.Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if h.NeedsRehash(enc) {
		t.Error("a freshly created hash should not need rehashing")
	}
	weak := "$argon2id$v=19$m=1024,t=1,p=1$c29tZXNhbHRzb21lc2FsdA$" +
		"J8yTQnYt0y1QW0xUZ0kZ1H1yqCk5o0xF3xL3G8mFbQY"
	if !h.NeedsRehash(weak) {
		t.Error("a hash with weaker parameters should need rehashing")
	}
}

// ---- users ---------------------------------------------------------------

func TestBootstrapOnlyOnce(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()

	need, err := us.NeedsBootstrap(ctx)
	if err != nil || !need {
		t.Fatalf("NeedsBootstrap = %v, %v; want true, nil", need, err)
	}

	u, err := us.Bootstrap(ctx, "bram", "Bram", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("bootstrap role = %q, want admin", u.Role)
	}

	// A second bootstrap must be refused, or the endpoint becomes a backdoor for
	// creating extra admin accounts after setup.
	if _, err := us.Bootstrap(ctx, "eve", "Eve", "another-long-password-x"); !errors.Is(err, ErrBootstrapDone) {
		t.Errorf("second bootstrap = %v, want ErrBootstrapDone", err)
	}

	need, _ = us.NeedsBootstrap(ctx)
	if need {
		t.Error("NeedsBootstrap still true after bootstrap")
	}
}

func TestAuthenticate(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()
	const pw = "correct-horse-battery-staple"

	if _, err := us.Bootstrap(ctx, "bram", "Bram", pw); err != nil {
		t.Fatal(err)
	}

	if _, err := us.Authenticate(ctx, "bram", pw); err != nil {
		t.Errorf("authenticate valid: %v", err)
	}
	// Username matching is case-insensitive.
	if _, err := us.Authenticate(ctx, "BRAM", pw); err != nil {
		t.Errorf("authenticate uppercase username: %v", err)
	}
	if _, err := us.Authenticate(ctx, "bram", "wrong-password-here"); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("authenticate wrong password = %v, want ErrPasswordMismatch", err)
	}
	if _, err := us.Authenticate(ctx, "nobody", pw); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("authenticate unknown user = %v, want ErrUserNotFound", err)
	}
}

func TestDisabledUserCannotAuthenticate(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()
	const pw = "correct-horse-battery-staple"

	if _, err := us.Bootstrap(ctx, "bram", "Bram", pw); err != nil {
		t.Fatal(err)
	}
	admin2, err := us.Create(ctx, "second", "Second", "another-long-password-x", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := us.SetDisabled(ctx, admin2.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := us.Authenticate(ctx, "second", "another-long-password-x"); !errors.Is(err, ErrUserDisabled) {
		t.Errorf("disabled user authenticate = %v, want ErrUserDisabled", err)
	}
}

// Locking out the only administrator on a device that may be physically remote
// is unrecoverable, so the store must refuse it.
func TestCannotRemoveLastAdmin(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()

	u, err := us.Bootstrap(ctx, "bram", "Bram", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}

	if err := us.Delete(ctx, u.ID); err == nil {
		t.Error("deleting the last administrator was allowed")
	}
	if err := us.SetDisabled(ctx, u.ID, true); err == nil {
		t.Error("disabling the last administrator was allowed")
	}
	if err := us.SetRole(ctx, u.ID, RoleViewer); err == nil {
		t.Error("demoting the last administrator was allowed")
	}

	// With a second admin present, all three become legal.
	if _, err := us.Create(ctx, "backup", "Backup", "another-long-password-x", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := us.SetRole(ctx, u.ID, RoleViewer); err != nil {
		t.Errorf("demoting with another admin present: %v", err)
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()
	if _, err := us.Bootstrap(ctx, "bram", "Bram", "correct-horse-battery-staple"); err != nil {
		t.Fatal(err)
	}
	// Different case, same identity.
	if _, err := us.Create(ctx, "BRAM", "Other", "another-long-password-x", RoleEditor); !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate username = %v, want ErrUserExists", err)
	}
}

func TestValidateUsername(t *testing.T) {
	valid := []string{"bram", "b2", "bram.van-boven", "a_b", "User123"}
	invalid := []string{"", "a", ".bram", "bram.", "-bram", "bram-", "has space",
		"has/slash", "../etc", strings.Repeat("a", 33), "emoji😀"}
	for _, v := range valid {
		if err := ValidateUsername(v); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range invalid {
		if err := ValidateUsername(v); err == nil {
			t.Errorf("ValidateUsername(%q) = nil, want error", v)
		}
	}
}

func TestChangePassword(t *testing.T) {
	db := newTestDB(t)
	us := NewUserStore(db, testHasher())
	ctx := context.Background()
	const old = "correct-horse-battery-staple"
	const next = "a-completely-different-one"

	u, err := us.Bootstrap(ctx, "bram", "Bram", old)
	if err != nil {
		t.Fatal(err)
	}
	if err := us.ChangePassword(ctx, u.ID, "not-the-old-password", next); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("change with wrong current = %v, want ErrPasswordMismatch", err)
	}
	if err := us.ChangePassword(ctx, u.ID, old, old); err == nil {
		t.Error("changing to the same password was allowed")
	}
	if err := us.ChangePassword(ctx, u.ID, old, next); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := us.Authenticate(ctx, "bram", next); err != nil {
		t.Errorf("authenticate with new password: %v", err)
	}
	if _, err := us.Authenticate(ctx, "bram", old); err == nil {
		t.Error("old password still works after change")
	}
}

// ---- sessions ------------------------------------------------------------

func setupUser(t *testing.T, db *sql.DB) User {
	t.Helper()
	us := NewUserStore(db, testHasher())
	u, err := us.Bootstrap(context.Background(), "bram", "Bram", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSessionLifecycle(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, time.Hour, 24*time.Hour)
	ctx := context.Background()

	token, sess, err := ss.Create(ctx, u.ID, "100.64.0.2", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if token == "" {
		t.Fatal("empty session token")
	}
	if sess.TokenHash == token {
		t.Fatal("session token stored verbatim; only its hash may be persisted")
	}

	// The raw token must not appear anywhere in the sessions table.
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, token).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("raw session token is queryable in the database")
	}

	got, gotUser, err := ss.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if gotUser.ID != u.ID {
		t.Errorf("looked up user %d, want %d", gotUser.ID, u.ID)
	}
	if got.CSRFSecret != sess.CSRFSecret {
		t.Error("CSRF secret changed between create and lookup")
	}

	if err := ss.Revoke(ctx, token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := ss.Lookup(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("lookup after revoke = %v, want ErrNoSession", err)
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, time.Hour, 2*time.Hour)
	ctx := context.Background()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	ss.SetClock(func() time.Time { return now })

	token, _, err := ss.Create(ctx, u.ID, "100.64.0.2", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ss.Lookup(ctx, token); err != nil {
		t.Fatalf("lookup while fresh: %v", err)
	}

	// Past the absolute lifetime, even with continuous activity.
	now = now.Add(3 * time.Hour)
	if _, _, err := ss.Lookup(ctx, token); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("lookup past absolute expiry = %v, want ErrSessionExpired", err)
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, 30*time.Minute, 30*24*time.Hour)
	ctx := context.Background()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	ss.SetClock(func() time.Time { return now })

	token, _, err := ss.Create(ctx, u.ID, "100.64.0.2", "ua")
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(31 * time.Minute)
	if _, _, err := ss.Lookup(ctx, token); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("lookup past idle timeout = %v, want ErrSessionExpired", err)
	}
}

func TestSessionIdleWindowRefreshedByActivity(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, 30*time.Minute, 30*24*time.Hour)
	ctx := context.Background()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	ss.SetClock(func() time.Time { return now })

	token, _, err := ss.Create(ctx, u.ID, "100.64.0.2", "ua")
	if err != nil {
		t.Fatal(err)
	}
	// Activity every 20 minutes keeps the session alive past the 30 minute window.
	for i := 0; i < 5; i++ {
		now = now.Add(20 * time.Minute)
		if _, _, err := ss.Lookup(ctx, token); err != nil {
			t.Fatalf("lookup at +%d min: %v", (i+1)*20, err)
		}
	}
}

func TestRevokeAllForUserExceptCurrent(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, time.Hour, 24*time.Hour)
	ctx := context.Background()

	keep, _, err := ss.Create(ctx, u.ID, "100.64.0.2", "current")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ss.Create(ctx, u.ID, "100.64.0.3", "other")
	if err != nil {
		t.Fatal(err)
	}

	if err := ss.RevokeAllForUserExcept(ctx, u.ID, keep); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ss.Lookup(ctx, keep); err != nil {
		t.Errorf("current session was revoked: %v", err)
	}
	if _, _, err := ss.Lookup(ctx, other); !errors.Is(err, ErrNoSession) {
		t.Errorf("other session survived = %v, want ErrNoSession", err)
	}
}

func TestSessionCleanup(t *testing.T) {
	db := newTestDB(t)
	u := setupUser(t, db)
	ss := NewSessionStore(db, time.Hour, time.Hour)
	ctx := context.Background()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	ss.SetClock(func() time.Time { return now })
	if _, _, err := ss.Create(ctx, u.ID, "ip", "ua"); err != nil {
		t.Fatal(err)
	}

	now = now.Add(48 * time.Hour)
	n, err := ss.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleanup removed %d sessions, want 1", n)
	}
}

// ---- CSRF ----------------------------------------------------------------

func TestCSRFTokenValidation(t *testing.T) {
	a := Session{CSRFSecret: "secret-a"}
	b := Session{CSRFSecret: "secret-b"}

	ta := CSRFToken(a)
	if ta == "" {
		t.Fatal("empty CSRF token")
	}
	if ta == a.CSRFSecret {
		t.Fatal("CSRF token equals the server-side secret; it must be derived, not the secret itself")
	}
	if CSRFToken(a) != ta {
		t.Error("CSRF token is not deterministic for a session")
	}
	if CSRFToken(b) == ta {
		t.Error("two sessions produced the same CSRF token")
	}

	if err := ValidateCSRF(a, ta); err != nil {
		t.Errorf("validate matching token: %v", err)
	}
	for _, bad := range []string{"", "wrong", ta + "x", CSRFToken(b)} {
		if err := ValidateCSRF(a, bad); !errors.Is(err, ErrCSRF) {
			t.Errorf("ValidateCSRF(%q) = %v, want ErrCSRF", bad, err)
		}
	}
}

func TestCSRFFromRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	r.Header.Set(CSRFHeaderName, "from-header")
	if got := CSRFFromRequest(r); got != "from-header" {
		t.Errorf("header token = %q, want from-header", got)
	}

	form := strings.NewReader(CSRFFormField + "=from-form")
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/x", form)
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := CSRFFromRequest(r2); got != "from-form" {
		t.Errorf("form token = %q, want from-form", got)
	}
}

// ---- cookies -------------------------------------------------------------

func TestSessionCookieFlags(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "tok", CookieOptions{Secure: true, MaxAge: time.Hour})

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookieName {
		t.Errorf("name = %q", c.Name)
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly so JavaScript cannot read it")
	}
	if !c.Secure {
		t.Error("Secure flag not honoured")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}

// The CSRF cookie is the one exception to HttpOnly: the SPA must read it to echo
// it back in a header. If it were HttpOnly the double-submit pattern breaks.
func TestCSRFCookieIsReadableByScript(t *testing.T) {
	w := httptest.NewRecorder()
	SetCSRFCookie(w, "tok", CookieOptions{})
	c := w.Result().Cookies()[0]
	if c.HttpOnly {
		t.Error("CSRF cookie must NOT be HttpOnly; the SPA reads it to set the request header")
	}
}

func TestClearAuthCookies(t *testing.T) {
	w := httptest.NewRecorder()
	ClearAuthCookies(w, false)
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2", len(cookies))
	}
	for _, c := range cookies {
		if c.MaxAge >= 0 {
			t.Errorf("cookie %q MaxAge = %d, want negative to expire it", c.Name, c.MaxAge)
		}
	}
}

// ---- throttling ----------------------------------------------------------

func TestThrottleLocksAfterRepeatedFailures(t *testing.T) {
	db := newTestDB(t)
	th := NewThrottle(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	th.SetClock(func() time.Time { return now })

	const ip, user = "100.64.0.2", "bram"

	for i := 0; i < maxFailuresBeforeLock-1; i++ {
		if err := th.Check(ctx, ip, user); err != nil {
			t.Fatalf("check at failure %d: %v", i, err)
		}
		if err := th.RecordFailure(ctx, ip, user); err != nil {
			t.Fatal(err)
		}
	}
	// The threshold failure triggers the lock.
	if err := th.RecordFailure(ctx, ip, user); err != nil {
		t.Fatal(err)
	}

	var rl *ErrRateLimited
	if err := th.Check(ctx, ip, user); !errors.As(err, &rl) {
		t.Fatalf("check after lock = %v, want ErrRateLimited", err)
	}
	if rl.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive while locked")
	}

	// The lock lifts once the window passes.
	now = now.Add(maxLockout + time.Minute)
	if err := th.Check(ctx, ip, user); err != nil {
		t.Errorf("check after lockout expiry = %v, want nil", err)
	}
}

// An attacker spraying one password across many accounts from one IP must be
// stopped by the IP bucket even though no single account reaches its limit.
func TestThrottleIPBucketCatchesPasswordSpray(t *testing.T) {
	db := newTestDB(t)
	th := NewThrottle(db)
	ctx := context.Background()
	const ip = "100.64.0.9"

	for i := 0; i < maxFailuresBeforeLock; i++ {
		user := "victim" + string(rune('a'+i))
		if err := th.RecordFailure(ctx, ip, user); err != nil {
			t.Fatal(err)
		}
	}
	var rl *ErrRateLimited
	if err := th.Check(ctx, ip, "yet-another-user"); !errors.As(err, &rl) {
		t.Errorf("spray from one IP was not throttled: %v", err)
	}
}

// Conversely, a distributed attack on one account must be stopped by the user
// bucket even though each source IP looks innocent.
func TestThrottleUserBucketCatchesDistributedAttack(t *testing.T) {
	db := newTestDB(t)
	th := NewThrottle(db)
	ctx := context.Background()
	const user = "bram"

	for i := 0; i < maxFailuresBeforeLock; i++ {
		ip := "100.64.1." + string(rune('0'+i))
		if err := th.RecordFailure(ctx, ip, user); err != nil {
			t.Fatal(err)
		}
	}
	var rl *ErrRateLimited
	if err := th.Check(ctx, "100.64.9.9", user); !errors.As(err, &rl) {
		t.Errorf("distributed attack on one account was not throttled: %v", err)
	}
}

func TestThrottleResetOnSuccess(t *testing.T) {
	db := newTestDB(t)
	th := NewThrottle(db)
	ctx := context.Background()
	const ip, user = "100.64.0.2", "bram"

	for i := 0; i < maxFailuresBeforeLock; i++ {
		if err := th.RecordFailure(ctx, ip, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := th.Reset(ctx, ip, user); err != nil {
		t.Fatal(err)
	}
	if err := th.Check(ctx, ip, user); err != nil {
		t.Errorf("check after reset = %v, want nil", err)
	}
}

// A slow trickle of genuine typos spread over hours must not accumulate into a
// lockout for a legitimate operator.
func TestThrottleFailureWindowResets(t *testing.T) {
	db := newTestDB(t)
	th := NewThrottle(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	th.SetClock(func() time.Time { return now })
	const ip, user = "100.64.0.2", "bram"

	for i := 0; i < maxFailuresBeforeLock+3; i++ {
		if err := th.RecordFailure(ctx, ip, user); err != nil {
			t.Fatal(err)
		}
		now = now.Add(failureWindow + time.Minute)
		if err := th.Check(ctx, ip, user); err != nil {
			t.Fatalf("spaced-out failure %d caused a lockout: %v", i, err)
		}
	}
}

func TestLockoutDurationGrowsAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for f := maxFailuresBeforeLock; f < maxFailuresBeforeLock+15; f++ {
		d := lockoutDuration(f)
		if d <= 0 {
			t.Fatalf("lockoutDuration(%d) = %v, must be positive", f, d)
		}
		if d > maxLockout {
			t.Fatalf("lockoutDuration(%d) = %v, exceeds cap %v", f, d, maxLockout)
		}
		if d < prev {
			t.Fatalf("lockoutDuration(%d) = %v decreased from %v", f, d, prev)
		}
		prev = d
	}
	if got := lockoutDuration(1000); got != maxLockout {
		t.Errorf("lockoutDuration(1000) = %v, want the cap %v (overflow guard)", got, maxLockout)
	}
}

// ---- roles ---------------------------------------------------------------

func TestRoleOrdering(t *testing.T) {
	if !RoleAtLeast(RoleAdmin, RoleEditor) {
		t.Error("admin should satisfy editor")
	}
	if !RoleAtLeast(RoleEditor, RoleViewer) {
		t.Error("editor should satisfy viewer")
	}
	if RoleAtLeast(RoleViewer, RoleEditor) {
		t.Error("viewer must not satisfy editor")
	}
	if RoleAtLeast(RoleEditor, RoleAdmin) {
		t.Error("editor must not satisfy admin")
	}
	if RoleAtLeast("nonsense", RoleViewer) {
		t.Error("an unknown role must not satisfy anything")
	}
}
