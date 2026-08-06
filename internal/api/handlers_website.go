package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/browser"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

// captureTimeout bounds a single managed-website screenshot so a hung page
// cannot wedge the player's request.
const captureTimeout = 20 * time.Second

func (s *Server) handleWebsites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		sites, err := s.deps.Websites.List(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list websites")
			return
		}
		if sites == nil {
			sites = []store.Website{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"websites": sites})
		return
	}

	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var req store.Website
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.CreatedBy = u.Username
	site, err := s.deps.Websites.Create(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "website_created",
		TargetType: "website", TargetID: strconv.FormatInt(site.ID, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"website": site})
}

func (s *Server) handleWebsiteByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/websites/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid website id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)
	action := actionFromPath(r.URL.Path, "/api/v1/websites/")

	switch {
	case r.Method == http.MethodGet && action == "":
		site, err := s.deps.Websites.Get(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "website not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"website": site})

	case r.Method == http.MethodPut && action == "":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		var req store.Website
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		req.ID = id
		if err := s.deps.Websites.Update(ctx, req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		site, _ := s.deps.Websites.Get(ctx, id)
		writeJSON(w, http.StatusOK, map[string]any{"website": site})

	case r.Method == http.MethodDelete && action == "":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		inUse, _ := s.deps.Playlist.IsWebsiteReferenced(ctx, id)
		if inUse && r.URL.Query().Get("force") != "true" {
			writeError(w, http.StatusConflict,
				"this website is used by one or more scenes; remove it there first or force the delete")
			return
		}
		if err := s.deps.Websites.Delete(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "website not found")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "website_deleted",
			TargetType: "website", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case r.Method == http.MethodPost:
		s.handleWebsiteAction(w, r, id, action)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleWebsiteAction dispatches the session-lifecycle verbs.
func (s *Server) handleWebsiteAction(w http.ResponseWriter, r *http.Request, id int64, action string) {
	ctx := r.Context()
	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	site, err := s.deps.Websites.Get(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "website not found")
		return
	}

	switch action {
	case "prepare-login":
		// Run interactively so MFA, SSO and consent screens can be completed by a
		// human at the Jetson. The call returns once the browser is open.
		if err := s.deps.Browser.PrepareLogin(ctx, site.ProfileID, site.URL); err != nil {
			s.reportBrowserErr(ctx, id, err)
			writeError(w, http.StatusServiceUnavailable, browserErrMessage(err))
			return
		}
		_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionPreparing, "interactive login opened")
		s.audit(ctx, r, u.Username, "website_login_prepared", id)
		s.deps.Hub.NotifyWebsite(id, store.SessionPreparing)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"message": "The website is now open on the Jetson display. Log in there (locally or over " +
				"an independently secured remote desktop), then select Finish login.",
		})

	case "finish-login":
		if err := s.deps.Browser.FinishLogin(ctx, site.ProfileID); err != nil {
			s.reportBrowserErr(ctx, id, err)
			writeError(w, http.StatusBadRequest, browserErrMessage(err))
			return
		}
		// Validate immediately: the point of finishing is to confirm the stored
		// session actually opens the target without bouncing to a login page.
		vctx, cancel := timeoutCtx(ctx, 30*time.Second)
		ok, verr := s.deps.Browser.Validate(vctx, site.ProfileID, site.URL, site.LoginURLPattern)
		cancel()
		if verr != nil {
			// The login itself succeeded and the profile is persisted, so this is a
			// 200 with an unvalidated state — not an error response. Returning the
			// error envelope under a 200 gave the client a body it reads as success
			// with no ok field and an error string it never surfaces.
			_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionError, "validation could not run")
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":            true,
				"session_state": store.SessionError,
				"validated":     false,
				"message":       "Login saved, but validation could not run. Try Validate again.",
			})
			return
		}
		if !ok {
			_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionReauthRequired, "still redirecting to login")
			s.deps.Hub.NotifyWebsite(id, store.SessionReauthRequired)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "session_state": store.SessionReauthRequired,
				"message": "The site still redirects to a login page. The login may not have completed.",
			})
			return
		}
		_ = s.deps.Websites.MarkValidated(ctx, id)
		s.audit(ctx, r, u.Username, "website_login_finished", id)
		s.deps.Hub.NotifyWebsite(id, store.SessionActive)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session_state": store.SessionActive})

	case "validate":
		vctx, cancel := timeoutCtx(ctx, 30*time.Second)
		ok, verr := s.deps.Browser.Validate(vctx, site.ProfileID, site.URL, site.LoginURLPattern)
		cancel()
		if verr != nil {
			s.reportBrowserErr(ctx, id, verr)
			writeError(w, http.StatusServiceUnavailable, browserErrMessage(verr))
			return
		}
		state := store.SessionReauthRequired
		if ok {
			_ = s.deps.Websites.MarkValidated(ctx, id)
			state = store.SessionActive
		} else {
			_ = s.deps.Websites.SetSessionState(ctx, id, state, "redirects to login")
		}
		s.deps.Hub.NotifyWebsite(id, state)
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "session_state": state})

	case "clear-session":
		if err := s.deps.Browser.ClearSession(ctx, site.ProfileID); err != nil {
			s.reportBrowserErr(ctx, id, err)
			writeError(w, http.StatusServiceUnavailable, browserErrMessage(err))
			return
		}
		_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionNone, "session cleared")
		s.audit(ctx, r, u.Username, "website_session_cleared", id)
		s.deps.Hub.NotifyWebsite(id, store.SessionNone)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusNotFound, "unknown website action")
	}
}

