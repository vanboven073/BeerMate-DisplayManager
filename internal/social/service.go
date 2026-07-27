package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/secrets"
)

// Connection states.
const (
	StateIdle         = "idle"
	StateOK           = "ok"
	StateError        = "error"
	StateUnauthorised = "unauthorised"
	StateRateLimited  = "rate_limited"
)

// Feed is a stored social feed configuration.
type Feed struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Platform        string     `json:"platform"`
	Source          string     `json:"source"`
	Config          string     `json:"config"`
	CredentialID    *int64     `json:"credential_id,omitempty"`
	Enabled         bool       `json:"enabled"`
	RefreshSec      int        `json:"refresh_sec"`
	MaxItems        int        `json:"max_items"`
	IncludeMedia    bool       `json:"include_media"`
	IncludeText     bool       `json:"include_text"`
	Moderation      string     `json:"moderation"`
	HashtagFilter   string     `json:"hashtag_filter"`
	KeywordFilter   string     `json:"keyword_filter"`
	Blocklist       string     `json:"blocklist"`
	Template        string     `json:"template"`
	ConnectionState string     `json:"connection_state"`
	LastRefreshAt   *time.Time `json:"last_refresh_at,omitempty"`
	LastError       string     `json:"last_error"`
	Failures        int        `json:"consecutive_failures"`
	NextAttemptAt   *time.Time `json:"next_attempt_at,omitempty"`
}

// StoredPost is a cached post row.
type StoredPost struct {
	ID              int64      `json:"id"`
	FeedID          int64      `json:"feed_id"`
	ExternalID      string     `json:"external_id"`
	Author          string     `json:"author"`
	AuthorHandle    string     `json:"author_handle"`
	AvatarURL       string     `json:"avatar_url"`
	Text            string     `json:"text"`
	MediaURL        string     `json:"media_url"`
	MediaKind       string     `json:"media_kind"`
	Permalink       string     `json:"permalink"`
	PostedAt        *time.Time `json:"posted_at,omitempty"`
	FetchedAt       time.Time  `json:"fetched_at"`
	ModerationState string     `json:"moderation_state"`
	Pinned          bool       `json:"pinned"`
}

// Service manages feeds, their credentials and the cached, moderated posts.
type Service struct {
	db       *dbx.DB
	box      *secrets.Box
	log      *slog.Logger
	cacheMax int
	now      func() time.Time
	onChange func(feedID int64, reason string)
}

