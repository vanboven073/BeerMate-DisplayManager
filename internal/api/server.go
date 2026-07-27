// Package api exposes the REST API, the embedded admin and player apps, and the
// health endpoint.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/auth"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/browser"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/config"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/display"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/media"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/realtime"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/schedule"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/social"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/version"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Config    config.Config
	Log       *slog.Logger
	Users     *auth.UserStore
	Sessions  *auth.SessionStore
	Throttle  *auth.Throttle
	Playlist  *store.PlaylistStore
	Media     *media.Store
	Settings  *store.SettingsStore
	Schedule  *store.ScheduleStore
	Emergency *store.EmergencyStore
	Audit     *store.AuditStore
	Player    *store.PlayerStore
	Hub       *realtime.Hub
	Engine    *schedule.Engine
	Display   display.Controller
	Websites  *store.WebsiteStore
	Browser   browser.Manager
	Social    *social.Service
	Backups   *store.BackupStore
	// HealthDB reports database reachability without exposing the handle.
	HealthDB func(context.Context) error
	// StorageUsage reports bytes used and free for the data directory.
	StorageUsage func() (used, free int64, err error)
}

// Server wires the HTTP routes.
type Server struct {
	deps Deps
	mux  *http.ServeMux
	// playerToken authorises the player endpoints. It is generated at startup and
	// handed to the player page, so a browser on the tailnet cannot poll playback
	// state or post heartbeats without it.
	playerToken string
}

// New builds a Server.
func New(deps Deps, playerToken string) *Server {
	s := &Server{deps: deps, mux: http.NewServeMux(), playerToken: playerToken}
	s.routes()
	return s
}

// Handler returns the root handler with global middleware applied.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.securityHeaders(s.requestLogger(s.mux)))
}

// ---- middleware ----------------------------------------------------------

// securityHeaders applies response headers to every route.
//
// The CSP differs by route: /admin is locked down, while /player must be allowed
// to frame third-party sites for embeddable website slides. Applying the admin
// policy globally would break the player; applying the player policy globally
// would weaken the admin surface.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// The admin UI is never legitimately framed. The player is only ever
		// framed by itself, and its own document is same-origin.
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy",
			"camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")

		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/player"):
			// frame-src * is required: an embeddable website slide is rendered in
			// an iframe pointing at an operator-configured third-party origin.
			// Scripts and styles stay locked to self.
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data: blob: https:; "+
					"media-src 'self' blob:; "+
					"font-src 'self'; "+
					"connect-src 'self'; "+
					"frame-src https: http:; "+
					"base-uri 'none'; "+
					"form-action 'none'; "+
					"object-src 'none'")
		case strings.HasPrefix(path, "/api/"), strings.HasPrefix(path, "/health"):
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		default:
			// Admin. 'unsafe-inline' for styles only: the SPA sets computed styles
			// for brand tokens. No inline script is permitted.
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data: blob:; "+
					"font-src 'self'; "+
					"connect-src 'self'; "+
					"frame-src 'self'; "+
					"frame-ancestors 'none'; "+
					"base-uri 'none'; "+
					"form-action 'self'; "+
					"object-src 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a handler panic into a 500 instead of killing the process.
// An unattended display must not go dark because one request hit a nil map.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.deps.Log.Error("panic in handler",
					"path", r.URL.Path, "method", r.Method, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer so SSE keeps working through the
// wrapper. Without this the event stream buffers and the player appears frozen.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Health and event streams are noisy and uninformative at info level.
		if r.URL.Path == "/health" || strings.HasSuffix(r.URL.Path, "/events") {
			return
		}
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		// The query string is deliberately omitted: it can carry tokens.
		s.deps.Log.Log(r.Context(), level, "request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(), "ip", clientIP(r, s.deps.Config.TrustProxyHeaders))
	})
}

// clientIP resolves the caller's address.
//
// Proxy headers are only trusted when explicitly configured. With a direct
// Tailscale connection, honouring X-Forwarded-For would let any client spoof its
// address and defeat per-IP login throttling entirely.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- auth middleware -----------------------------------------------------

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

// userFrom returns the authenticated user from the request context.
func userFrom(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(ctxUser).(auth.User)
	return u, ok
}

func sessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(ctxSession).(auth.Session)
	return s, ok
}

