package api

import (
	"net/http"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/auth"
)

// routes registers every endpoint.
//
// The pattern is one route table so the authentication and role requirements of
// the whole surface can be read in a single place, rather than being scattered
// across handler files where a missing wrapper is easy to overlook.
func (s *Server) routes() {
	m := s.mux

	// ---- unauthenticated -------------------------------------------------
	m.HandleFunc("/health", methods("GET", s.handleHealth))
	m.HandleFunc("/api/v1/bootstrap/status", methods("GET", s.handleBootstrapStatus))
	m.HandleFunc("/api/v1/bootstrap", methods("POST", s.handleBootstrap))
	m.HandleFunc("/api/v1/auth/login", methods("POST", s.handleLogin))
	m.HandleFunc("/api/v1/auth/logout", methods("POST", s.handleLogout))

	// ---- session ---------------------------------------------------------
	m.HandleFunc("/api/v1/auth/me", s.requireAuth(auth.RoleViewer, methods("GET", s.handleMe)))
	m.HandleFunc("/api/v1/auth/password", s.requireAuth(auth.RoleViewer, methods("POST", s.handleChangePassword)))
	m.HandleFunc("/api/v1/auth/sessions", s.requireAuth(auth.RoleViewer, methods("GET,DELETE", s.handleSessions)))

	// ---- users (admin only) ----------------------------------------------
	m.HandleFunc("/api/v1/users", s.requireAuth(auth.RoleAdmin, methods("GET,POST", s.handleUsers)))
	m.HandleFunc("/api/v1/users/", s.requireAuth(auth.RoleAdmin, methods("GET,PATCH,DELETE", s.handleUserByID)))

	// ---- content reference data ------------------------------------------
	m.HandleFunc("/api/v1/layouts", s.requireAuth(auth.RoleViewer, methods("GET", s.handleLayouts)))
	m.HandleFunc("/api/v1/content-types", s.requireAuth(auth.RoleViewer, methods("GET", s.handleContentTypes)))

	// ---- playlist and scenes ---------------------------------------------
	m.HandleFunc("/api/v1/playlist", s.requireAuth(auth.RoleViewer, methods("GET", s.handleGetPlaylist)))
	m.HandleFunc("/api/v1/playlist/publish", s.requireAuth(auth.RoleEditor, methods("POST", s.handlePublish)))
	m.HandleFunc("/api/v1/playlist/discard", s.requireAuth(auth.RoleEditor, methods("POST", s.handleDiscardDraft)))
	m.HandleFunc("/api/v1/playlist/reorder", s.requireAuth(auth.RoleEditor, methods("POST", s.handleReorder)))
	m.HandleFunc("/api/v1/playlist/bulk", s.requireAuth(auth.RoleEditor, methods("POST", s.handleBulk)))
	m.HandleFunc("/api/v1/playlist/revisions", s.requireAuth(auth.RoleViewer, methods("GET", s.handleRevisions)))
	m.HandleFunc("/api/v1/playlist/rollback", s.requireAuth(auth.RoleAdmin, methods("POST", s.handleRollback)))

	m.HandleFunc("/api/v1/scenes", s.requireAuth(auth.RoleEditor, methods("POST", s.handleCreateScene)))
	m.HandleFunc("/api/v1/scenes/", s.requireAuth(auth.RoleEditor, methods("GET,PUT,DELETE,POST", s.handleSceneByID)))

	// ---- media -----------------------------------------------------------
	m.HandleFunc("/api/v1/media", s.requireAuth(auth.RoleViewer, methods("GET,POST", s.handleMedia)))
	m.HandleFunc("/api/v1/media/", s.requireAuth(auth.RoleViewer, methods("GET,PATCH,DELETE", s.handleMediaByID)))

	// Media files are served by database ID, never by a client-supplied path.
	m.HandleFunc("/media/file/", s.requireAnyViewer(s.handleMediaFile))
	m.HandleFunc("/media/thumb/", s.requireAnyViewer(s.handleMediaThumb))

	// ---- websites --------------------------------------------------------
	m.HandleFunc("/api/v1/websites", s.requireAuth(auth.RoleViewer, methods("GET,POST", s.handleWebsites)))
	m.HandleFunc("/api/v1/websites/", s.handleWebsiteRoute)

	// ---- social ----------------------------------------------------------
	m.HandleFunc("/api/v1/social/feeds", s.requireAuth(auth.RoleViewer, methods("GET,POST", s.handleSocialFeeds)))
	m.HandleFunc("/api/v1/social/feeds/", s.requireAuth(auth.RoleViewer, methods("GET,POST,DELETE", s.handleSocialFeedByID)))
	m.HandleFunc("/api/v1/social/posts/", s.requireAuth(auth.RoleEditor, methods("POST,DELETE", s.handleSocialPost)))
	m.HandleFunc("/api/v1/player/social/", s.requirePlayer(methods("GET", s.handleSocialPlayer)))

	// ---- backups ---------------------------------------------------------
	m.HandleFunc("/api/v1/backups", s.requireAuth(auth.RoleViewer, methods("GET,POST", s.handleBackups)))
	m.HandleFunc("/api/v1/backups/", s.requireAuth(auth.RoleViewer, methods("GET,POST,DELETE", s.handleBackupByID)))

	// ---- legacy migration (admin only) -----------------------------------
	m.HandleFunc("/api/v1/migrate/legacy", s.requireAuth(auth.RoleAdmin, methods("POST", s.handleLegacyImport)))

	// ---- QR generator ----------------------------------------------------
	m.HandleFunc("/api/v1/qr", s.requireAnyViewer(methods("GET", s.handleQR)))

	// ---- schedule --------------------------------------------------------
	m.HandleFunc("/api/v1/schedule", s.requireAuth(auth.RoleViewer, methods("GET", s.handleGetSchedule)))
	m.HandleFunc("/api/v1/schedule/rules", s.requireAuth(auth.RoleEditor, methods("POST", s.handleUpsertRule)))
	m.HandleFunc("/api/v1/schedule/rules/", s.requireAuth(auth.RoleEditor, methods("DELETE", s.handleDeleteRule)))
	m.HandleFunc("/api/v1/schedule/override", s.requireAuth(auth.RoleEditor, methods("POST,DELETE", s.handleOverride)))

	// ---- emergency (admin only: this pre-empts the whole playlist) --------
	m.HandleFunc("/api/v1/emergency", s.requireAuth(auth.RoleViewer, methods("GET", s.handleListEmergency)))
	m.HandleFunc("/api/v1/emergency/raise", s.requireAuth(auth.RoleAdmin, methods("POST", s.handleRaiseEmergency)))
	m.HandleFunc("/api/v1/emergency/", s.requireAuth(auth.RoleAdmin, methods("DELETE", s.handleDismissEmergency)))

	// ---- status ----------------------------------------------------------
	m.HandleFunc("/api/v1/overview", s.requireAuth(auth.RoleViewer, methods("GET", s.handleOverview)))
	m.HandleFunc("/api/v1/audit", s.requireAuth(auth.RoleAdmin, methods("GET", s.handleAudit)))
	m.HandleFunc("/api/v1/settings", s.requireAuth(auth.RoleViewer, methods("GET,POST", s.handleSettings)))

	// ---- realtime --------------------------------------------------------
	m.HandleFunc("/api/v1/events", s.requireAuth(auth.RoleViewer, methods("GET", s.handleAdminEvents)))
	m.HandleFunc("/api/v1/player/events", s.requirePlayer(methods("GET", s.handlePlayerEvents)))

	// ---- player (token authenticated) -------------------------------------
	m.HandleFunc("/api/v1/player/state", s.requirePlayer(methods("GET", s.handlePlayerState)))
	m.HandleFunc("/api/v1/player/heartbeat", s.requirePlayer(methods("POST", s.handleHeartbeat)))

	// ---- static ----------------------------------------------------------
	m.HandleFunc("/", s.handleStatic)
}