// NewService builds the social Service.
func NewService(db *dbx.DB, box *secrets.Box, cacheMax int, log *slog.Logger) *Service {
	if cacheMax < 1 {
		cacheMax = 200
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, box: box, log: log, cacheMax: cacheMax, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *Service) SetClock(fn func() time.Time) { s.now = fn }

// OnChange registers a callback fired when a feed refreshes or is moderated, so
// the API can broadcast the change.
func (s *Service) OnChange(fn func(feedID int64, reason string)) { s.onChange = fn }

func (s *Service) notify(feedID int64, reason string) {
	if s.onChange != nil {
		s.onChange(feedID, reason)
	}
}

// aad binds a credential's ciphertext to its feed, so a token cannot be lifted
// from one feed's row and decrypted under another.
func credAAD(id int64) []byte {
	return []byte(fmt.Sprintf("social-credential:%d", id))
}

// CreateFeed stores a new feed and, optionally, its encrypted credential.
func (s *Service) CreateFeed(ctx context.Context, f Feed, token string) (Feed, error) {
	if _, ok := AdapterFor(f.Platform); !ok {
		return Feed{}, ErrUnknownPlatform
	}
	if strings.TrimSpace(f.Name) == "" {
		return Feed{}, errors.New("a feed needs a name")
	}
	if f.RefreshSec < 60 {
		f.RefreshSec = 900
	}
	if f.MaxItems < 1 || f.MaxItems > 50 {
		f.MaxItems = 20
	}
	if f.Moderation != "manual" && f.Moderation != "auto" {
		f.Moderation = "manual"
	}
	if f.Template == "" {
		f.Template = "cards"
	}
	if strings.TrimSpace(f.Config) == "" {
		f.Config = "{}"
	}

	var credID *int64
	if strings.TrimSpace(token) != "" {
		id, err := s.storeCredential(ctx, f.Name, token)
		if err != nil {
			return Feed{}, err
		}
		credID = &id
	}

	now := rfc3339(s.now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO social_feeds (name, platform, source, config_json, credential_id,
			enabled, refresh_sec, max_items, include_media, include_text, moderation,
			hashtag_filter, keyword_filter, blocklist, template, connection_state,
			created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.Name, f.Platform, f.Source, f.Config, credID, b2i(f.Enabled),
		f.RefreshSec, f.MaxItems, b2i(f.IncludeMedia), b2i(f.IncludeText),
		f.Moderation, f.HashtagFilter, f.KeywordFilter, f.Blocklist, f.Template,
		StateIdle, now, now)
	if err != nil {
		return Feed{}, fmt.Errorf("create feed: %w", err)
	}
	f.ID, _ = res.LastInsertId()
	f.CredentialID = credID
	f.ConnectionState = StateIdle
	return f, nil
}

// storeCredential encrypts and stores an API token, returning its id.
func (s *Service) storeCredential(ctx context.Context, name, token string) (int64, error) {
	if s.box == nil {
		return 0, errors.New("social: no encryption key configured; cannot store a token")
	}
	// Insert a placeholder row first to obtain the id, which is then used as the
	// AAD when encrypting. This binds the ciphertext to its row.
	now := rfc3339(s.now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO credentials (name, kind, ciphertext, nonce, hint, created_at, updated_at)
		VALUES (?, 'bearer', X'', X'', ?, ?, ?)`,
		"social:"+name, hintFor(token), now, now)
	if err != nil {
		return 0, fmt.Errorf("store credential: %w", err)
	}
	id, _ := res.LastInsertId()

	ct, nonce, err := s.box.Encrypt([]byte(token), credAAD(id))
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE credentials SET ciphertext = ?, nonce = ? WHERE id = ?`, ct, nonce, id); err != nil {
		return 0, err
	}
	return id, nil
}

// hintFor returns a non-secret hint (last 4 chars) for the UI.
func hintFor(token string) string {
	if len(token) <= 4 {
		return "••••"
	}
	return "••••" + token[len(token)-4:]
}

// decryptCredential returns the plaintext token for a credential id.
func (s *Service) decryptCredential(ctx context.Context, id int64) (string, error) {
	if s.box == nil {
		return "", errors.New("social: no encryption key configured")
	}
	var ct, nonce []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT ciphertext, nonce FROM credentials WHERE id = ?`, id).Scan(&ct, &nonce)
	if err != nil {
		return "", err
	}
	plain, err := s.box.Decrypt(ct, nonce, credAAD(id))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DueFeeds returns enabled fetch-based feeds whose next attempt time has passed.
func (s *Service) DueFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, feedSelect+`
		WHERE enabled = 1
		  AND platform NOT IN ('manual','webhook')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`, rfc3339(s.now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeeds(rows)
}

// Refresh fetches one feed, filters and caches its posts, and updates state.
//
// A fetch failure never propagates to the display: the cached approved posts keep
// showing, the error is recorded, and the next attempt is pushed out with
// exponential backoff.
func (s *Service) Refresh(ctx context.Context, f Feed) error {
	adapter, ok := AdapterFor(f.Platform)
	if !ok {
		return ErrUnknownPlatform
	}

	var token string
	if f.CredentialID != nil {
		t, err := s.decryptCredential(ctx, *f.CredentialID)
		if err != nil {
			s.recordFailure(ctx, f, StateUnauthorised, "could not read the stored token")
			return err
		}
		token = t
	}

	var cfg map[string]any
	_ = json.Unmarshal([]byte(f.Config), &cfg)

	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	posts, err := adapter.Fetch(fetchCtx, FeedConfig{
		ID: f.ID, Platform: f.Platform, Source: f.Source, Config: cfg, Token: token,
	}, f.MaxItems)
	// The token is out of scope from here; it was never stored beyond this call.
	if err != nil {
		state := StateError
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "token") || strings.Contains(low, "401") || strings.Contains(low, "403") {
			state = StateUnauthorised
		} else if strings.Contains(low, "rate") || strings.Contains(low, "429") {
			state = StateRateLimited
		}
		s.recordFailure(ctx, f, state, sanitiseError(err))
		return err
	}

	inserted := 0
	for _, p := range posts {
		if p.ExternalID == "" {
			continue
		}
		if !PassesFilters(p, f.KeywordFilter, f.HashtagFilter, f.Blocklist) {
			continue
		}
		state := "pending"
		if f.Moderation == "auto" {
			state = "approved"
		}
		if s.upsertPost(ctx, f.ID, p, state) {
			inserted++
		}
	}

	s.trimCache(ctx, f.ID)
	s.recordSuccess(ctx, f)
	if inserted > 0 {
		s.notify(f.ID, "refreshed")
	}
	return nil
}

func (s *Service) upsertPost(ctx context.Context, feedID int64, p Post, state string) bool {
	var posted any
	if p.PostedAt != nil {
		posted = rfc3339(*p.PostedAt)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO social_posts (feed_id, external_id, author, author_handle, avatar_url,
			text, media_url, media_kind, permalink, posted_at, fetched_at, moderation_state)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(feed_id, external_id) DO NOTHING`,
		feedID, p.ExternalID, p.Author, p.AuthorHandle, p.AvatarURL, p.Text,
		p.MediaURL, p.MediaKind, p.Permalink, posted, rfc3339(s.now()), state)
	if err != nil {
		s.log.Warn("could not store social post", "feed", feedID, "error", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// trimCache enforces the per-feed cache cap, deleting the oldest non-pinned posts.
func (s *Service) trimCache(ctx context.Context, feedID int64) {
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM social_posts
		 WHERE feed_id = ? AND pinned = 0 AND id NOT IN (
			SELECT id FROM social_posts WHERE feed_id = ?
			 ORDER BY COALESCE(posted_at, fetched_at) DESC, id DESC LIMIT ?
		 )`, feedID, feedID, s.cacheMax)
}

func (s *Service) recordSuccess(ctx context.Context, f Feed) {
	next := nextAttempt(s.now(), f.RefreshSec, 0)
	_, _ = s.db.ExecContext(ctx, `
		UPDATE social_feeds SET connection_state = ?, last_refresh_at = ?, last_error = '',
			consecutive_failures = 0, next_attempt_at = ?, updated_at = ? WHERE id = ?`,
		StateOK, rfc3339(s.now()), rfc3339(next), rfc3339(s.now()), f.ID)
}

func (s *Service) recordFailure(ctx context.Context, f Feed, state, msg string) {
	failures := f.Failures + 1
	next := nextAttempt(s.now(), f.RefreshSec, failures)
	_, _ = s.db.ExecContext(ctx, `
		UPDATE social_feeds SET connection_state = ?, last_error = ?,
			consecutive_failures = ?, next_attempt_at = ?, updated_at = ? WHERE id = ?`,
		state, msg, failures, rfc3339(next), rfc3339(s.now()), f.ID)
	s.log.Warn("social feed refresh failed", "feed", f.ID, "platform", f.Platform,
		"state", state, "failures", failures)
}

// sanitiseError removes any URL that could carry a token from an error string.
func sanitiseError(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "http"); i >= 0 {
		if j := strings.IndexByte(msg[i:], ' '); j >= 0 {
			msg = msg[:i] + "[url]" + msg[i+j:]
		} else {
			msg = msg[:i] + "[url]"
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

const feedSelect = `
	SELECT id, name, platform, source, config_json, credential_id, enabled, refresh_sec,
	       max_items, include_media, include_text, moderation, hashtag_filter,
	       keyword_filter, blocklist, template, connection_state, last_refresh_at,
	       last_error, consecutive_failures, next_attempt_at
	  FROM social_feeds`

func scanFeeds(rows *sql.Rows) ([]Feed, error) {
	var out []Feed
	for rows.Next() {
		f, err := scanFeedRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanFeedRow(sc interface {
	Scan(...any) error
}) (Feed, error) {
	var (
		f                          Feed
		credID                     sql.NullInt64
		enabled, incMedia, incText int
		lastRefresh, nextAttempt   sql.NullString
	)
	if err := sc.Scan(&f.ID, &f.Name, &f.Platform, &f.Source, &f.Config, &credID,
		&enabled, &f.RefreshSec, &f.MaxItems, &incMedia, &incText, &f.Moderation,
		&f.HashtagFilter, &f.KeywordFilter, &f.Blocklist, &f.Template, &f.ConnectionState,
		&lastRefresh, &f.LastError, &f.Failures, &nextAttempt); err != nil {
		return Feed{}, err
	}
	f.Enabled = enabled == 1
	f.IncludeMedia = incMedia == 1
	f.IncludeText = incText == 1
	if credID.Valid {
		f.CredentialID = &credID.Int64
	}
	if lastRefresh.Valid {
		t := parseTime(lastRefresh.String)
		f.LastRefreshAt = &t
	}
	if nextAttempt.Valid {
		t := parseTime(nextAttempt.String)
		f.NextAttemptAt = &t
	}
	return f, nil
}

// ListFeeds returns all feeds.
func (s *Service) ListFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, feedSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeeds(rows)
}

// GetFeed loads one feed.
func (s *Service) GetFeed(ctx context.Context, id int64) (Feed, error) {
	f, err := scanFeedRow(s.db.QueryRowContext(ctx, feedSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, errors.New("social: feed not found")
	}
	return f, err
}

// DeleteFeed removes a feed, its posts (cascade) and any credential.
func (s *Service) DeleteFeed(ctx context.Context, id int64) error {
	f, err := s.GetFeed(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM social_feeds WHERE id = ?`, id); err != nil {
		return err
	}
	if f.CredentialID != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, *f.CredentialID)
	}
	return nil
}

// SetEnabled toggles a feed.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE social_feeds SET enabled = ?, updated_at = ? WHERE id = ?`,
		b2i(enabled), rfc3339(s.now()), id)
	return err
}

