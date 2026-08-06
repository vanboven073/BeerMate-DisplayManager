// Package realtime broadcasts state changes to the player and admin dashboards
// over Server-Sent Events.
//
// SSE rather than WebSocket: updates only ever flow server to client, and the
// browser's EventSource reconnects automatically with backoff. That is exactly
// the recovery behaviour required after a backend restart or a network drop,
// with no client reconnect logic to get wrong. The player reports its status
// back over ordinary POST requests.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Event names. Clients subscribe to all of them and switch on the type.
const (
	EventPlaylist  = "playlist"  // the published revision changed
	EventSchedule  = "schedule"  // schedule or display state changed
	EventEmergency = "emergency" // an override was raised or cleared
	EventWebsite   = "website"   // a managed website session changed state
	EventSocial    = "social"    // a feed refreshed or a post was moderated
	EventPlayer    = "player"    // player status changed (admin dashboards only)
	EventMedia     = "media"     // the media library changed
	EventPing      = "ping"      // keep-alive
	EventHello     = "hello"     // sent immediately on connect
)

// Audience selects which clients receive an event.
type Audience uint8

const (
	// ToAll broadcasts to every subscriber.
	ToAll Audience = iota
	// ToPlayer reaches only the player. Used for playback commands.
	ToPlayer
	// ToAdmin reaches only admin dashboards. Player status belongs here: the
	// player has no use for it, and it keeps admin-only detail off the display
	// connection.
	ToAdmin
)

// Message is one broadcast event.
type Message struct {
	Event    string   `json:"event"`
	Data     any      `json:"data"`
	Audience Audience `json:"-"`
}

// Role identifies a subscriber.
type Role uint8

const (
	// RolePlayer is the fullscreen display.
	RolePlayer Role = iota
	// RoleAdmin is a dashboard tab.
	RoleAdmin
)

// clientBuffer is how many messages may queue for one slow client before it is
// disconnected. A client that cannot keep up is dropped rather than allowed to
// grow an unbounded queue; EventSource will reconnect and resynchronise.
const clientBuffer = 16

// pingInterval keeps intermediaries from closing an idle connection and gives
// the client a liveness signal.
const pingInterval = 25 * time.Second

type client struct {
	id   uint64
	role Role
	ch   chan Message
}

// Hub fans messages out to subscribers.
type Hub struct {
	mu      sync.RWMutex
	clients map[uint64]*client
	nextID  atomic.Uint64
	maxSubs int
	log     *slog.Logger

	// revision is echoed in the hello message so a reconnecting client can tell
	// immediately whether it missed a publish while it was away.
	revision atomic.Int64
}

// NewHub builds a Hub allowing at most maxSubscribers concurrent clients.
func NewHub(maxSubscribers int, log *slog.Logger) *Hub {
	if maxSubscribers < 2 {
		maxSubscribers = 2
	}
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		clients: make(map[uint64]*client),
		maxSubs: maxSubscribers,
		log:     log,
	}
}

// SetRevision records the currently published playlist revision.
func (h *Hub) SetRevision(rev int64) { h.revision.Store(rev) }

// Revision returns the last published revision the hub knows about.
func (h *Hub) Revision() int64 { return h.revision.Load() }

// ErrTooManySubscribers is returned when the subscriber cap is reached.
var ErrTooManySubscribers = fmt.Errorf("realtime: subscriber limit reached")

func (h *Hub) subscribe(role Role) (*client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.maxSubs {
		return nil, ErrTooManySubscribers
	}
	c := &client{
		id:   h.nextID.Add(1),
		role: role,
		ch:   make(chan Message, clientBuffer),
	}
	h.clients[c.id] = c
	return c, nil
}

func (h *Hub) unsubscribe(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c.id]; ok {
		delete(h.clients, c.id)
		close(c.ch)
	}
	h.mu.Unlock()
}

// Broadcast delivers a message to the matching audience.
//
// It never blocks: a client whose buffer is full is dropped. Blocking here would
// let one stalled browser tab stall every publish in the system.
//
// The sends happen while the read lock is still held. Selecting targets under the
// lock and then sending after releasing it would be a use-after-close: a client
// that disconnects in that window has its channel closed by unsubscribe, and the
// send panics. Every closer (unsubscribe, Close) takes the write lock, so holding
// the read lock across the sends is what makes the channel safe to touch. It costs
// nothing in latency because the sends are non-blocking.
func (h *Hub) Broadcast(m Message) {
	var stalled []*client

	h.mu.RLock()
	for _, c := range h.clients {
		if !m.matches(c.role) {
			continue
		}
		select {
		case c.ch <- m:
		default:
			stalled = append(stalled, c)
		}
	}
	h.mu.RUnlock()

	// Dropped outside the read lock: unsubscribe needs the write lock.
	for _, c := range stalled {
		h.log.Warn("dropping slow realtime subscriber", "client", c.id, "role", c.role)
		h.unsubscribe(c)
	}
}

