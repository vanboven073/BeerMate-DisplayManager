package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/schedule"
)

// ---- settings ------------------------------------------------------------

// SettingsStore holds arbitrary JSON-valued configuration.
type SettingsStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewSettingsStore builds a SettingsStore.
func NewSettingsStore(db *dbx.DB) *SettingsStore {
	return &SettingsStore{db: db, now: time.Now}
}

// Get decodes a setting into dst. Returns ErrNotFound when unset.
func (s *SettingsStore) Get(ctx context.Context, key string, dst any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dst)
}

// GetString reads a string setting, returning def when unset.
func (s *SettingsStore) GetString(ctx context.Context, key, def string) string {
	var v string
	if err := s.Get(ctx, key, &v); err != nil {
		return def
	}
	return v
}

// Set stores a setting.
func (s *SettingsStore) Set(ctx context.Context, key string, value any, actor string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value_json, updated_at, updated_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		key, string(raw), rfc3339(s.now()), actor)
	return err
}

// All returns every setting as raw JSON.
func (s *SettingsStore) All(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value_json FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

// ---- schedule ------------------------------------------------------------

// ScheduleStore manages schedule rules and manual overrides.
type ScheduleStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewScheduleStore builds a ScheduleStore.
func NewScheduleStore(db *dbx.DB) *ScheduleStore {
	return &ScheduleStore{db: db, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *ScheduleStore) SetClock(fn func() time.Time) { s.now = fn }

// Rules returns all schedule rules.
func (s *ScheduleStore) Rules(ctx context.Context) ([]schedule.Rule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, weekday, on_date, enabled, on_time, off_time, label
		  FROM schedule_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []schedule.Rule
	for rows.Next() {
		var (
			r       schedule.Rule
			weekday sql.NullInt64
			onDate  sql.NullString
			onTime  sql.NullString
			offTime sql.NullString
			enabled int
		)
		if err := rows.Scan(&r.ID, &r.Kind, &weekday, &onDate, &enabled,
			&onTime, &offTime, &r.Label); err != nil {
			return nil, err
		}
		if weekday.Valid {
			w := int(weekday.Int64)
			r.Weekday = &w
		}
		r.OnDate = onDate.String
		r.OnTime = onTime.String
		r.OffTime = offTime.String
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	schedule.SortRules(out)
	return out, nil
}

// UpsertRule creates or updates a rule.
func (s *ScheduleStore) UpsertRule(ctx context.Context, r schedule.Rule) (int64, error) {
	if err := schedule.ValidateRule(r); err != nil {
		return 0, err
	}
	now := rfc3339(s.now())

	var weekday, onDate, onTime, offTime any
	if r.Weekday != nil {
		weekday = *r.Weekday
	}
	if r.OnDate != "" {
		onDate = r.OnDate
	}
	if r.OnTime != "" {
		onTime = r.OnTime
	}
	if r.OffTime != "" {
		offTime = r.OffTime
	}

	if r.ID > 0 {
		_, err := s.db.ExecContext(ctx, `
			UPDATE schedule_rules
			   SET kind=?, weekday=?, on_date=?, enabled=?, on_time=?, off_time=?,
			       label=?, updated_at=?
			 WHERE id=?`,
			r.Kind, weekday, onDate, b2i(r.Enabled), onTime, offTime, r.Label, now, r.ID)
		return r.ID, err
	}

	// The partial unique indexes make one rule per weekday and per date, so an
	// upsert on those keys is the natural behaviour rather than an error.
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO schedule_rules (kind, weekday, on_date, enabled, on_time, off_time,
		                            label, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		r.Kind, weekday, onDate, b2i(r.Enabled), onTime, offTime, r.Label, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			// Update the existing rule for this weekday or date instead.
			if r.Kind == schedule.KindWeekly {
				_, uerr := s.db.ExecContext(ctx, `
					UPDATE schedule_rules SET enabled=?, on_time=?, off_time=?, label=?, updated_at=?
					 WHERE kind='weekly' AND weekday=?`,
					b2i(r.Enabled), onTime, offTime, r.Label, now, weekday)
				return 0, uerr
			}
			_, uerr := s.db.ExecContext(ctx, `
				UPDATE schedule_rules SET enabled=?, on_time=?, off_time=?, label=?, updated_at=?
				 WHERE kind='date' AND on_date=?`,
				b2i(r.Enabled), onTime, offTime, r.Label, now, onDate)
			return 0, uerr
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// DeleteRule removes a rule. Weekly rules are kept (disabled instead) so the
// seven-day grid in the UI never develops holes.
func (s *ScheduleStore) DeleteRule(ctx context.Context, id int64) error {
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT kind FROM schedule_rules WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind == schedule.KindWeekly {
		_, err := s.db.ExecContext(ctx,
			`UPDATE schedule_rules SET enabled = 0, updated_at = ? WHERE id = ?`,
			rfc3339(s.now()), id)
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM schedule_rules WHERE id = ?`, id)
	return err
}

// ActiveOverrides returns overrides that have not been cleared or expired.
func (s *ScheduleStore) ActiveOverrides(ctx context.Context) ([]schedule.Override, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mode, starts_at, expires_at, created_by
		  FROM schedule_overrides
		 WHERE cleared_at IS NULL AND expires_at > ?
		 ORDER BY starts_at DESC`, rfc3339(s.now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []schedule.Override
	for rows.Next() {
		var o schedule.Override
		var starts, expires string
		if err := rows.Scan(&o.ID, &o.Mode, &starts, &expires, &o.CreatedBy); err != nil {
			return nil, err
		}
		o.StartsAt, o.ExpiresAt = parseTime(starts), parseTime(expires)
		out = append(out, o)
	}
	return out, rows.Err()
}

// MaxOverrideDuration caps a manual override. An override that never expires
// would let an operator leave the screen on indefinitely by accident, which is
// exactly what the schedule exists to prevent.
const MaxOverrideDuration = 12 * time.Hour

// CreateOverride records a manual wake or sleep.
func (s *ScheduleStore) CreateOverride(ctx context.Context, mode string, d time.Duration, actor string) (schedule.Override, error) {
	if mode != "wake" && mode != "sleep" {
		return schedule.Override{}, fmt.Errorf("override mode must be wake or sleep, got %q", mode)
	}
	if d <= 0 {
		return schedule.Override{}, errors.New("override duration must be positive")
	}
	if d > MaxOverrideDuration {
		return schedule.Override{}, fmt.Errorf("override duration must be at most %s", MaxOverrideDuration)
	}

	now := s.now().UTC()
	o := schedule.Override{
		Mode: mode, StartsAt: now, ExpiresAt: now.Add(d), CreatedBy: actor,
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO schedule_overrides (mode, starts_at, expires_at, created_by, created_at)
		VALUES (?,?,?,?,?)`,
		mode, rfc3339(o.StartsAt), rfc3339(o.ExpiresAt), actor, rfc3339(now))
	if err != nil {
		return schedule.Override{}, err
	}
	o.ID, _ = res.LastInsertId()
	return o, nil
}

// ClearOverrides cancels all active overrides.
func (s *ScheduleStore) ClearOverrides(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE schedule_overrides SET cleared_at = ? WHERE cleared_at IS NULL`,
		rfc3339(s.now()))
	return err
}

// PruneOverrides deletes long-expired override rows.
func (s *ScheduleStore) PruneOverrides(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM schedule_overrides WHERE expires_at < ?`,
		rfc3339(s.now().Add(-7*24*time.Hour)))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ---- emergency messages --------------------------------------------------

// Emergency is a priority message that overrides the playlist.
type Emergency struct {
	ID        int64      `json:"id"`
	Heading   string     `json:"heading"`
	Body      string     `json:"body"`
	Severity  string     `json:"severity"`
	StartsAt  time.Time  `json:"starts_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Dismissed *time.Time `json:"dismissed_at,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

// MaxEmergencyDuration caps how long an override can run unattended.
const MaxEmergencyDuration = 24 * time.Hour

// EmergencyStore manages priority messages.
type EmergencyStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewEmergencyStore builds an EmergencyStore.
func NewEmergencyStore(db *dbx.DB) *EmergencyStore {
	return &EmergencyStore{db: db, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *EmergencyStore) SetClock(fn func() time.Time) { s.now = fn }

// Create raises an emergency message.
func (s *EmergencyStore) Create(ctx context.Context, e Emergency) (Emergency, error) {
	if strings.TrimSpace(e.Heading) == "" {
		return Emergency{}, errors.New("an emergency message needs a heading")
	}
	switch e.Severity {
	case "info", "warning", "critical":
	default:
		return Emergency{}, fmt.Errorf("severity must be info, warning or critical, got %q", e.Severity)
	}
	now := s.now().UTC()
	if e.StartsAt.IsZero() {
		e.StartsAt = now
	}
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = now.Add(time.Hour)
	}
	if !e.ExpiresAt.After(e.StartsAt) {
		return Emergency{}, errors.New("expiry must be after the start time")
	}
	if e.ExpiresAt.Sub(e.StartsAt) > MaxEmergencyDuration {
		return Emergency{}, fmt.Errorf("an emergency message may run for at most %s", MaxEmergencyDuration)
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO emergency_messages (heading, body, severity, starts_at, expires_at,
		                                created_by, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		e.Heading, e.Body, e.Severity, rfc3339(e.StartsAt), rfc3339(e.ExpiresAt),
		e.CreatedBy, rfc3339(now))
	if err != nil {
		return Emergency{}, err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt = now
	return e, nil
}

// Active returns the emergency message in force now, if any.
func (s *EmergencyStore) Active(ctx context.Context) (Emergency, bool, error) {
	now := rfc3339(s.now())
	var (
		e         Emergency
		starts    string
		expires   string
		created   string
		dismissed sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, heading, body, severity, starts_at, expires_at, dismissed_at,
		       created_by, created_at
		  FROM emergency_messages
		 WHERE dismissed_at IS NULL AND starts_at <= ? AND expires_at > ?
		 ORDER BY
		   CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
		   created_at DESC
		 LIMIT 1`, now, now).
		Scan(&e.ID, &e.Heading, &e.Body, &e.Severity, &starts, &expires,
			&dismissed, &e.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Emergency{}, false, nil
	}
	if err != nil {
		return Emergency{}, false, err
	}
	e.StartsAt, e.ExpiresAt, e.CreatedAt = parseTime(starts), parseTime(expires), parseTime(created)
	return e, true, nil
}

// List returns recent emergency messages.
func (s *EmergencyStore) List(ctx context.Context, limit int) ([]Emergency, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, heading, body, severity, starts_at, expires_at, dismissed_at,
		       created_by, created_at
		  FROM emergency_messages ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Emergency
	for rows.Next() {
		var (
			e                        Emergency
			starts, expires, created string
			dismissed                sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.Heading, &e.Body, &e.Severity, &starts, &expires,
			&dismissed, &e.CreatedBy, &created); err != nil {
			return nil, err
		}
		e.StartsAt, e.ExpiresAt, e.CreatedAt = parseTime(starts), parseTime(expires), parseTime(created)
		if dismissed.Valid {
			t := parseTime(dismissed.String)
			e.Dismissed = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Dismiss cancels an emergency message.
func (s *EmergencyStore) Dismiss(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE emergency_messages SET dismissed_at = ? WHERE id = ? AND dismissed_at IS NULL`,
		rfc3339(s.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- audit ---------------------------------------------------------------

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	ActorName  string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	IP         string    `json:"ip,omitempty"`
}

// AuditStore records sensitive actions.
type AuditStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewAuditStore builds an AuditStore.
func NewAuditStore(db *dbx.DB) *AuditStore {
	return &AuditStore{db: db, now: time.Now}
}

// Record writes an audit entry.
//
// Detail is free text written by callers. It must never contain a credential:
// the logging redaction layer does not cover the database, so this is enforced
// by convention at the call sites and reviewed as part of the security model.
func (s *AuditStore) Record(ctx context.Context, e AuditEntry) error {
	var actorID any
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (created_at, actor_id, actor_name, action, target_type,
		                       target_id, detail, ip)
		VALUES (?,?,?,?,?,?,?,?)`,
		rfc3339(s.now()), actorID, e.ActorName, e.Action, e.TargetType,
		e.TargetID, truncate(e.Detail, 2000), e.IP)
	return err
}

// List returns recent audit entries.
func (s *AuditStore) List(ctx context.Context, limit int, action string) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, created_at, actor_name, action, target_type, target_id, detail, ip
	        FROM audit_log`
	args := []any{}
	if action != "" {
		q += ` WHERE action = ?`
		args = append(args, action)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var created string
		if err := rows.Scan(&e.ID, &created, &e.ActorName, &e.Action,
			&e.TargetType, &e.TargetID, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune keeps the audit log bounded on a device that runs for years.
func (s *AuditStore) Prune(ctx context.Context, keep int) (int64, error) {
	if keep < 1000 {
		keep = 1000
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM audit_log WHERE id NOT IN (
			SELECT id FROM audit_log ORDER BY id DESC LIMIT ?
		)`, keep)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