// requireAuth authenticates the request and enforces the minimum role.
//
// State-changing methods additionally require a valid CSRF token. The check lives
// here rather than in each handler so a new endpoint cannot forget it.
func (s *Server) requireAuth(minRole string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := auth.SessionTokenFromRequest(r)
		sess, user, err := s.deps.Sessions.Lookup(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !auth.RoleAtLeast(user.Role, minRole) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		if isStateChanging(r.Method) {
			if err := auth.ValidateCSRF(sess, auth.CSRFFromRequest(r)); err != nil {
				writeError(w, http.StatusForbidden, "CSRF validation failed")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next(w, r.WithContext(ctx))
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// requirePlayer authorises the player endpoints with the startup token.
func (s *Server) requirePlayer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get("X-BeerMate-Player")
		if presented == "" {
			presented = r.URL.Query().Get("token")
		}
		if s.playerToken == "" || presented != s.playerToken {
			writeError(w, http.StatusUnauthorized, "player token required")
			return
		}
		next(w, r)
	}
}

// methods restricts a handler to specific HTTP methods.
func methods(allowed string, h http.HandlerFunc) http.HandlerFunc {
	set := strings.Split(allowed, ",")
	return func(w http.ResponseWriter, r *http.Request) {
		for _, m := range set {
			if r.Method == strings.TrimSpace(m) {
				h(w, r)
				return
			}
		}
		w.Header().Set("Allow", allowed)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- responses -----------------------------------------------------------

// errorBody is the uniform API error shape.
type errorBody struct {
	Error  string `json:"error"`
	Detail any    `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written, so this can only be logged.
		slog.Default().Error("encoding response failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func writeErrorDetail(w http.ResponseWriter, status int, msg string, detail any) {
	writeJSON(w, status, errorBody{Error: msg, Detail: detail})
}

// maxJSONBody bounds request bodies. Generous enough for a scene with many
// zones, small enough that a malicious client cannot exhaust memory.
const maxJSONBody = 1 << 20 // 1 MiB

// decodeJSON reads and strictly decodes a request body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Reject trailing content so a body containing two JSON documents cannot be
	// interpreted differently by different readers.
	if dec.More() {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

// ---- health --------------------------------------------------------------

// healthResponse is the /health payload. It deliberately carries no secrets:
// no tokens, cookies, credentials or user records.
type healthResponse struct {
	Status      string        `json:"status"`
	Version     version.Info  `json:"build"`
	ServerTime  string        `json:"server_time"`
	Timezone    string        `json:"timezone"`
	Database    string        `json:"database"`
	Revision    int64         `json:"playlist_revision"`
	Player      playerHealth  `json:"player"`
	Storage     storageHealth `json:"storage"`
	Display     displayHealth `json:"display"`
	Browser     string        `json:"browser_control"`
	Social      string        `json:"social_adapters"`
	WebsiteWarn int           `json:"website_session_warnings"`
	Subscribers int           `json:"event_subscribers"`
}

type playerHealth struct {
	Online        bool   `json:"online"`
	LastHeartbeat string `json:"last_heartbeat,omitempty"`
	CurrentScene  string `json:"current_scene,omitempty"`
}

type storageHealth struct {
	UsedBytes int64  `json:"used_bytes"`
	FreeBytes int64  `json:"free_bytes"`
	State     string `json:"state"`
}

type displayHealth struct {
	On     bool   `json:"on"`
	Driver string `json:"driver"`
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := healthResponse{
		Status:     "ok",
		Version:    version.Get(),
		ServerTime: time.Now().UTC().Format(time.RFC3339),
		Timezone:   s.deps.Config.Timezone,
		Database:   "ok",
		Revision:   s.deps.Hub.Revision(),
	}

	if s.deps.HealthDB != nil {
		if err := s.deps.HealthDB(ctx); err != nil {
			// The specific error is logged, not returned: it can name filesystem
			// paths, and /health is unauthenticated.
			s.deps.Log.Error("health: database check failed", "error", err)
			resp.Database = "error"
			resp.Status = "degraded"
		}
	}

	st := s.deps.Player.Get()
	resp.Player = playerHealth{Online: st.Online, CurrentScene: st.CurrentScene}
	if st.LastHeartbeat != nil {
		resp.Player.LastHeartbeat = st.LastHeartbeat.Format(time.RFC3339)
	}
	if !st.Online {
		resp.Status = "degraded"
	}

	if s.deps.StorageUsage != nil {
		used, free, err := s.deps.StorageUsage()
		resp.Storage = storageHealth{UsedBytes: used, FreeBytes: free, State: "ok"}
		switch {
		case err != nil:
			resp.Storage.State = "unknown"
		case free < s.deps.Config.LowDiskWarnBytes:
			resp.Storage.State = "low"
			resp.Status = "degraded"
		}
	}

	rules, _ := s.deps.Schedule.Rules(ctx)
	overrides, _ := s.deps.Schedule.ActiveOverrides(ctx)
	d := s.deps.Engine.Decide(time.Now(), rules, overrides)
	resp.Display = displayHealth{On: d.On, Driver: s.deps.Display.Name(), Reason: d.Reason}

	if s.deps.Browser != nil {
		state, _ := s.deps.Browser.Status()
		resp.Browser = string(state)
	} else {
		resp.Browser = "disabled"
	}
	resp.Social = "ok"
	if s.deps.Websites != nil {
		if n, err := s.deps.Websites.WarningCount(ctx); err == nil {
			resp.WebsiteWarn = n
			if n > 0 {
				resp.Status = "degraded"
			}
		}
	}
	resp.Subscribers = s.deps.Hub.Count()

	status := http.StatusOK
	if resp.Status != "ok" {
		// 200 with a degraded body: a monitoring probe should see the service is
		// answering. Reserved 503 for a genuinely unusable service.
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

// cookieSecure decides the Secure cookie flag.
//
// Auto-detected per request unless pinned in configuration. On the Jetson the
// admin UI is served over plain HTTP inside the Tailscale tunnel, where Tailscale
// provides the transport encryption; forcing Secure there would silently break
// login.
func (s *Server) cookieSecure(r *http.Request) bool {
	if s.deps.Config.CookieSecure != nil {
		return *s.deps.Config.CookieSecure
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") && s.deps.Config.TrustProxyHeaders
}
