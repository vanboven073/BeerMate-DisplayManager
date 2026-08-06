package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Throttle configuration.
//
// Two independent subjects are throttled per login attempt: the source IP and the
// submitted username. Throttling the IP alone lets a botnet spread a password-
// spray across many addresses; throttling the username alone lets an attacker
// lock out a known account at will. Requiring both to be clear covers each gap
// without the other's weakness.
const (
	maxFailuresBeforeLock = 5
	failureWindow         = 15 * time.Minute
	baseLockout           = 30 * time.Second
	maxLockout            = 30 * time.Minute
)

// ErrRateLimited is returned when a subject is currently locked out.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("auth: too many failed attempts, retry after %s", e.RetryAfter.Round(time.Second))
}

// Throttle records and enforces login failure limits.
type Throttle struct {
	db  *sql.DB
	now func() time.Time
}

// NewThrottle builds a Throttle.
func NewThrottle(db *sql.DB) *Throttle {
	return &Throttle{db: db, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (t *Throttle) SetClock(fn func() time.Time) { t.now = fn }

func ipSubject(ip string) string { return "ip:" + ip }
func userSubject(username string) string {
	return "user:" + strings.ToLower(strings.TrimSpace(username))
}

// Check reports whether a login attempt from ip for username may proceed.
func (t *Throttle) Check(ctx context.Context, ip, username string) error {
	for _, subject := range []string{ipSubject(ip), userSubject(username)} {
		retry, err := t.lockedFor(ctx, subject)
		if err != nil {
			return err
		}
		if retry > 0 {
			return &ErrRateLimited{RetryAfter: retry}
		}
	}
	return nil
}

func (t *Throttle) lockedFor(ctx context.Context, subject string) (time.Duration, error) {
	var lockedUntil sql.NullString
	err := t.db.QueryRowContext(ctx,
		`SELECT locked_until FROM login_attempts WHERE subject = ?`, subject).Scan(&lockedUntil)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("auth: throttle check: %w", err)
	}
	if !lockedUntil.Valid || lockedUntil.String == "" {
		return 0, nil
	}
	until := parseTime(lockedUntil.String)
	if remaining := until.Sub(t.now().UTC()); remaining > 0 {
		return remaining, nil
	}
	return 0, nil
}

// RecordFailure increments the failure counters and applies exponential lockout.
func (t *Throttle) RecordFailure(ctx context.Context, ip, username string) error {
	for _, subject := range []string{ipSubject(ip), userSubject(username)} {
		if err := t.recordOne(ctx, subject); err != nil {
			return err
		}
	}
	return nil
}

func (t *Throttle) recordOne(ctx context.Context, subject string) error {
	now := t.now().UTC()

	var (
		failures  int
		firstFail sql.NullString
	)
	err := t.db.QueryRowContext(ctx,
		`SELECT failures, first_fail FROM login_attempts WHERE subject = ?`, subject).
		Scan(&failures, &firstFail)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		failures = 0
	case err != nil:
		return fmt.Errorf("auth: throttle read: %w", err)
	default:
		// Reset the counter if the previous failures are outside the window, so a
		// slow trickle of typos never accumulates into a permanent lockout.
		if firstFail.Valid && now.Sub(parseTime(firstFail.String)) > failureWindow {
			failures = 0
		}
	}
	failures++

	var lockedUntil any
	if failures >= maxFailuresBeforeLock {
		lockedUntil = rfc3339(now.Add(lockoutDuration(failures)))
	}

	first := rfc3339(now)
	if failures > 1 && firstFail.Valid && firstFail.String != "" {
		first = firstFail.String
	}

	_, err = t.db.ExecContext(ctx, `
		INSERT INTO login_attempts (subject, failures, first_fail, last_fail, locked_until)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(subject) DO UPDATE SET
			failures     = excluded.failures,
			first_fail   = excluded.first_fail,
			last_fail    = excluded.last_fail,
			locked_until = excluded.locked_until`,
		subject, failures, first, rfc3339(now), lockedUntil)
	if err != nil {
		return fmt.Errorf("auth: throttle write: %w", err)
	}
	return nil
}

// lockoutDuration grows exponentially from baseLockout, capped at maxLockout.
func lockoutDuration(failures int) time.Duration {
	excess := failures - maxFailuresBeforeLock
	if excess < 0 {
		excess = 0
	}
	if excess > 20 { // guard against overflow in the shift below
		return maxLockout
	}
	d := time.Duration(float64(baseLockout) * math.Pow(2, float64(excess)))
	if d > maxLockout || d <= 0 {
		return maxLockout
	}
	return d
}

// Reset clears counters after a successful login.
func (t *Throttle) Reset(ctx context.Context, ip, username string) error {
	_, err := t.db.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE subject IN (?, ?)`,
		ipSubject(ip), userSubject(username))
	return err
}

// Cleanup removes stale throttle rows.
func (t *Throttle) Cleanup(ctx context.Context) (int64, error) {
	cutoff := rfc3339(t.now().UTC().Add(-24 * time.Hour))
	res, err := t.db.ExecContext(ctx,
		`DELETE FROM login_attempts
		  WHERE last_fail < ? AND (locked_until IS NULL OR locked_until < ?)`,
		cutoff, rfc3339(t.now().UTC()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
