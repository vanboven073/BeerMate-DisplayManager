package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/social"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

func (s *Server) handleSocialFeeds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		feeds, err := s.deps.Social.ListFeeds(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list feeds")
			return
		}
		if feeds == nil {
			feeds = []social.Feed{}
		}
		// Attach pending-moderation counts so the dashboard can badge them.
		pending, _ := s.deps.Social.PendingCount(ctx)
		writeJSON(w, http.StatusOK, map[string]any{
			"feeds":         feeds,
			"platforms":     social.Platforms(),
			"pending_total": pending,
		})
		return
	}

	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	// The token is accepted in the body but never echoed back or logged.
	var req struct {
		social.Feed
		Token string `json:"token,omitempty"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	feed, err := s.deps.Social.CreateFeed(ctx, req.Feed, req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "social_feed_created",
		TargetType: "feed", TargetID: strconv.FormatInt(feed.ID, 10),
		Detail: "platform=" + feed.Platform,
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"feed": feed})
}

func (s *Server) handleSocialFeedByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/social/feeds/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid feed id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)
	action := actionFromPath(r.URL.Path, "/api/v1/social/feeds/")

	switch {
	case r.Method == http.MethodGet && action == "posts":
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "pending"
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		posts, err := s.deps.Social.Posts(ctx, id, state, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list posts")
			return
		}
		if posts == nil {
			posts = []social.StoredPost{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"posts": posts})

	case r.Method == http.MethodPost && action == "refresh":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		feed, err := s.deps.Social.GetFeed(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		rctx, cancel := timeoutCtx(ctx, 25*time.Second)
		err = s.deps.Social.Refresh(rctx, feed)
		cancel()
		if err != nil {
			// A refresh failure is reported but not fatal: cached posts remain.
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case r.Method == http.MethodPost && action == "enable":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		_ = s.deps.Social.SetEnabled(ctx, id, true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case r.Method == http.MethodPost && action == "disable":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		_ = s.deps.Social.SetEnabled(ctx, id, false)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case r.Method == http.MethodDelete && action == "":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		if err := s.deps.Social.DeleteFeed(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "social_feed_deleted",
			TargetType: "feed", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type moderateRequest struct {
	State  string `json:"state"` // approved | rejected | hidden | pending
	Pinned *bool  `json:"pinned,omitempty"`
}

func (s *Server) handleSocialPost(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/social/posts/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req moderateRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.State != "" {
			if err := s.deps.Social.Moderate(ctx, id, req.State, u.Username); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.Pinned != nil {
			if err := s.deps.Social.SetPinned(ctx, id, *req.Pinned); err != nil {
				writeError(w, http.StatusBadRequest, "could not update pin state")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		if err := s.deps.Social.DeletePost(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete the post")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSocialPlayer returns approved posts for a feed, for the player.
func (s *Server) handleSocialPlayer(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/player/social/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid feed id")
		return
	}
	limit := int64(20)
	if n, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && n > 0 && n <= 50 {
		limit = n
	}
	posts, err := s.deps.Social.ApprovedForDisplay(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load posts")
		return
	}
	if posts == nil {
		posts = []social.StoredPost{}
	}
	// no-store: moderation changes should reach the display promptly.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}
