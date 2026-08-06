package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/content"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
)

// Website session states.
const (
	SessionNone           = "none"
	SessionPreparing      = "preparing"
	SessionActive         = "active"
	SessionExpired        = "expired"
	SessionReauthRequired = "reauth_required"
	SessionError          = "error"
)

// Render modes.
const (
	RenderIframe  = "iframe"
	RenderManaged = "managed"
)

// Website is a configured website slide source.
type Website struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	RenderMode      string     `json:"render_mode"`
	RequiresAuth    bool       `json:"requires_auth"`
	ProfileID       string     `json:"profile_id"`
	SessionState    string     `json:"session_state"`
	LoginURLPattern string     `json:"login_url_pattern"`
	Zoom            float64    `json:"zoom"`
	CaptureMS       int        `json:"capture_interval_ms"`
	LoadTimeoutMS   int        `json:"load_timeout_ms"`
	RefreshOnShow   bool       `json:"refresh_on_show"`
	PeriodicMS      int        `json:"periodic_refresh_ms"`
	AllowInsecure   bool       `json:"allow_insecure"`
	FallbackScene   *int64     `json:"fallback_scene_id,omitempty"`
	LastOKAt        *time.Time `json:"last_ok_at,omitempty"`
	LastValidatedAt *time.Time `json:"last_validated_at,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	LastError       string     `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CreatedBy       string     `json:"created_by"`
}

// Domain returns the URL's host, for the sessions dashboard.
func (w Website) Domain() string {
	u, err := url.Parse(w.URL)
	if err != nil {
		return ""
	}
	return u.Host
}