// handleWebsiteRoute dispatches /api/v1/websites/{id}/... .
//
// The /frame sub-path is reachable by the player token as well as a signed-in
// session, because the player renders it. Everything else needs a session, and the
// mutating verbs re-check for editor inside the handler. Splitting here keeps the
// player token off the management surface.
func (s *Server) handleWebsiteRoute(w http.ResponseWriter, r *http.Request) {
	action := actionFromPath(r.URL.Path, "/api/v1/websites/")
	if action == "frame" && r.Method == http.MethodGet {
		s.requireAnyViewer(s.handleWebsiteFrame)(w, r)
		return
	}
	s.requireAuth(auth.RoleViewer, methods("GET,PUT,DELETE,POST", s.handleWebsiteByID))(w, r)
}

// requireAnyViewer authenticates either an admin session or the player token.
//
// Media files are needed by both: the admin library shows thumbnails, and the
// player renders the originals. Requiring an admin session would break playback;
// leaving them public would expose uploaded content to anyone on the tailnet.
func (s *Server) requireAnyViewer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.playerTokenMatches(r.Header.Get("X-BeerMate-Player")) {
			next(w, r)
			return
		}
		if s.playerTokenMatches(r.URL.Query().Get("token")) {
			next(w, r)
			return
		}
		if _, _, err := s.deps.Sessions.Lookup(r.Context(), auth.SessionTokenFromRequest(r)); err == nil {
			next(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "authentication required")
	}
}
