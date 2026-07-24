package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Cookie names. The __Host- prefix is deliberately NOT used: it requires Secure,
// and the Jetson serves plain HTTP over the Tailscale interface (Tailscale
// provides the transport encryption). Using __Host- would silently break login.
const (
	SessionCookieName = "beermate_session"
	CSRFCookieName    = "beermate_csrf"
	CSRFHeaderName    = "X-BeerMate-CSRF"
	CSRFFormField     = "csrf_token"
)

const (
	sessionTokenBytes = 32 // 256 bits
	csrfSecretBytes   = 32
)

var (
	// ErrNoSession means no valid session was presented.
	ErrNoSession = errors.New("auth: no active session")
	// ErrSessionExpired means the session existed but is past its lifetime.
	ErrSessionExpired = errors.New("auth: session expired")
	// ErrCSRF means the CSRF token was missing or wrong.
	ErrCSRF = errors.New("auth: CSRF validation failed")
)

// Role values, ordered by privilege.
const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
)

var roleRank = map[string]int{RoleViewer: 1, RoleEditor: 2, RoleAdmin: 3}

// RoleAtLeast reports whether have satisfies want.
func RoleAtLeast(have, want string) bool {
	return roleRank[have] >= roleRank[want] && roleRank[have] > 0
}

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { _, ok := roleRank[r]; return ok }

// User is an authenticated principal.
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	MustChangePw bool       `json:"must_change_password"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Session is a server-side login session.
type Session struct {
	TokenHash   string
	UserID      int64
	CSRFSecret  string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	AbsoluteExp time.Time
	IP          string
	UserAgent   string
}

// SessionStore persists sessions.
type SessionStore struct {
	db          *sql.DB
	idleTimeout time.Duration
	maxLifetime time.Duration
	now         func() time.Time
}

// NewSessionStore builds a session store.
func NewSessionStore(db *sql.DB, idleTimeout, maxLifetime time.Duration) *SessionStore {
	return &SessionStore{
		db:          db,
		idleTimeout: idleTimeout,
		maxLifetime: maxLifetime,
		now:         time.Now,
	}
}

// SetClock overrides the time source, for tests.
func (s *SessionStore) SetClock(fn func() time.Time) { s.now = fn }

// hashToken derives the storage key for a session token.
//
// Only this digest is stored. SHA-256 without a salt is correct here (unlike for
// passwords): the token is 256 bits of uniform randomness, so there is no
// dictionary to attack and no benefit to a slow KDF.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("auth: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create issues a new session and returns the raw token (shown to the client once)
// and the CSRF secret.
func (s *SessionStore) Create(ctx context.Context, userID int64, ip, userAgent string) (token string, sess Session, err error) {
	token, err = randomToken(sessionTokenBytes)
	if err != nil {
		return "", Session{}, err
	}
	csrfSecret, err := randomToken(csrfSecretBytes)
	if err != nil {
		return "", Session{}, err
	}

	now := s.now().UTC()
	sess = Session{
		TokenHash:   hashToken(token),
		UserID:      userID,
		CSRFSecret:  csrfSecret,
		CreatedAt:   now,
		LastSeenAt:  now,
		AbsoluteExp: now.Add(s.maxLifetime),
		IP:          ip,
		UserAgent:   truncate(userAgent, 255),
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, csrf_secret, created_at, last_seen_at,
		                      absolute_exp, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.CSRFSecret,
		rfc3339(sess.CreatedAt), rfc3339(sess.LastSeenAt), rfc3339(sess.AbsoluteExp),
		sess.IP, sess.UserAgent)
	if err != nil {
		return "", Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, sess, nil
}

// Lookup resolves a raw token to its session and user, enforcing both the idle
// timeout and the absolute lifetime, and refreshing last_seen_at.
func (s *SessionStore) Lookup(ctx context.Context, token string) (Session, User, error) {
	if token == "" {
		return Session{}, User{}, ErrNoSession
	}
	th := hashToken(token)

	var (
		sess         Session
		u            User
		createdAt    string
		lastSeen     string
		absExp       string
		revokedAt    sql.NullString
		userCreated  string
		lastLogin    sql.NullString
		disabled     int
		mustChangePw int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token_hash, s.user_id, s.csrf_secret, s.created_at, s.last_seen_at,
		       s.absolute_exp, s.ip, s.user_agent, s.revoked_at,
		       u.username, u.display_name, u.role, u.disabled, u.must_change_pw,
		       u.created_at, u.last_login_at
		  FROM sessions s
		  JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`, th).Scan(
		&sess.TokenHash, &sess.UserID, &sess.CSRFSecret, &createdAt, &lastSeen,
		&absExp, &sess.IP, &sess.UserAgent, &revokedAt,
		&u.Username, &u.DisplayName, &u.Role, &disabled, &mustChangePw,
		&userCreated, &lastLogin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, User{}, ErrNoSession
		}
		return Session{}, User{}, fmt.Errorf("auth: lookup session: %w", err)
	}

	if revokedAt.Valid && revokedAt.String != "" {
		return Session{}, User{}, ErrNoSession
	}

	sess.CreatedAt = parseTime(createdAt)
	sess.LastSeenAt = parseTime(lastSeen)
	sess.AbsoluteExp = parseTime(absExp)

	now := s.now().UTC()
	if now.After(sess.AbsoluteExp) {
		_ = s.Revoke(ctx, token)
		return Session{}, User{}, ErrSessionExpired
	}
	if now.Sub(sess.LastSeenAt) > s.idleTimeout {
		_ = s.Revoke(ctx, token)
		return Session{}, User{}, ErrSessionExpired
	}

	u.ID = sess.UserID
	u.Disabled = disabled == 1
	u.MustChangePw = mustChangePw == 1
	u.CreatedAt = parseTime(userCreated)
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		u.LastLoginAt = &t
	}
	if u.Disabled {
		return Session{}, User{}, ErrNoSession
	}

	// Refresh the idle window, but only when it has moved meaningfully. Writing
	// on every request would mean a SQLite write per API call, which on flash
	// storage is avoidable wear for no benefit.
	if now.Sub(sess.LastSeenAt) > time.Minute {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`,
			rfc3339(now), th); err != nil {
			return Session{}, User{}, fmt.Errorf("auth: touch session: %w", err)
		}
		sess.LastSeenAt = now
	}
	return sess, u, nil
}

// Revoke invalidates a single session.
func (s *SessionStore) Revoke(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		rfc3339(s.now().UTC()), hashToken(token))
	return err
}

// RevokeAllForUser invalidates every session belonging to a user. Called on
// password change so a stolen session cannot outlive the credential it came from.
func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		rfc3339(s.now().UTC()), userID)
	return err
}

// RevokeAllForUserExcept revokes a user's sessions apart from the current one,
// so changing your own password does not log you out of the tab you are using.
func (s *SessionStore) RevokeAllForUserExcept(ctx context.Context, userID int64, keepToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?
		  WHERE user_id = ? AND revoked_at IS NULL AND token_hash != ?`,
		rfc3339(s.now().UTC()), userID, hashToken(keepToken))
	return err
}

