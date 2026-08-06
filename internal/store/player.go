package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
)

// Status is the player's self-reported state.
type Status struct {
	Online        bool           `json:"online"`
	LastHeartbeat *time.Time     `json:"last_heartbeat,omitempty"`
	RevisionID    int64          `json:"revision_id"`
	CurrentScene  string         `json:"current_scene"`
	CurrentSlide  string         `json:"current_slide"`
	BrowserUA     string         `json:"browser_user_agent"`
	DisplayOn     bool           `json:"display_on"`
	ZoneStates    map[string]any `json:"zone_states,omitempty"`
	StorageState  string         `json:"storage_state,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Offline       bool           `json:"player_offline_mode,omitempty"`
}

// OfflineAfter is how long without a heartbeat before the player counts as
// offline. Three missed beats at the player's 10 s interval: long enough to ride
// out a transient stall, short enough that a genuinely dead display is noticed.
const OfflineAfter = 35 * time.Second

// PlayerStore tracks player status.
//
// Live status is held in memory and flushed to SQLite at most once per
// persistInterval (plus immediately on an online/offline edge). The player
// heartbeats every few seconds and the Jetson boots from flash: a sustained
// write every few seconds for months is avoidable wear for data that is
// worthless after a restart anyway.
type PlayerStore struct {
	db              *dbx.DB
	persistInterval time.Duration
	now             func() time.Time

	mu          sync.RWMutex
	live        Status
	lastPersist time.Time
	dirty       bool
}

// NewPlayerStore builds a PlayerStore.
func NewPlayerStore(db *dbx.DB, persistInterval time.Duration) *PlayerStore {
	if persistInterval <= 0 {
		persistInterval = time.Minute
	}
	return &PlayerStore{db: db, persistInterval: persistInterval, now: time.Now}
}

// SetClock overrides the time source, for tests.
func (s *PlayerStore) SetClock(fn func() time.Time) { s.now = fn }

// Load restores the last persisted status at startup.
func (s *PlayerStore) Load(ctx context.Context) error {
	var (
		online    int
		heartbeat sql.NullString
		revision  sql.NullInt64
		scene     string
		slide     string
		ua        string
		detail    string
		lastErr   string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT online, last_heartbeat, revision_id, current_scene, current_slide,
		       browser_ua, detail_json, last_error
		  FROM player_state WHERE id = 1`).
		Scan(&online, &heartbeat, &revision, &scene, &slide, &ua, &detail, &lastErr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.live = Status{
		CurrentScene: scene, CurrentSlide: slide, BrowserUA: ua, LastError: lastErr,
	}
	if revision.Valid {
		s.live.RevisionID = revision.Int64
	}
	if heartbeat.Valid {
		t := parseTime(heartbeat.String)
		s.live.LastHeartbeat = &t
	}
	// Never restore "online": the player is by definition not connected until it
	// heartbeats to this process.
	s.live.Online = false
	if detail != "" {
		var d map[string]any
		if json.Unmarshal([]byte(detail), &d) == nil {
			s.live.ZoneStates = d
		}
	}
	return nil
}

// Heartbeat records a report from the player and reports whether the online
// state changed, so the caller knows when to broadcast.
func (s *PlayerStore) Heartbeat(ctx context.Context, st Status) (changed bool, err error) {
	now := s.now().UTC()

	s.mu.Lock()
	wasOnline := s.live.Online && s.isFreshLocked(now)
	st.Online = true
	st.LastHeartbeat = &now
	s.live = st
	s.dirty = true
	changed = !wasOnline
	needFlush := changed || now.Sub(s.lastPersist) >= s.persistInterval
	s.mu.Unlock()

	if needFlush {
		if err := s.flush(ctx); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// Get returns the current status, deriving online from heartbeat freshness so a
// player that stopped reporting is not shown as online forever.
func (s *PlayerStore) Get() Status {
	now := s.now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.live
	st.Online = s.isFreshLocked(now)
	return st
}

func (s *PlayerStore) isFreshLocked(now time.Time) bool {
	if s.live.LastHeartbeat == nil {
		return false
	}
	return now.Sub(*s.live.LastHeartbeat) < OfflineAfter
}

// MarkOffline forces the offline state, e.g. when the SSE connection drops.
func (s *PlayerStore) MarkOffline(ctx context.Context) error {
	s.mu.Lock()
	if !s.live.Online {
		s.mu.Unlock()
		return nil
	}
	s.live.Online = false
	s.dirty = true
	s.mu.Unlock()
	return s.flush(ctx)
}

// SetDisplayOn records the scheduler's current display decision.
func (s *PlayerStore) SetDisplayOn(on bool) {
	s.mu.Lock()
	s.live.DisplayOn = on
	s.mu.Unlock()
}

// Flush persists the in-memory status if it has changed. Called periodically and
// during graceful shutdown.
func (s *PlayerStore) Flush(ctx context.Context) error { return s.flush(ctx) }

func (s *PlayerStore) flush(ctx context.Context) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	st := s.live
	s.dirty = false
	s.lastPersist = s.now().UTC()
	s.mu.Unlock()

	detail := "{}"
	if st.ZoneStates != nil {
		if b, err := json.Marshal(st.ZoneStates); err == nil {
			detail = string(b)
		}
	}
	var hb any
	if st.LastHeartbeat != nil {
		hb = rfc3339(*st.LastHeartbeat)
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE player_state
		   SET online = ?, last_heartbeat = ?, revision_id = ?, current_scene = ?,
		       current_slide = ?, browser_ua = ?, detail_json = ?, last_error = ?,
		       updated_at = ?
		 WHERE id = 1`,
		b2i(st.Online), hb, st.RevisionID, st.CurrentScene, st.CurrentSlide,
		st.BrowserUA, detail, st.LastError, rfc3339(s.now()))
	return err
}
