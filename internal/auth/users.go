package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrUserNotFound means no such user.
	ErrUserNotFound = errors.New("auth: user not found")
	// ErrUserExists means the username is taken.
	ErrUserExists = errors.New("auth: username already exists")
	// ErrUserDisabled means the account exists but is disabled.
	ErrUserDisabled = errors.New("auth: account is disabled")
	// ErrBootstrapDone means an administrator already exists.
	ErrBootstrapDone = errors.New("auth: initial administrator already created")
)

// 2-32 characters: an alphanumeric first and last character with dots, dashes and
// underscores permitted only in between. Anchoring both ends keeps usernames from
// resembling paths or option flags anywhere they are echoed back.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,30}[a-zA-Z0-9]$`)

// ValidateUsername enforces the username policy.
func ValidateUsername(u string) error {
	u = strings.TrimSpace(u)
	if !usernameRe.MatchString(u) {
		return errors.New("username must be 2-32 characters using letters, digits, dot, dash or underscore, " +
			"and must start and end with a letter or digit")
	}
	return nil
}

// UserStore manages user records.
type UserStore struct {
	db     *sql.DB
	hasher *Hasher
	now    func() time.Time
}

// NewUserStore builds a UserStore.
func NewUserStore(db *sql.DB, h *Hasher) *UserStore {
	return &UserStore{db: db, hasher: h, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *UserStore) SetClock(fn func() time.Time) { s.now = fn }

// Count returns the number of users.
func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// NeedsBootstrap reports whether the instance has no users yet.
func (s *UserStore) NeedsBootstrap(ctx context.Context) (bool, error) {
	n, err := s.Count(ctx)
	return n == 0, err
}

// Bootstrap creates the first administrator.
//
// It fails if any user already exists, so the bootstrap endpoint cannot be used
// to add a second privileged account after setup. The check and the insert share
// one transaction, closing the race between two simultaneous bootstrap requests.
func (s *UserStore) Bootstrap(ctx context.Context, username, displayName, password string) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrBootstrapDone
	}

	now := rfc3339(s.now().UTC())
	res, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, username_lower, display_name, password_hash, role,
		                   disabled, must_change_pw, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'admin', 0, 0, ?, ?)`,
		username, strings.ToLower(username), displayName, hash, now, now)
	if err != nil {
		return User{}, fmt.Errorf("auth: bootstrap insert: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return User{}, err
	}

	return User{
		ID: id, Username: username, DisplayName: displayName,
		Role: RoleAdmin, CreatedAt: s.now().UTC(),
	}, nil
}

// Authenticate verifies credentials and returns the user.
//
// A dummy hash is verified when the user does not exist so that the response time
// for "unknown user" matches "wrong password"; otherwise the timing difference
// enumerates valid usernames.
func (s *UserStore) Authenticate(ctx context.Context, username, password string) (User, error) {
	var (
		u            User
		hash         string
		disabled     int
		mustChangePw int
		createdAt    string
		lastLogin    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, display_name, password_hash, role, disabled,
		       must_change_pw, created_at, last_login_at
		  FROM users WHERE username_lower = ?`,
		strings.ToLower(strings.TrimSpace(username))).
		Scan(&u.ID, &u.Username, &u.DisplayName, &hash, &u.Role, &disabled,
			&mustChangePw, &createdAt, &lastLogin)

	if errors.Is(err, sql.ErrNoRows) {
		_ = s.hasher.Verify(password, dummyHash)
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: authenticate lookup: %w", err)
	}

	if err := s.hasher.Verify(password, hash); err != nil {
		return User{}, ErrPasswordMismatch
	}
	if disabled == 1 {
		return User{}, ErrUserDisabled
	}

	u.Disabled = false
	u.MustChangePw = mustChangePw == 1
	u.CreatedAt = parseTime(createdAt)
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		u.LastLoginAt = &t
	}

	// Transparently upgrade hashes created under weaker parameters.
	if s.hasher.NeedsRehash(hash) {
		if newHash, err := s.hasher.Hash(password); err == nil {
			_, _ = s.db.ExecContext(ctx,
				`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
				newHash, rfc3339(s.now().UTC()), u.ID)
		}
	}
	return u, nil
}

// dummyHash is a real Argon2id hash of an unguessable value, used only to keep
// the failure path's timing indistinguishable from the success path's.
const dummyHash = "$argon2id$v=19$m=65536,t=2,p=4$c29tZXNhbHRzb21lc2FsdA$" +
	"J8yTQnYt0y1QW0xUZ0kZ1H1yqCk5o0xF3xL3G8mFbQY"

// RecordLogin stamps the last login time.
func (s *UserStore) RecordLogin(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`,
		rfc3339(s.now().UTC()), rfc3339(s.now().UTC()), userID)
	return err
}

// ChangePassword verifies the current password and sets a new one.
func (s *UserStore) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if err := s.hasher.Verify(current, hash); err != nil {
		return ErrPasswordMismatch
	}
	if current == next {
		return errors.New("new password must differ from the current password")
	}
	return s.SetPassword(ctx, userID, next)
}

