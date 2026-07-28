// Package store contains the database repositories.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/content"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
)

var (
	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrInUse is returned when deleting something a scene still references.
	ErrInUse = errors.New("store: still referenced by a scene")
)

// Revision describes one playlist version.
type Revision struct {
	ID          int64      `json:"id"`
	IsDraft     bool       `json:"is_draft"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	PublishedBy string     `json:"published_by"`
	Note        string     `json:"note"`
	Keep        bool       `json:"keep"`
	ParentID    *int64     `json:"parent_id,omitempty"`
	SceneCount  int        `json:"scene_count"`
}

// PlaylistStore manages revisions, scenes and zones.
type PlaylistStore struct {
	db  *dbx.DB
	now func() time.Time
}

// NewPlaylistStore builds a PlaylistStore.
func NewPlaylistStore(db *dbx.DB) *PlaylistStore {
	return &PlaylistStore{db: db, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *PlaylistStore) SetClock(fn func() time.Time) { s.now = fn }

// NewStableID generates an identifier that survives publish-time cloning, so
// history and "same scene across revisions" comparisons remain possible.
func NewStableID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is unrecoverable; a time-based fallback keeps the
		// application usable rather than panicking during an edit.
		return fmt.Sprintf("s%d", time.Now().UnixNano())
	}
	return "s" + hex.EncodeToString(b)
}

// ---- revisions ---------------------------------------------------------------

// DraftRevision returns the current draft, creating one if it is somehow missing.
func (s *PlaylistStore) DraftRevision(ctx context.Context) (Revision, error) {
	r, err := s.scanRevision(ctx, s.db.QueryRowContext(ctx, revisionSelect+` WHERE r.is_draft = 1`))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Revision{}, err
	}
	// Self-heal: an appliance without a draft cannot be edited at all.
	var id int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO playlist_revisions (is_draft, created_at, created_by, note)
			 VALUES (1, ?, 'system', 'recreated draft')`, rfc3339(s.now()))
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return Revision{}, err
	}
	return s.GetRevision(ctx, id)
}

// PublishedRevision returns the newest published revision.
func (s *PlaylistStore) PublishedRevision(ctx context.Context) (Revision, error) {
	return s.scanRevision(ctx, s.db.QueryRowContext(ctx,
		revisionSelect+` WHERE r.published_at IS NOT NULL ORDER BY r.published_at DESC, r.id DESC LIMIT 1`))
}

// GetRevision loads one revision by ID.
func (s *PlaylistStore) GetRevision(ctx context.Context, id int64) (Revision, error) {
	return s.scanRevision(ctx, s.db.QueryRowContext(ctx, revisionSelect+` WHERE r.id = ?`, id))
}

const revisionSelect = `
	SELECT r.id, r.is_draft, r.published_at, r.created_at, r.created_by,
	       r.published_by, r.note, r.keep, r.parent_id,
	       (SELECT COUNT(*) FROM scenes sc WHERE sc.revision_id = r.id)
	  FROM playlist_revisions r`

func (s *PlaylistStore) scanRevision(_ context.Context, row *sql.Row) (Revision, error) {
	var (
		r           Revision
		isDraft     int
		keep        int
		publishedAt sql.NullString
		createdAt   string
		parentID    sql.NullInt64
	)
	err := row.Scan(&r.ID, &isDraft, &publishedAt, &createdAt, &r.CreatedBy,
		&r.PublishedBy, &r.Note, &keep, &parentID, &r.SceneCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, err
	}
	r.IsDraft = isDraft == 1
	r.Keep = keep == 1
	r.CreatedAt = parseTime(createdAt)
	if publishedAt.Valid {
		t := parseTime(publishedAt.String)
		r.PublishedAt = &t
	}
	if parentID.Valid {
		r.ParentID = &parentID.Int64
	}
	return r, nil
}