// Cleanup deletes expired and long-revoked sessions. Bounded growth matters on an
// appliance that runs for years.
func (s *SessionStore) Cleanup(ctx context.Context) (int64, error) {
	cutoff := rfc3339(s.now().UTC().Add(-24 * time.Hour))
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM sessions
		 WHERE absolute_exp < ?
		    OR (revoked_at IS NOT NULL AND revoked_at < ?)`,
		rfc3339(s.now().UTC()), cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ActiveSessions lists a user's live sessions for the settings screen.
func (s *SessionStore) ActiveSessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token_hash, created_at, last_seen_at, absolute_exp, ip, user_agent
		  FROM sessions
		 WHERE user_id = ? AND revoked_at IS NULL AND absolute_exp > ?
		 ORDER BY last_seen_at DESC`, userID, rfc3339(s.now().UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var sess Session
		var c, l, a string
		if err := rows.Scan(&sess.TokenHash, &c, &l, &a, &sess.IP, &sess.UserAgent); err != nil {
			return nil, err
		}
		sess.UserID = userID
		sess.CreatedAt, sess.LastSeenAt, sess.AbsoluteExp = parseTime(c), parseTime(l), parseTime(a)
		out = append(out, sess)
	}
	return out, rows.Err()
}

// ---- CSRF ----------------------------------------------------------------

// CSRFToken derives the token a client must echo back.
//
// This is the double-submit pattern bound to the session: the token is derived
// from the session's server-side secret, so an attacker who can set cookies on
// the victim's browser (but cannot read the session secret) still cannot forge a
// matching pair.
func CSRFToken(sess Session) string {
	sum := sha256.Sum256([]byte("beermate-csrf-v1:" + sess.CSRFSecret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ValidateCSRF compares a presented token against the session's expected token
// in constant time.
func ValidateCSRF(sess Session, presented string) error {
	if presented == "" {
		return ErrCSRF
	}
	want := CSRFToken(sess)
	if subtle.ConstantTimeCompare([]byte(want), []byte(presented)) != 1 {
		return ErrCSRF
	}
	return nil
}

// CSRFFromRequest extracts the token from the header or, for form posts, the body.
func CSRFFromRequest(r *http.Request) string {
	if v := r.Header.Get(CSRFHeaderName); v != "" {
		return v
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(ct, "multipart/form-data") {
		return r.FormValue(CSRFFormField)
	}
	return ""
}

// ---- cookies -------------------------------------------------------------

// CookieOptions controls cookie emission.
type CookieOptions struct {
	Secure bool
	Path   string
	MaxAge time.Duration
}

// SetSessionCookie writes the session cookie.
func SetSessionCookie(w http.ResponseWriter, token string, o CookieOptions) {
	path := o.Path
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     path,
		HttpOnly: true, // never readable from JavaScript
		Secure:   o.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(o.MaxAge.Seconds()),
	})
}

// SetCSRFCookie writes the CSRF cookie. This one is intentionally readable by
// JavaScript: the SPA must echo it back in a header.
func SetCSRFCookie(w http.ResponseWriter, token string, o CookieOptions) {
	path := o.Path
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     path,
		HttpOnly: false,
		Secure:   o.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(o.MaxAge.Seconds()),
	})
}

// ClearAuthCookies expires both cookies on logout.
func ClearAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{SessionCookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == SessionCookieName,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
}

// SessionTokenFromRequest reads the raw session token from the request cookie.
func SessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ---- helpers -------------------------------------------------------------

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