// WebsiteStore manages website records.
type WebsiteStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewWebsiteStore builds a WebsiteStore.
func NewWebsiteStore(db *dbx.DB) *WebsiteStore {
	return &WebsiteStore{db: db, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *WebsiteStore) SetClock(fn func() time.Time) { s.now = fn }

// profileIDRe restricts a profile identifier to a safe filesystem segment: it
// becomes a directory name under browser-profiles/, so anything path-like must be
// rejected before it can be used to escape that directory.
func validProfileID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Create adds a website.
func (s *WebsiteStore) Create(ctx context.Context, w Website) (Website, error) {
	if strings.TrimSpace(w.Name) == "" {
		return Website{}, errors.New("a website needs a name")
	}
	if err := content.ValidateHTTPURL(w.URL, w.AllowInsecure); err != nil {
		return Website{}, err
	}
	if w.RenderMode != RenderIframe && w.RenderMode != RenderManaged {
		w.RenderMode = RenderIframe
	}
	if w.RequiresAuth {
		// An authenticated site cannot be rendered in a bare iframe: the login
		// cookies live in the managed browser's profile, not in the player page.
		w.RenderMode = RenderManaged
	}
	if w.ProfileID == "" {
		w.ProfileID = "default"
	}
	if !validProfileID(w.ProfileID) {
		return Website{}, errors.New("profile id may contain only letters, digits, dash and underscore")
	}
	if w.Zoom <= 0 {
		w.Zoom = 1.0
	}
	if w.CaptureMS <= 0 {
		w.CaptureMS = 5000
	}
	if w.LoadTimeoutMS <= 0 {
		w.LoadTimeoutMS = 20000
	}

	now := rfc3339(s.now())
	state := SessionNone
	if w.RequiresAuth {
		state = SessionNone
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO websites (name, url, render_mode, requires_auth, profile_id,
			session_state, login_url_pattern, zoom, capture_interval_ms, load_timeout_ms,
			refresh_on_show, periodic_refresh_ms, allow_insecure, fallback_scene_id,
			created_at, updated_at, created_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.Name, w.URL, w.RenderMode, b2i(w.RequiresAuth), w.ProfileID, state,
		w.LoginURLPattern, w.Zoom, w.CaptureMS, w.LoadTimeoutMS, b2i(w.RefreshOnShow),
		w.PeriodicMS, b2i(w.AllowInsecure), w.FallbackScene, now, now, w.CreatedBy)
	if err != nil {
		return Website{}, fmt.Errorf("create website: %w", err)
	}
	w.ID, _ = res.LastInsertId()
	w.SessionState = state
	return w, nil
}

// Get loads one website.
func (s *WebsiteStore) Get(ctx context.Context, id int64) (Website, error) {
	return s.scan(s.db.QueryRowContext(ctx, websiteSelect+` WHERE id = ?`, id))
}

const websiteSelect = `
	SELECT id, name, url, render_mode, requires_auth, profile_id, session_state,
	       login_url_pattern, zoom, capture_interval_ms, load_timeout_ms,
	       refresh_on_show, periodic_refresh_ms, allow_insecure, fallback_scene_id,
	       last_ok_at, last_validated_at, last_login_at, last_error,
	       created_at, updated_at, created_by
	  FROM websites`

func (s *WebsiteStore) scan(row *sql.Row) (Website, error) {
	var (
		w                                Website
		requiresAuth, refreshOnShow      int
		allowInsecure                    int
		fallback                         sql.NullInt64
		lastOK, lastValidated, lastLogin sql.NullString
		created, updated                 string
	)
	err := row.Scan(&w.ID, &w.Name, &w.URL, &w.RenderMode, &requiresAuth, &w.ProfileID,
		&w.SessionState, &w.LoginURLPattern, &w.Zoom, &w.CaptureMS, &w.LoadTimeoutMS,
		&refreshOnShow, &w.PeriodicMS, &allowInsecure, &fallback,
		&lastOK, &lastValidated, &lastLogin, &w.LastError, &created, &updated, &w.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Website{}, ErrNotFound
	}
	if err != nil {
		return Website{}, err
	}
	w.RequiresAuth = requiresAuth == 1
	w.RefreshOnShow = refreshOnShow == 1
	w.AllowInsecure = allowInsecure == 1
	if fallback.Valid {
		w.FallbackScene = &fallback.Int64
	}
	w.CreatedAt, w.UpdatedAt = parseTime(created), parseTime(updated)
	if lastOK.Valid {
		t := parseTime(lastOK.String)
		w.LastOKAt = &t
	}
	if lastValidated.Valid {
		t := parseTime(lastValidated.String)
		w.LastValidatedAt = &t
	}
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		w.LastLoginAt = &t
	}
	return w, nil
}

// List returns all websites.
func (s *WebsiteStore) List(ctx context.Context) ([]Website, error) {
	rows, err := s.db.QueryContext(ctx, websiteSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Website
	for rows.Next() {
		var (
			w                                Website
			requiresAuth, refreshOnShow      int
			allowInsecure                    int
			fallback                         sql.NullInt64
			lastOK, lastValidated, lastLogin sql.NullString
			created, updated                 string
		)
		if err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.RenderMode, &requiresAuth, &w.ProfileID,
			&w.SessionState, &w.LoginURLPattern, &w.Zoom, &w.CaptureMS, &w.LoadTimeoutMS,
			&refreshOnShow, &w.PeriodicMS, &allowInsecure, &fallback,
			&lastOK, &lastValidated, &lastLogin, &w.LastError, &created, &updated, &w.CreatedBy); err != nil {
			return nil, err
		}
		w.RequiresAuth = requiresAuth == 1
		w.RefreshOnShow = refreshOnShow == 1
		w.AllowInsecure = allowInsecure == 1
		if fallback.Valid {
			w.FallbackScene = &fallback.Int64
		}
		w.CreatedAt, w.UpdatedAt = parseTime(created), parseTime(updated)
		if lastOK.Valid {
			t := parseTime(lastOK.String)
			w.LastOKAt = &t
		}
		if lastValidated.Valid {
			t := parseTime(lastValidated.String)
			w.LastValidatedAt = &t
		}
		if lastLogin.Valid {
			t := parseTime(lastLogin.String)
			w.LastLoginAt = &t
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Update saves editable fields.
func (s *WebsiteStore) Update(ctx context.Context, w Website) error {
	if err := content.ValidateHTTPURL(w.URL, w.AllowInsecure); err != nil {
		return err
	}
	if w.RequiresAuth {
		w.RenderMode = RenderManaged
	}
	if !validProfileID(w.ProfileID) {
		return errors.New("invalid profile id")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE websites SET name=?, url=?, render_mode=?, requires_auth=?, profile_id=?,
			login_url_pattern=?, zoom=?, capture_interval_ms=?, load_timeout_ms=?,
			refresh_on_show=?, periodic_refresh_ms=?, allow_insecure=?, fallback_scene_id=?,
			updated_at=? WHERE id=?`,
		w.Name, w.URL, w.RenderMode, b2i(w.RequiresAuth), w.ProfileID, w.LoginURLPattern,
		w.Zoom, w.CaptureMS, w.LoadTimeoutMS, b2i(w.RefreshOnShow), w.PeriodicMS,
		b2i(w.AllowInsecure), w.FallbackScene, rfc3339(s.now()), w.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSessionState transitions the managed-session lifecycle and records the
// reason. Cookie values, tokens and form contents are never passed here: only
// the state name and a non-sensitive note.
func (s *WebsiteStore) SetSessionState(ctx context.Context, id int64, state, note string) error {
	now := rfc3339(s.now())
	var extra string
	switch state {
	case SessionActive:
		extra = ", last_ok_at = '" + now + "', last_validated_at = '" + now + "', last_login_at = '" + now + "'"
	}
	// #nosec G201 -- `extra` is built only from the constant fragments above.
	q := `UPDATE websites SET session_state = ?, last_error = ?, updated_at = ?` + extra + ` WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, state, note, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkValidated records a successful session validation.
func (s *WebsiteStore) MarkValidated(ctx context.Context, id int64) error {
	now := rfc3339(s.now())
	_, err := s.db.ExecContext(ctx,
		`UPDATE websites SET session_state = ?, last_validated_at = ?, last_ok_at = ?,
		        last_error = '', updated_at = ? WHERE id = ?`,
		SessionActive, now, now, now, id)
	return err
}

// Delete removes a website.
func (s *WebsiteStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM websites WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// WarningCount returns how many authenticated sites need reauthentication, for
// the health endpoint and overview.
func (s *WebsiteStore) WarningCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM websites WHERE session_state IN (?, ?, ?)`,
		SessionExpired, SessionReauthRequired, SessionError).Scan(&n)
	return n, err
}