// ListRevisions returns revision history, newest first.
func (s *PlaylistStore) ListRevisions(ctx context.Context, limit int) ([]Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, revisionSelect+` ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Revision
	for rows.Next() {
		var (
			r           Revision
			isDraft     int
			keep        int
			publishedAt sql.NullString
			createdAt   string
			parentID    sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &isDraft, &publishedAt, &createdAt, &r.CreatedBy,
			&r.PublishedBy, &r.Note, &keep, &parentID, &r.SceneCount); err != nil {
			return nil, err
		}
		r.IsDraft = isDraft == 1
		r.Keep = keep == 1
		r.CreatedAt = parseTime(createdAt)
		if publishedAt.Valid {
			t := parseTime(publishedAt.String)
			r.PublishedAt = &t
		}
		if parentID.Valid {
			r.ParentID = &parentID.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- scenes ------------------------------------------------------------------

// ListScenes returns every scene in a revision, ordered by position, with zones.
func (s *PlaylistStore) ListScenes(ctx context.Context, revisionID int64) ([]content.Scene, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, revision_id, stable_id, name, position, enabled, layout, layout_json,
		       duration_ms, background, transition, active_from, active_until, days_mask,
		       valid, validation_msg, last_played_at, last_error,
		       created_at, created_by, updated_at, updated_by
		  FROM scenes WHERE revision_id = ? ORDER BY position, id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scenes []content.Scene
	byID := map[int64]int{}
	for rows.Next() {
		var (
			sc                     content.Scene
			enabled, valid         int
			activeFrom, activeTill sql.NullString
			lastPlayed             sql.NullString
			createdAt, updatedAt   string
		)
		if err := rows.Scan(&sc.ID, &sc.RevisionID, &sc.StableID, &sc.Name, &sc.Position,
			&enabled, &sc.Layout, &sc.LayoutJSON, &sc.DurationMS, &sc.Background,
			&sc.Transition, &activeFrom, &activeTill, &sc.DaysMask, &valid,
			&sc.ValidationMsg, &lastPlayed, &sc.LastError,
			&createdAt, &sc.CreatedBy, &updatedAt, &sc.UpdatedBy); err != nil {
			return nil, err
		}
		sc.Enabled = enabled == 1
		sc.Valid = valid == 1
		sc.CreatedAt, sc.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		if activeFrom.Valid {
			t := parseTime(activeFrom.String)
			sc.ActiveFrom = &t
		}
		if activeTill.Valid {
			t := parseTime(activeTill.String)
			sc.ActiveUntil = &t
		}
		if lastPlayed.Valid {
			t := parseTime(lastPlayed.String)
			sc.LastPlayedAt = &t
		}
		byID[sc.ID] = len(scenes)
		scenes = append(scenes, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(scenes) == 0 {
		return scenes, nil
	}

	// Load all zones for the revision in one query rather than per scene.
	zrows, err := s.db.QueryContext(ctx, `
		SELECT z.id, z.scene_id, z.slot, z.x, z.y, z.w, z.h, z.z,
		       z.content_type, z.content_ref, z.config_json, z.style_json, z.label
		  FROM zones z JOIN scenes s ON s.id = z.scene_id
		 WHERE s.revision_id = ? ORDER BY z.z, z.slot`, revisionID)
	if err != nil {
		return nil, err
	}
	defer zrows.Close()

	for zrows.Next() {
		var z content.Zone
		if err := zrows.Scan(&z.ID, &z.SceneID, &z.Slot, &z.Rect.X, &z.Rect.Y,
			&z.Rect.W, &z.Rect.H, &z.Z, &z.ContentType, &z.ContentRef,
			&z.Config, &z.Style, &z.Label); err != nil {
			return nil, err
		}
		if idx, ok := byID[z.SceneID]; ok {
			scenes[idx].Zones = append(scenes[idx].Zones, z)
		}
	}
	return scenes, zrows.Err()
}

// GetScene loads one scene with its zones.
func (s *PlaylistStore) GetScene(ctx context.Context, id int64) (content.Scene, error) {
	var revID int64
	err := s.db.QueryRowContext(ctx, `SELECT revision_id FROM scenes WHERE id = ?`, id).Scan(&revID)
	if errors.Is(err, sql.ErrNoRows) {
		return content.Scene{}, ErrNotFound
	}
	if err != nil {
		return content.Scene{}, err
	}
	scenes, err := s.ListScenes(ctx, revID)
	if err != nil {
		return content.Scene{}, err
	}
	for _, sc := range scenes {
		if sc.ID == id {
			return sc, nil
		}
	}
	return content.Scene{}, ErrNotFound
}

// SaveScene inserts or updates a scene in the draft revision.
//
// Validation runs first and its result is persisted on the row, so the admin UI
// can show which scenes are broken without re-validating everything, and the
// publish path can refuse a playlist containing invalid scenes.
func (s *PlaylistStore) SaveScene(ctx context.Context, sc *content.Scene, actor string) error {
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	sc.RevisionID = draft.ID

	refs, verr := content.ValidateScene(sc)
	sc.Valid = verr == nil
	sc.ValidationMsg = ""
	if verr != nil {
		sc.ValidationMsg = verr.Error()
	}

	if sc.StableID == "" {
		sc.StableID = NewStableID()
	}
	if sc.Transition == "" {
		sc.Transition = "fade"
	}
	now := rfc3339(s.now())

	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Referenced media/websites/feeds must exist, or the player would render a
		// fallback for content the operator believes is configured correctly.
		if sc.Valid {
			if msg, err := checkRefs(ctx, tx, refs); err != nil {
				return err
			} else if msg != "" {
				sc.Valid = false
				sc.ValidationMsg = msg
			}
		}

		if sc.ID == 0 {
			if sc.Position == 0 {
				var maxPos sql.NullInt64
				if err := tx.QueryRowContext(ctx,
					`SELECT MAX(position) FROM scenes WHERE revision_id = ?`, draft.ID).Scan(&maxPos); err != nil {
					return err
				}
				sc.Position = int(maxPos.Int64) + 1
			}
			res, err := tx.ExecContext(ctx, `
				INSERT INTO scenes (revision_id, stable_id, name, position, enabled, layout,
					layout_json, duration_ms, background, transition, active_from, active_until,
					days_mask, valid, validation_msg, created_at, created_by, updated_at, updated_by)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				draft.ID, sc.StableID, sc.Name, sc.Position, b2i(sc.Enabled), sc.Layout,
				orEmptyJSON(sc.LayoutJSON), sc.DurationMS, sc.Background, sc.Transition,
				nullTime(sc.ActiveFrom), nullTime(sc.ActiveUntil), sc.DaysMask,
				b2i(sc.Valid), sc.ValidationMsg, now, actor, now, actor)
			if err != nil {
				return err
			}
			sc.ID, _ = res.LastInsertId()
		} else {
			res, err := tx.ExecContext(ctx, `
				UPDATE scenes SET name=?, position=?, enabled=?, layout=?, layout_json=?,
					duration_ms=?, background=?, transition=?, active_from=?, active_until=?,
					days_mask=?, valid=?, validation_msg=?, updated_at=?, updated_by=?
				 WHERE id=? AND revision_id=?`,
				sc.Name, sc.Position, b2i(sc.Enabled), sc.Layout, orEmptyJSON(sc.LayoutJSON),
				sc.DurationMS, sc.Background, sc.Transition,
				nullTime(sc.ActiveFrom), nullTime(sc.ActiveUntil), sc.DaysMask,
				b2i(sc.Valid), sc.ValidationMsg, now, actor, sc.ID, draft.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				// Editing a scene that belongs to a published revision is a bug in the
				// caller: published revisions are immutable.
				return fmt.Errorf("%w: scene %d is not in the draft revision", ErrNotFound, sc.ID)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM zones WHERE scene_id = ?`, sc.ID); err != nil {
				return err
			}
		}

		for i := range sc.Zones {
			z := &sc.Zones[i]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO zones (scene_id, slot, x, y, w, h, z, content_type,
					content_ref, config_json, style_json, label)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				sc.ID, z.Slot, z.Rect.X, z.Rect.Y, z.Rect.W, z.Rect.H, z.Z,
				z.ContentType, z.ContentRef, orEmptyJSON(z.Config),
				orEmptyJSON(z.Style), z.Label); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Surface the validation failure to the caller after the row is saved, so an
	// operator can save work in progress and still be told what is wrong.
	return verr
}

// checkRefs verifies referenced rows exist, returning a human message if not.
func checkRefs(ctx context.Context, tx *sql.Tx, refs content.Refs) (string, error) {
	check := func(table string, ids []int64, label string) (string, error) {
		for _, id := range ids {
			var n int
			// #nosec G201 -- table is a compile-time constant from the call sites below.
			q := "SELECT COUNT(*) FROM " + table + " WHERE id = ?"
			if err := tx.QueryRowContext(ctx, q, id).Scan(&n); err != nil {
				return "", err
			}
			if n == 0 {
				return fmt.Sprintf("referenced %s %d no longer exists", label, id), nil
			}
		}
		return "", nil
	}
	for _, c := range []struct {
		table, label string
		ids          []int64
	}{
		{"media", "media item", refs.MediaIDs},
		{"websites", "website", refs.WebsiteIDs},
		{"social_feeds", "social feed", refs.FeedIDs},
		{"credentials", "credential", refs.CredIDs},
	} {
		msg, err := check(c.table, c.ids, c.label)
		if err != nil || msg != "" {
			return msg, err
		}
	}
	return "", nil
}

// DeleteScene removes a scene from the draft.
func (s *PlaylistStore) DeleteScene(ctx context.Context, id int64) error {
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM scenes WHERE id = ? AND revision_id = ?`, id, draft.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DuplicateScene copies a scene within the draft, appending it at the end.
func (s *PlaylistStore) DuplicateScene(ctx context.Context, id int64, actor string) (content.Scene, error) {
	src, err := s.GetScene(ctx, id)
	if err != nil {
		return content.Scene{}, err
	}
	dup := src
	dup.ID = 0
	dup.StableID = NewStableID()
	dup.Position = 0 // appended
	dup.Name = nextCopyName(src.Name)
	dup.Zones = make([]content.Zone, len(src.Zones))
	copy(dup.Zones, src.Zones)
	for i := range dup.Zones {
		dup.Zones[i].ID = 0
		dup.Zones[i].SceneID = 0
	}
	if err := s.SaveScene(ctx, &dup, actor); err != nil {
		// A duplicate of an invalid scene is still saved; report it as created.
		var ve content.ValidationErrors
		if !errors.As(err, &ve) {
			return content.Scene{}, err
		}
	}
	return dup, nil
}

func nextCopyName(name string) string {
	const suffix = " (copy)"
	if len(name)+len(suffix) > content.MaxSceneNameLen {
		name = name[:content.MaxSceneNameLen-len(suffix)]
	}
	return name + suffix
}

// Reorder applies a new scene order to the draft.
//
// Positions are rewritten from the supplied list in one transaction, so a partial
// reorder can never be observed.
//
// Rows that actually move have updated_at stamped: reordering is an unpublished
// change like any other, and leaving the timestamps alone would hide it from the
// dashboard's "you have unpublished changes" check.
func (s *PlaylistStore) Reorder(ctx context.Context, sceneIDs []int64, actor string) error {
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	now := rfc3339(s.now())
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var have int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scenes WHERE revision_id = ?`, draft.ID).Scan(&have); err != nil {
			return err
		}
		if have != len(sceneIDs) {
			return fmt.Errorf("reorder must list all %d scenes, got %d", have, len(sceneIDs))
		}
		for i, id := range sceneIDs {
			res, err := tx.ExecContext(ctx, `
				UPDATE scenes
				   SET position = ?,
				       updated_at = CASE WHEN position = ? THEN updated_at ELSE ? END,
				       updated_by = CASE WHEN position = ? THEN updated_by ELSE ? END
				 WHERE id = ? AND revision_id = ?`,
				i+1, i+1, now, i+1, actor, id, draft.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("%w: scene %d is not in the draft", ErrNotFound, id)
			}
		}
		return nil
	})
}

// SetEnabled toggles one scene.
func (s *PlaylistStore) SetEnabled(ctx context.Context, id int64, enabled bool, actor string) error {
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE scenes SET enabled = ?, updated_at = ?, updated_by = ?
		  WHERE id = ? AND revision_id = ?`,
		b2i(enabled), rfc3339(s.now()), actor, id, draft.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BulkSetEnabled toggles many scenes at once.
func (s *PlaylistStore) BulkSetEnabled(ctx context.Context, ids []int64, enabled bool, actor string) error {
	if len(ids) == 0 {
		return nil
	}
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE scenes SET enabled = ?, updated_at = ?, updated_by = ?
				  WHERE id = ? AND revision_id = ?`,
				b2i(enabled), rfc3339(s.now()), actor, id, draft.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// BulkSetDuration sets the same duration on many scenes.
func (s *PlaylistStore) BulkSetDuration(ctx context.Context, ids []int64, durationMS int, actor string) error {
	if err := content.ValidateDuration(durationMS); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	draft, err := s.DraftRevision(ctx)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE scenes SET duration_ms = ?, updated_at = ?, updated_by = ?
				  WHERE id = ? AND revision_id = ?`,
				durationMS, rfc3339(s.now()), actor, id, draft.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- publishing ---------------------------------------------------------------

// PublishResult describes a successful publish.
type PublishResult struct {
	PublishedRevision int64 `json:"published_revision"`
	NewDraftRevision  int64 `json:"new_draft_revision"`
	SceneCount        int   `json:"scene_count"`
}

// Publish makes the draft live.
//
// The whole operation is one transaction: the draft is stamped published and a
// fresh draft is cloned from it. Because the player only ever reads the newest
// *published* revision, there is no window in which a partially-saved playlist
// can reach the screen.
func (s *PlaylistStore) Publish(ctx context.Context, actor, note string) (PublishResult, error) {
	var res PublishResult

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var draftID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM playlist_revisions WHERE is_draft = 1`).Scan(&draftID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("no draft revision to publish")
			}
			return err
		}

		// Refuse to publish a playlist containing invalid scenes. Publishing
		// something known-broken would put a fallback slide on the screen with no
		// obvious cause.
		var invalid int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scenes WHERE revision_id = ? AND valid = 0 AND enabled = 1`,
			draftID).Scan(&invalid); err != nil {
			return err
		}
		if invalid > 0 {
			return fmt.Errorf("cannot publish: %d enabled scene(s) have validation errors", invalid)
		}

		var enabled int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scenes WHERE revision_id = ? AND enabled = 1`,
			draftID).Scan(&enabled); err != nil {
			return err
		}
		if enabled == 0 {
			return errors.New("cannot publish: no enabled scenes; the display would be blank")
		}
		res.SceneCount = enabled

		now := rfc3339(s.now())
		// Stamp the draft as published. Clearing is_draft first frees the partial
		// unique index so the new draft can be inserted.
		if _, err := tx.ExecContext(ctx, `
			UPDATE playlist_revisions
			   SET is_draft = 0, published_at = ?, published_by = ?, note = ?
			 WHERE id = ?`, now, actor, note, draftID); err != nil {
			return err
		}
		res.PublishedRevision = draftID

		newDraft, err := cloneRevision(ctx, tx, draftID, actor, s.now())
		if err != nil {
			return err
		}
		res.NewDraftRevision = newDraft
		return nil
	})
	return res, err
}

// cloneRevision deep-copies a revision's scenes and zones into a new draft.
func cloneRevision(ctx context.Context, tx *sql.Tx, srcID int64, actor string, now time.Time) (int64, error) {
	ts := rfc3339(now)
	r, err := tx.ExecContext(ctx, `
		INSERT INTO playlist_revisions (is_draft, created_at, created_by, note, parent_id)
		VALUES (1, ?, ?, '', ?)`, ts, actor, srcID)
	if err != nil {
		return 0, err
	}
	newID, _ := r.LastInsertId()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, stable_id, name, position, enabled, layout, layout_json, duration_ms,
		       background, transition, active_from, active_until, days_mask, valid,
		       validation_msg, created_at, created_by, updated_at, updated_by
		  FROM scenes WHERE revision_id = ? ORDER BY position, id`, srcID)
	if err != nil {
		return 0, err
	}

	type sceneRow struct {
		oldID                                       int64
		stableID, name, layout, layoutJSON          string
		background, transition, validationMsg       string
		createdAt, createdBy                        string
		updatedAt, updatedBy                        string
		position, durationMS, daysMask, enabled, ok int
		activeFrom, activeUntil                     sql.NullString
	}
	var srcScenes []sceneRow
	for rows.Next() {
		var sr sceneRow
		if err := rows.Scan(&sr.oldID, &sr.stableID, &sr.name, &sr.position, &sr.enabled,
			&sr.layout, &sr.layoutJSON, &sr.durationMS, &sr.background, &sr.transition,
			&sr.activeFrom, &sr.activeUntil, &sr.daysMask, &sr.ok, &sr.validationMsg,
			&sr.createdAt, &sr.createdBy, &sr.updatedAt, &sr.updatedBy); err != nil {
			rows.Close()
			return 0, err
		}
		srcScenes = append(srcScenes, sr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, sr := range srcScenes {
		// updated_at is copied from the source rather than stamped with the clone
		// time. A clone is not an edit: stamping it would make every scene in the
		// fresh draft look newer than its published counterpart, and the dashboard's
		// "unpublished changes" check — which compares those timestamps — would read
		// true the instant a publish finished.
		res, err := tx.ExecContext(ctx, `
			INSERT INTO scenes (revision_id, stable_id, name, position, enabled, layout,
				layout_json, duration_ms, background, transition, active_from, active_until,
				days_mask, valid, validation_msg, created_at, created_by, updated_at, updated_by)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID, sr.stableID, sr.name, sr.position, sr.enabled, sr.layout, sr.layoutJSON,
			sr.durationMS, sr.background, sr.transition, sr.activeFrom, sr.activeUntil,
			sr.daysMask, sr.ok, sr.validationMsg, sr.createdAt, sr.createdBy,
			sr.updatedAt, sr.updatedBy)
		if err != nil {
			return 0, err
		}
		newSceneID, _ := res.LastInsertId()

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO zones (scene_id, slot, x, y, w, h, z, content_type, content_ref,
			                   config_json, style_json, label)
			SELECT ?, slot, x, y, w, h, z, content_type, content_ref,
			       config_json, style_json, label
			  FROM zones WHERE scene_id = ?`, newSceneID, sr.oldID); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

// Rollback restores a previous revision by cloning it into the draft and
// publishing that clone.
//
// History is append-only: rolling back creates a new revision rather than
// deleting newer ones, so an accidental rollback is itself reversible.
func (s *PlaylistStore) Rollback(ctx context.Context, targetRevID int64, actor string) (PublishResult, error) {
	var res PublishResult
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var published sql.NullString
		if err := tx.QueryRowContext(ctx,
			`SELECT published_at FROM playlist_revisions WHERE id = ?`, targetRevID).Scan(&published); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !published.Valid {
			return errors.New("can only roll back to a previously published revision")
		}

		// Drop the current draft so the clone can take the single-draft slot. The
		// draft holds unpublished edits, which is exactly what a rollback discards.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM playlist_revisions WHERE is_draft = 1`); err != nil {
			return err
		}

		newID, err := cloneRevision(ctx, tx, targetRevID, actor, s.now())
		if err != nil {
			return err
		}
		now := rfc3339(s.now())
		if _, err := tx.ExecContext(ctx, `
			UPDATE playlist_revisions
			   SET is_draft = 0, published_at = ?, published_by = ?, note = ?
			 WHERE id = ?`, now, actor,
			fmt.Sprintf("rollback to revision %d", targetRevID), newID); err != nil {
			return err
		}
		res.PublishedRevision = newID

		freshDraft, err := cloneRevision(ctx, tx, newID, actor, s.now())
		if err != nil {
			return err
		}
		res.NewDraftRevision = freshDraft

		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scenes WHERE revision_id = ? AND enabled = 1`,
			newID).Scan(&res.SceneCount); err != nil {
			return err
		}
		return nil
	})
	return res, err
}

// DiscardDraft resets the draft to match the live revision, throwing away
// unpublished edits.
func (s *PlaylistStore) DiscardDraft(ctx context.Context, actor string) (int64, error) {
	var newDraft int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var liveID int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM playlist_revisions
			 WHERE published_at IS NOT NULL
			 ORDER BY published_at DESC, id DESC LIMIT 1`).Scan(&liveID)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("nothing has been published yet; there is no state to revert to")
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_revisions WHERE is_draft = 1`); err != nil {
			return err
		}
		newDraft, err = cloneRevision(ctx, tx, liveID, actor, s.now())
		return err
	})
	return newDraft, err
}

// SetKeep pins or unpins a revision against pruning.
func (s *PlaylistStore) SetKeep(ctx context.Context, id int64, keep bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE playlist_revisions SET keep = ? WHERE id = ?`, b2i(keep), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneRevisions deletes old published revisions beyond the retention limit.
//
// The draft, the live revision and any pinned revision are always kept. Without
// this an appliance running for years accumulates a full scene/zone copy per
// publish, and media referenced only by an ancient revision can never be
// reclaimed by orphan cleanup.
func (s *PlaylistStore) PruneRevisions(ctx context.Context, keepN int) (int64, error) {
	if keepN < 2 {
		keepN = 2
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM playlist_revisions
		 WHERE is_draft = 0
		   AND keep = 0
		   AND published_at IS NOT NULL
		   AND id NOT IN (
		       SELECT id FROM playlist_revisions
		        WHERE published_at IS NOT NULL
		        ORDER BY published_at DESC, id DESC
		        LIMIT ?
		   )`, keepN)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ReferencedMediaIDs returns media IDs used by any surviving revision, so orphan
// cleanup can tell which uploaded files are still needed.
func (s *PlaylistStore) ReferencedMediaIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT content_ref FROM zones
		 WHERE content_type IN ('image','video') AND content_ref != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		if id, err := parseID(ref); err == nil {
			out[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Media referenced from inside config JSON (poster images, completion images,
	// announcement images) is found by scanning the config blobs. Every blob is
	// scanned rather than pre-filtered in SQL: `LIKE '%_id%'` reads as a literal
	// but `_` is a single-character wildcard in SQLite, so the filter never meant
	// what it looked like, and getting this set wrong silently deletes live media.
	crows, err := s.db.QueryContext(ctx, `
		SELECT config_json FROM zones WHERE config_json != '{}'`)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var cfg string
		if err := crows.Scan(&cfg); err != nil {
			return nil, err
		}
		for _, id := range extractMediaIDs(cfg) {
			out[id] = true
		}
	}
	return out, crows.Err()
}

// IsMediaReferenced reports whether any scene in any revision uses the media item.
func (s *PlaylistStore) IsMediaReferenced(ctx context.Context, mediaID int64) (bool, error) {
	refs, err := s.ReferencedMediaIDs(ctx)
	if err != nil {
		return false, err
	}
	return refs[mediaID], nil
}

// IsWebsiteReferenced reports whether any scene uses the website.
func (s *PlaylistStore) IsWebsiteReferenced(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM zones WHERE content_type = 'website' AND content_ref = ?`,
		fmt.Sprint(id)).Scan(&n)
	return n > 0, err
}

// IsFeedReferenced reports whether any scene uses the social feed.
//
// The candidate rows are narrowed in SQL and then matched exactly in Go. A bare
// LIKE on `"feed_id":5` also matches 50, 51 and 500, which would refuse a
// perfectly legitimate delete because an unrelated feed shares a digit prefix.
func (s *PlaylistStore) IsFeedReferenced(ctx context.Context, id int64) (bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT config_json FROM zones WHERE content_type IN ('social','ticker')`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cfg string
		if err := rows.Scan(&cfg); err != nil {
			return false, err
		}
		for _, found := range extractJSONIntField(cfg, "feed_id") {
			if found == id {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

// MarkPlayed records a successful or failed playback for a scene.
func (s *PlaylistStore) MarkPlayed(ctx context.Context, stableID string, playErr string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scenes SET last_played_at = ?, last_error = ?
		 WHERE stable_id = ?`, rfc3339(s.now()), playErr, stableID)
	return err
}

// ---- shared helpers ------------------------------------------------------------

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return rfc3339(*t)
}

func orEmptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func parseID(s string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &id)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("not an id: %q", s)
	}
	return id, nil
}

// extractMediaIDs pulls the media-referencing "*_id": N values out of a zone
// config blob.
func extractMediaIDs(cfg string) []int64 {
	var out []int64
	for _, key := range []string{"poster_id", "image_id", "completion_image_id"} {
		out = append(out, extractJSONIntField(cfg, key)...)
	}
	return out
}

// extractJSONIntField returns every integer value stored under key in a zone
// config blob.
//
// Zone config is schemaless by design — a new slide type must not need a schema
// change — so the reference scan reads it textually rather than unmarshalling into
// a type per slide kind. Matching on the quoted key and consuming only the digits
// that follow keeps it exact: "feed_id":5 does not match "feed_id":50, and a key
// that merely ends in the same letters is not a match either.
func extractJSONIntField(cfg, key string) []int64 {
	needle := `"` + key + `":`
	compact := strings.ReplaceAll(cfg, " ", "")

	var out []int64
	for idx := 0; idx < len(compact); {
		i := strings.Index(compact[idx:], needle)
		if i < 0 {
			break
		}
		start := idx + i + len(needle)
		end := start
		for end < len(compact) && compact[end] >= '0' && compact[end] <= '9' {
			end++
		}
		if end > start {
			// Reject a value that continues into something that is not a JSON
			// delimiter, e.g. a float such as "feed_id":5.5.
			if end == len(compact) || isJSONDelimiter(compact[end]) {
				if id, err := parseID(compact[start:end]); err == nil {
					out = append(out, id)
				}
			}
		}
		idx = start + 1
	}
	return out
}

func isJSONDelimiter(c byte) bool {
	return c == ',' || c == '}' || c == ']'
}