// handleWebsiteFrame serves the player's view of a website.
//
// For an embeddable site it redirects to the target URL so the iframe loads it
// directly. For a managed site it returns the latest capture as a JPEG. Either
// way the player never receives cookies or tokens for the site.
func (s *Server) handleWebsiteFrame(w http.ResponseWriter, r *http.Request) {
	// Reachable by the player token or an admin session; website content is not
	// itself secret, but the endpoint should not be world-open on the tailnet.
	id, ok := idFromPath(r.URL.Path, "/api/v1/websites/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid website id")
		return
	}
	ctx := r.Context()
	site, err := s.deps.Websites.Get(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "website not found")
		return
	}

	if site.RenderMode == store.RenderIframe && !site.RequiresAuth {
		// The player frames the site directly. A redirect keeps this endpoint out
		// of the data path entirely for cheap embeddable sites.
		http.Redirect(w, r, site.URL, http.StatusFound)
		return
	}

	// Managed capture. If the session needs reauthentication, serve nothing and
	// let the player show its branded fallback rather than a login page.
	if site.SessionState == store.SessionReauthRequired || site.SessionState == store.SessionExpired {
		writeError(w, http.StatusConflict, "reauthentication required")
		return
	}

	cctx, cancel := timeoutCtx(ctx, captureTimeout)
	defer cancel()
	shot, err := s.deps.Browser.Capture(cctx, site.ProfileID, site.URL)
	if err != nil {
		if errors.Is(err, browser.ErrDisabled) {
			writeError(w, http.StatusServiceUnavailable, "managed browser is disabled")
			return
		}
		s.reportBrowserErr(ctx, id, err)
		writeError(w, http.StatusBadGateway, "could not capture the website")
		return
	}

	// A stored session that succeeded should clear any earlier warning.
	if site.SessionState != store.SessionActive && site.RequiresAuth {
		_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionActive, "")
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(shot)
}

func (s *Server) reportBrowserErr(ctx context.Context, id int64, err error) {
	// Errors are redacted of query strings before storage: a navigated URL can
	// carry a one-time login token.
	_ = s.deps.Websites.SetSessionState(ctx, id, store.SessionError, browserErrMessage(err))
}

// browserErrMessage strips a possible token-bearing query string from an error.
func browserErrMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "?"); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// timeoutCtx derives a child context with a timeout.
func timeoutCtx(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// audit records a website action.
func (s *Server) audit(ctx context.Context, r *http.Request, actor, action string, id int64) {
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: actor, Action: action,
		TargetType: "website", TargetID: strconv.FormatInt(id, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
}