// SetPassword replaces a password without checking the old one. Used by an admin
// resetting another user's password, and by ChangePassword after verification.
func (s *UserStore) SetPassword(ctx context.Context, userID int64, next string) error {
	newHash, err := s.hasher.Hash(next)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_pw = 0, updated_at = ? WHERE id = ?`,
		newHash, rfc3339(s.now().UTC()), userID)
	if err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Create adds a user. Only an administrator may call this.
func (s *UserStore) Create(ctx context.Context, username, displayName, password, role string) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if !ValidRole(role) {
		return User{}, fmt.Errorf("auth: unknown role %q", role)
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return User{}, err
	}
	now := rfc3339(s.now().UTC())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, username_lower, display_name, password_hash, role,
		                   disabled, must_change_pw, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 1, ?, ?)`,
		username, strings.ToLower(username), displayName, hash, role, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrUserExists
		}
		return User{}, fmt.Errorf("auth: create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return User{
		ID: id, Username: username, DisplayName: displayName, Role: role,
		MustChangePw: true, CreatedAt: s.now().UTC(),
	}, nil
}

// Get loads a user by ID.
func (s *UserStore) Get(ctx context.Context, id int64) (User, error) {
	var (
		u            User
		disabled     int
		mustChangePw int
		createdAt    string
		lastLogin    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, display_name, role, disabled, must_change_pw,
		       created_at, last_login_at
		  FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &disabled,
			&mustChangePw, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.Disabled = disabled == 1
	u.MustChangePw = mustChangePw == 1
	u.CreatedAt = parseTime(createdAt)
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		u.LastLoginAt = &t
	}
	return u, nil
}

// List returns all users ordered by username.
func (s *UserStore) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, display_name, role, disabled, must_change_pw,
		       created_at, last_login_at
		  FROM users ORDER BY username_lower`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var (
			u            User
			disabled     int
			mustChangePw int
			createdAt    string
			lastLogin    sql.NullString
		)
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &disabled,
			&mustChangePw, &createdAt, &lastLogin); err != nil {
			return nil, err
		}
		u.Disabled = disabled == 1
		u.MustChangePw = mustChangePw == 1
		u.CreatedAt = parseTime(createdAt)
		if lastLogin.Valid {
			t := parseTime(lastLogin.String)
			u.LastLoginAt = &t
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetRole changes a user's role.
func (s *UserStore) SetRole(ctx context.Context, id int64, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("auth: unknown role %q", role)
	}
	if role != RoleAdmin {
		if err := s.guardLastAdmin(ctx, id); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`,
		role, rfc3339(s.now().UTC()), id)
	return err
}

// SetDisabled enables or disables an account.
func (s *UserStore) SetDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		if err := s.guardLastAdmin(ctx, id); err != nil {
			return err
		}
	}
	v := 0
	if disabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`,
		v, rfc3339(s.now().UTC()), id)
	return err
}

// Delete removes a user.
func (s *UserStore) Delete(ctx context.Context, id int64) error {
	if err := s.guardLastAdmin(ctx, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// guardLastAdmin refuses an operation that would leave the system with no enabled
// administrator, which would lock everyone out of an appliance that may be
// physically remote.
func (s *UserStore) guardLastAdmin(ctx context.Context, id int64) error {
	var role string
	var disabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT role, disabled FROM users WHERE id = ?`, id).Scan(&role, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if role != RoleAdmin || disabled == 1 {
		return nil
	}
	var others int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0 AND id != ?`,
		id).Scan(&others); err != nil {
		return err
	}
	if others == 0 {
		return errors.New("cannot remove or demote the last enabled administrator")
	}
	return nil
}