// ---- posts and moderation --------------------------------------------------

// Posts returns cached posts for a feed, filtered by moderation state.
func (s *Service) Posts(ctx context.Context, feedID int64, state string, limit int) ([]StoredPost, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := postSelect + ` WHERE feed_id = ?`
	args := []any{feedID}
	if state != "" && state != "all" {
		q += ` AND moderation_state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY pinned DESC, COALESCE(posted_at, fetched_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ApprovedForDisplay returns approved posts a scene should show, newest first.
func (s *Service) ApprovedForDisplay(ctx context.Context, feedID, limit int64) ([]StoredPost, error) {
	rows, err := s.db.QueryContext(ctx, postSelect+`
		WHERE feed_id = ? AND moderation_state = 'approved'
		ORDER BY pinned DESC, COALESCE(posted_at, fetched_at) DESC, id DESC LIMIT ?`,
		feedID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

const postSelect = `
	SELECT id, feed_id, external_id, author, author_handle, avatar_url, text,
	       media_url, media_kind, permalink, posted_at, fetched_at, moderation_state, pinned
	  FROM social_posts`

func scanPosts(rows *sql.Rows) ([]StoredPost, error) {
	var out []StoredPost
	for rows.Next() {
		var (
			p       StoredPost
			posted  sql.NullString
			fetched string
			pinned  int
		)
		if err := rows.Scan(&p.ID, &p.FeedID, &p.ExternalID, &p.Author, &p.AuthorHandle,
			&p.AvatarURL, &p.Text, &p.MediaURL, &p.MediaKind, &p.Permalink,
			&posted, &fetched, &p.ModerationState, &pinned); err != nil {
			return nil, err
		}
		p.Pinned = pinned == 1
		p.FetchedAt = parseTime(fetched)
		if posted.Valid {
			t := parseTime(posted.String)
			p.PostedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Moderate sets a post's moderation state.
func (s *Service) Moderate(ctx context.Context, postID int64, state, actor string) error {
	switch state {
	case "approved", "rejected", "hidden", "pending":
	default:
		return fmt.Errorf("social: invalid moderation state %q", state)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE social_posts SET moderation_state = ?, moderated_by = ?, moderated_at = ?
		 WHERE id = ?`, state, actor, rfc3339(s.now()), postID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("social: post not found")
	}
	var feedID int64
	_ = s.db.QueryRowContext(ctx, `SELECT feed_id FROM social_posts WHERE id = ?`, postID).Scan(&feedID)
	s.notify(feedID, "moderated")
	return nil
}