func (m Message) matches(r Role) bool {
	switch m.Audience {
	case ToPlayer:
		return r == RolePlayer
	case ToAdmin:
		return r == RoleAdmin
	default:
		return true
	}
}

// Count returns the number of connected subscribers.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// CountByRole returns subscriber counts per role.
func (h *Hub) CountByRole() (players, admins int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients {
		if c.role == RolePlayer {
			players++
		} else {
			admins++
		}
	}
	return players, admins
}

// ServeHTTP streams events to one client until the request context is cancelled.
//
// The caller is responsible for authentication and for choosing the role.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, role Role) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	c, err := h.subscribe(role)
	if err != nil {
		// 503 rather than 429: this is a server capacity limit, and EventSource
		// will retry on its own.
		http.Error(w, "too many event subscribers", http.StatusServiceUnavailable)
		return
	}
	defer h.unsubscribe(c)

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	hdr.Set("Connection", "keep-alive")
	// Without this, any buffering proxy would hold the stream and the player
	// would appear frozen while updates queue upstream.
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Tell the browser how long to wait before reconnecting.
	fmt.Fprintf(w, "retry: 3000\n\n")

	writeEvent(w, EventHello, map[string]any{
		"revision":    h.revision.Load(),
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
	flusher.Flush()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case m, open := <-c.ch:
			if !open {
				return
			}
			if err := writeEvent(w, m.Event, m.Data); err != nil {
				return
			}
			flusher.Flush()

		case <-ticker.C:
			if err := writeEvent(w, EventPing, map[string]any{
				"t": time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEvent emits one SSE frame.
func writeEvent(w http.ResponseWriter, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// SSE frames are newline-delimited, so any newline inside the payload would
	// truncate the frame. json.Marshal escapes newlines inside strings, so the
	// encoded form is always single-line.
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	return nil
}

// Close disconnects every subscriber. Called during graceful shutdown so clients
// reconnect to the new process rather than hanging on a dead socket.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.clients {
		close(c.ch)
		delete(h.clients, id)
	}
}

// NotifyPlaylist announces a new published revision.
func (h *Hub) NotifyPlaylist(ctx context.Context, revision int64) {
	_ = ctx
	h.SetRevision(revision)
	h.Broadcast(Message{
		Event:    EventPlaylist,
		Audience: ToAll,
		Data:     map[string]any{"revision": revision},
	})
}

// NotifySchedule announces a display state change.
func (h *Hub) NotifySchedule(on bool, reason string) {
	h.Broadcast(Message{
		Event:    EventSchedule,
		Audience: ToAll,
		Data:     map[string]any{"display_on": on, "reason": reason},
	})
}

// NotifyEmergency announces an emergency override change.
func (h *Hub) NotifyEmergency(active bool, payload any) {
	h.Broadcast(Message{
		Event:    EventEmergency,
		Audience: ToAll,
		Data:     map[string]any{"active": active, "message": payload},
	})
}

// NotifyWebsite announces a website session state change.
func (h *Hub) NotifyWebsite(id int64, state string) {
	h.Broadcast(Message{
		Event:    EventWebsite,
		Audience: ToAll,
		Data:     map[string]any{"id": id, "session_state": state},
	})
}

// NotifySocial announces a social feed change.
func (h *Hub) NotifySocial(feedID int64, reason string) {
	h.Broadcast(Message{
		Event:    EventSocial,
		Audience: ToAll,
		Data:     map[string]any{"feed_id": feedID, "reason": reason},
	})
}

// NotifyPlayer forwards player status to admin dashboards only.
func (h *Hub) NotifyPlayer(status any) {
	h.Broadcast(Message{Event: EventPlayer, Audience: ToAdmin, Data: status})
}

// NotifyMedia announces a media library change to admin dashboards.
func (h *Hub) NotifyMedia(reason string) {
	h.Broadcast(Message{
		Event: EventMedia, Audience: ToAdmin,
		Data: map[string]any{"reason": reason},
	})
}
