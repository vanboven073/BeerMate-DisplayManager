package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/auth"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

func (s *Server) handleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	need, err := s.deps.Users.NeedsBootstrap(r.Context())
	if err != nil {
		s.deps.Log.Error("bootstrap status", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read setup state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needs_bootstrap": need})
}

type bootstrapRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req bootstrapRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	u, err := s.deps.Users.Bootstrap(ctx, req.Username, req.DisplayName, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrBootstrapDone) {
			writeError(w, http.StatusConflict,
				"an administrator already exists; use the login page")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ip := clientIP(r, s.deps.Config.TrustProxyHeaders)
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "bootstrap",
		TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10), IP: ip,
	})

	s.issueSession(w, r, u)
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	ip := clientIP(r, s.deps.Config.TrustProxyHeaders)

	if err := s.deps.Throttle.Check(ctx, ip, req.Username); err != nil {
		var rl *auth.ErrRateLimited
		if errors.As(err, &rl) {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests,
				"too many failed sign-in attempts; try again in "+
					rl.RetryAfter.Round(time.Second).String())
			return
		}
		s.deps.Log.Error("throttle check", "error", err)
		writeError(w, http.StatusInternalServerError, "sign-in unavailable")
		return
	}

	u, err := s.deps.Users.Authenticate(ctx, req.Username, req.Password)
	if err != nil {
		_ = s.deps.Throttle.RecordFailure(ctx, ip, req.Username)
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: req.Username, Action: "login_failed", IP: ip,
		})
		// One message for every failure mode. Distinguishing "no such user" from
		// "wrong password" from "disabled" enumerates accounts.
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}

	_ = s.deps.Throttle.Reset(ctx, ip, req.Username)
	_ = s.deps.Users.RecordLogin(ctx, u.ID)
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "login",
		TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10), IP: ip,
	})

	if err := s.issueSession(w, r, u); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

// issueSession creates a session and sets both cookies.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u auth.User) error {
	token, sess, err := s.deps.Sessions.Create(r.Context(), u.ID,
		clientIP(r, s.deps.Config.TrustProxyHeaders), r.UserAgent())
	if err != nil {
		s.deps.Log.Error("create session", "error", err)
		return err
	}
	secure := s.cookieSecure(r)
	opts := auth.CookieOptions{Secure: secure, MaxAge: s.deps.Config.SessionMaxLifetime}
	auth.SetSessionCookie(w, token, opts)
	auth.SetCSRFCookie(w, auth.CSRFToken(sess), opts)
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := auth.SessionTokenFromRequest(r)
	ctx := r.Context()

	// Logout must work even with a stale CSRF token: refusing would strand a user
	// with a session they cannot end. The action is idempotent and only ever
	// removes access, so the CSRF risk is nil.
	if token != "" {
		if sess, u, err := s.deps.Sessions.Lookup(ctx, token); err == nil {
			_ = sess
			_ = s.deps.Audit.Record(ctx, store.AuditEntry{
				ActorName: u.Username, Action: "logout",
				IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
			})
		}
		_ = s.deps.Sessions.Revoke(ctx, token)
	}
	auth.ClearAuthCookies(w, s.cookieSecure(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	sess, _ := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       u,
		"csrf_token": auth.CSRFToken(sess),
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if err := s.deps.Users.ChangePassword(ctx, u.ID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, auth.ErrPasswordMismatch) {
			writeError(w, http.StatusForbidden, "the current password is incorrect")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Every other session for this user is revoked: a session that outlives the
	// credential it was created with defeats the point of changing it. The
	// current session survives so the user is not logged out of the tab they are
	// using.
	token := auth.SessionTokenFromRequest(r)
	if err := s.deps.Sessions.RevokeAllForUserExcept(ctx, u.ID, token); err != nil {
		s.deps.Log.Error("revoke other sessions", "error", err)
	}

	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "password_changed",
		TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if r.Method == http.MethodDelete {
		token := auth.SessionTokenFromRequest(r)
		if err := s.deps.Sessions.RevokeAllForUserExcept(ctx, u.ID, token); err != nil {
			writeError(w, http.StatusInternalServerError, "could not revoke sessions")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "sessions_revoked",
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	sessions, err := s.deps.Sessions.ActiveSessions(ctx, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	// The token hash is never returned: it is the storage key for the session and
	// has no business leaving the server.
	out := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, map[string]any{
			"created_at":   sess.CreatedAt,
			"last_seen_at": sess.LastSeenAt,
			"expires_at":   sess.AbsoluteExp,
			"ip":           sess.IP,
			"user_agent":   sess.UserAgent,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// ---- user management -----------------------------------------------------

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor, _ := userFrom(ctx)

	if r.Method == http.MethodGet {
		users, err := s.deps.Users.List(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list users")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
		return
	}

	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, err := s.deps.Users.Create(ctx, req.Username, req.DisplayName, req.Password, req.Role)
	if err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			writeError(w, http.StatusConflict, "that username is already taken")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: actor.Username, Action: "user_created",
		TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
		Detail: "role=" + u.Role,
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

type patchUserRequest struct {
	Role        *string `json:"role,omitempty"`
	Disabled    *bool   `json:"disabled,omitempty"`
	NewPassword *string `json:"new_password,omitempty"`
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	ctx := r.Context()
	actor, _ := userFrom(ctx)

	switch r.Method {
	case http.MethodGet:
		u, err := s.deps.Users.Get(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": u})

	case http.MethodPatch:
		var req patchUserRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Role != nil {
			if err := s.deps.Users.SetRole(ctx, id, *req.Role); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.Disabled != nil {
			if err := s.deps.Users.SetDisabled(ctx, id, *req.Disabled); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if *req.Disabled {
				// A disabled account must lose its live sessions immediately,
				// otherwise the flag only takes effect at the next login.
				_ = s.deps.Sessions.RevokeAllForUser(ctx, id)
			}
		}
		if req.NewPassword != nil {
			if err := s.deps.Users.SetPassword(ctx, id, *req.NewPassword); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			_ = s.deps.Sessions.RevokeAllForUser(ctx, id)
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: actor.Username, Action: "user_updated",
			TargetType: "user", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		u, _ := s.deps.Users.Get(ctx, id)
		writeJSON(w, http.StatusOK, map[string]any{"user": u})

	case http.MethodDelete:
		if id == actor.ID {
			writeError(w, http.StatusBadRequest, "you cannot delete your own account")
			return
		}
		if err := s.deps.Users.Delete(ctx, id); err != nil {
			if errors.Is(err, auth.ErrUserNotFound) {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = s.deps.Sessions.RevokeAllForUser(ctx, id)
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: actor.Username, Action: "user_deleted",
			TargetType: "user", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// idFromPath extracts a trailing numeric id after a prefix.
func idFromPath(path, prefix string) (int64, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return 0, false
	}
	// Allow a sub-action segment, e.g. /api/v1/scenes/12/duplicate.
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// actionFromPath returns the segment after the id, if any.
func actionFromPath(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return ""
	}
	return strings.Trim(rest[i+1:], "/")
}