// SetPinned pins or unpins a post so it survives cache trimming and shows first.
func (s *Service) SetPinned(ctx context.Context, postID int64, pinned bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE social_posts SET pinned = ? WHERE id = ?`, b2i(pinned), postID)
	return err
}

// DeletePost removes a single cached post.
func (s *Service) DeletePost(ctx context.Context, postID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM social_posts WHERE id = ?`, postID)
	return err
}

// IngestWebhook stores a post pushed to the webhook endpoint.
func (s *Service) IngestWebhook(ctx context.Context, feedID int64, p Post) error {
	f, err := s.GetFeed(ctx, feedID)
	if err != nil {
		return err
	}
	if f.Platform != "webhook" && f.Platform != "manual" {
		return errors.New("social: this feed does not accept pushed posts")
	}
	if p.ExternalID == "" {
		p.ExternalID = fmt.Sprintf("wh-%d", s.now().UnixNano())
	}
	p.Text = cleanText(p.Text)
	if !PassesFilters(p, f.KeywordFilter, f.HashtagFilter, f.Blocklist) {
		return errors.New("social: post did not pass the feed filters")
	}
	state := "pending"
	if f.Moderation == "auto" {
		state = "approved"
	}
	s.upsertPost(ctx, feedID, p, state)
	s.trimCache(ctx, feedID)
	s.notify(feedID, "webhook")
	return nil
}

// PendingCount returns how many posts await moderation across all feeds.
func (s *Service) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM social_posts WHERE moderation_state = 'pending'`).Scan(&n)
	return n, err
}

// ---- shared time helpers ---------------------------------------------------

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
