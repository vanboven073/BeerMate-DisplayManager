package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/content"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/media"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/realtime"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/schedule"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

// ---- reference data ------------------------------------------------------

func (s *Server) handleLayouts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"layouts": content.Layouts()})
}

func (s *Server) handleContentTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"types":                content.AllTypes(),
		"transitions":          content.Transitions,
		"image_text_templates": content.ImageTextTemplates,
		"social_templates":     content.SocialTemplates,
		"limits": map[string]any{
			"min_duration_ms":  content.MinDurationMS,
			"max_duration_ms":  content.MaxDurationMS,
			"max_custom_zones": content.MaxCustomZones,
			"max_kpi_cards":    content.MaxKPICards,
			"max_heading_len":  content.MaxHeadingLen,
			"max_body_len":     content.MaxBodyLen,
		},
	})
}

// ---- playlist ------------------------------------------------------------

func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	which := r.URL.Query().Get("revision")

	var rev store.Revision
	var err error
	switch which {
	case "", "draft":
		rev, err = s.deps.Playlist.DraftRevision(ctx)
	case "published", "live":
		rev, err = s.deps.Playlist.PublishedRevision(ctx)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"revision": nil, "scenes": []any{}})
			return
		}
	default:
		id, perr := strconv.ParseInt(which, 10, 64)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "revision must be draft, published or a numeric id")
			return
		}
		rev, err = s.deps.Playlist.GetRevision(ctx, id)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "revision not found")
			return
		}
		s.deps.Log.Error("load playlist", "error", err)
		writeError(w, http.StatusInternalServerError, "could not load the playlist")
		return
	}

	scenes, err := s.deps.Playlist.ListScenes(ctx, rev.ID)
	if err != nil {
		s.deps.Log.Error("list scenes", "error", err)
		writeError(w, http.StatusInternalServerError, "could not load scenes")
		return
	}
	if scenes == nil {
		scenes = []content.Scene{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "scenes": scenes})
}

type publishRequest struct {
	Note string `json:"note"`
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	res, err := s.deps.Playlist.Publish(ctx, u.Username, req.Note)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// Retention runs after a successful publish rather than on a timer: this is
	// the only moment revisions are created, so it is the only moment the count
	// can exceed the limit.
	if n, err := s.deps.Playlist.PruneRevisions(ctx, s.deps.Config.RevisionRetention); err != nil {
		s.deps.Log.Warn("prune revisions", "error", err)
	} else if n > 0 {
		s.deps.Log.Info("pruned old playlist revisions", "count", n)
	}

	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "playlist_published",
		TargetType: "revision", TargetID: strconv.FormatInt(res.PublishedRevision, 10),
		Detail: fmt.Sprintf("%d scene(s)", res.SceneCount),
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	s.deps.Hub.NotifyPlaylist(ctx, res.PublishedRevision)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDiscardDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := userFrom(ctx)
	newDraft, err := s.deps.Playlist.DiscardDraft(ctx, u.Username)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "draft_discarded",
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusOK, map[string]any{"draft_revision": newDraft})
}

type reorderRequest struct {
	SceneIDs []int64 `json:"scene_ids"`
}

func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.deps.Playlist.Reorder(r.Context(), req.SceneIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type bulkRequest struct {
	SceneIDs   []int64 `json:"scene_ids"`
	Action     string  `json:"action"` // enable | disable | duration
	DurationMS int     `json:"duration_ms,omitempty"`
}

func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	var req bulkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	var err error
	switch req.Action {
	case "enable":
		err = s.deps.Playlist.BulkSetEnabled(ctx, req.SceneIDs, true, u.Username)
	case "disable":
		err = s.deps.Playlist.BulkSetEnabled(ctx, req.SceneIDs, false, u.Username)
	case "duration":
		err = s.deps.Playlist.BulkSetDuration(ctx, req.SceneIDs, req.DurationMS, u.Username)
	default:
		writeError(w, http.StatusBadRequest, "action must be enable, disable or duration")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRevisions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	revs, err := s.deps.Playlist.ListRevisions(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list revisions")
		return
	}
	if revs == nil {
		revs = []store.Revision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

type rollbackRequest struct {
	RevisionID int64 `json:"revision_id"`
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	var req rollbackRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	res, err := s.deps.Playlist.Rollback(ctx, req.RevisionID, u.Username)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "playlist_rollback",
		TargetType: "revision", TargetID: strconv.FormatInt(req.RevisionID, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	s.deps.Hub.NotifyPlaylist(ctx, res.PublishedRevision)
	writeJSON(w, http.StatusOK, res)
}

// ---- scenes --------------------------------------------------------------

func (s *Server) handleCreateScene(w http.ResponseWriter, r *http.Request) {
	var sc content.Scene
	if err := decodeJSON(w, r, &sc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid scene: "+err.Error())
		return
	}
	sc.ID = 0
	s.saveScene(w, r, &sc, http.StatusCreated)
}

func (s *Server) handleSceneByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/scenes/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid scene id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)
	action := actionFromPath(r.URL.Path, "/api/v1/scenes/")

	switch {
	case r.Method == http.MethodGet:
		sc, err := s.deps.Playlist.GetScene(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "scene not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"scene": sc})

	case r.Method == http.MethodPost && action == "duplicate":
		dup, err := s.deps.Playlist.DuplicateScene(ctx, id, u.Username)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"scene": dup})

	case r.Method == http.MethodPut:
		var sc content.Scene
		if err := decodeJSON(w, r, &sc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid scene: "+err.Error())
			return
		}
		sc.ID = id
		s.saveScene(w, r, &sc, http.StatusOK)

	case r.Method == http.MethodDelete:
		if err := s.deps.Playlist.DeleteScene(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "scene not found")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "scene_deleted",
			TargetType: "scene", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// saveScene persists a scene, reporting validation failures as structured field
// errors so the editor can highlight the offending inputs.
func (s *Server) saveScene(w http.ResponseWriter, r *http.Request, sc *content.Scene, okStatus int) {
	ctx := r.Context()
	u, _ := userFrom(ctx)

	err := s.deps.Playlist.SaveScene(ctx, sc, u.Username)

	var ve content.ValidationErrors
	if errors.As(err, &ve) {
		// The scene is saved but invalid: an operator must be able to save work in
		// progress. Publishing is what refuses invalid scenes.
		writeErrorDetail(w, http.StatusUnprocessableEntity,
			"the scene was saved but has validation errors", ve)
		return
	}
	if err != nil {
		s.deps.Log.Error("save scene", "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, okStatus, map[string]any{"scene": sc})
}

// ---- media ---------------------------------------------------------------

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		kind := r.URL.Query().Get("kind")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		items, err := s.deps.Media.List(ctx, kind, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list media")
			return
		}
		if items == nil {
			items = []media.Item{}
		}
		used, count, _ := s.deps.Media.Usage(ctx)
		writeJSON(w, http.StatusOK, map[string]any{
			"media": items, "total_bytes": used, "total_count": count,
		})
		return
	}

	// Upload. Editors and above only.
	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	// The multipart reader is bounded by the largest configured cap plus slack
	// for the form envelope, so a client cannot stream an unbounded body.
	maxUpload := s.deps.Config.MaxVideoBytes
	if s.deps.Config.MaxPDFBytes > maxUpload {
		maxUpload = s.deps.Config.MaxPDFBytes
	}
	if s.deps.Config.MaxImageBytes > maxUpload {
		maxUpload = s.deps.Config.MaxImageBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}

	var saved []media.Item
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		// Streamed straight to the store: the file is never buffered in memory,
		// which matters on a 4 GB device receiving a 500 MB video.
		item, err := s.deps.Media.Save(ctx, part, part.FileName(), u.Username)
		part.Close()
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, media.ErrTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, status, err.Error())
			return
		}
		saved = append(saved, item)
	}

	if len(saved) == 0 {
		writeError(w, http.StatusBadRequest, "no file was uploaded")
		return
	}
	for _, it := range saved {
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "media_uploaded",
			TargetType: "media", TargetID: strconv.FormatInt(it.ID, 10),
			Detail: it.MIME + " " + strconv.FormatInt(it.Bytes, 10) + "B",
			IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
	}
	s.deps.Hub.NotifyMedia("uploaded")
	writeJSON(w, http.StatusCreated, map[string]any{"media": saved})
}

type patchMediaRequest struct {
	Name     *string `json:"name,omitempty"`
	PosterID *int64  `json:"poster_id,omitempty"`
}

func (s *Server) handleMediaByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/media/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid media id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	switch r.Method {
	case http.MethodGet:
		it, err := s.deps.Media.Get(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		pages, _ := s.deps.Media.Pages(ctx, id)
		writeJSON(w, http.StatusOK, map[string]any{"media": it, "pages": pages})

	case http.MethodPatch:
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		var req patchMediaRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name != nil {
			if err := s.deps.Media.Rename(ctx, id, *req.Name); err != nil {
				writeError(w, http.StatusNotFound, "media not found")
				return
			}
		}
		if req.PosterID != nil {
			if err := s.deps.Media.SetPoster(ctx, id, *req.PosterID); err != nil {
				writeError(w, http.StatusBadRequest, "could not set the poster image")
				return
			}
		}
		it, _ := s.deps.Media.Get(ctx, id)
		writeJSON(w, http.StatusOK, map[string]any{"media": it})

	case http.MethodDelete:
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		// Deleting media a scene still uses would leave the player rendering a
		// fallback with no obvious cause, so it is refused with an explanation
		// rather than allowed silently.
		inUse, err := s.deps.Playlist.IsMediaReferenced(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check media usage")
			return
		}
		if inUse && r.URL.Query().Get("force") != "true" {
			writeError(w, http.StatusConflict,
				"this file is still used by one or more scenes; remove it from them first, "+
					"or repeat the request with force=true")
			return
		}
		if err := s.deps.Media.Delete(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "media_deleted",
			TargetType: "media", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		s.deps.Hub.NotifyMedia("deleted")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func roleAtLeastEditor(role string) bool {
	return role == "editor" || role == "admin"
}

// handleMediaFile serves an original upload by database ID.
//
// The path never contains a filename: the ID is looked up and the stored name
// comes from the database, so there is no user-controlled component in the path
// at any point.
func (s *Server) handleMediaFile(w http.ResponseWriter, r *http.Request) {
	s.serveMediaBlob(w, r, "/media/file/", false)
}

func (s *Server) handleMediaThumb(w http.ResponseWriter, r *http.Request) {
	s.serveMediaBlob(w, r, "/media/thumb/", true)
}

func (s *Server) serveMediaBlob(w http.ResponseWriter, r *http.Request, prefix string, thumb bool) {
	id, ok := idFromPath(r.URL.Path, prefix)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid media id")
		return
	}
	it, err := s.deps.Media.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}

	var path string
	var mime string
	if thumb {
		if it.ThumbName == "" {
			writeError(w, http.StatusNotFound, "no thumbnail for this item")
			return
		}
		path, err = s.deps.Media.ThumbPath(it.ThumbName)
		mime = "image/jpeg"
	} else {
		path, err = s.deps.Media.Path(it.StoredName)
		mime = it.MIME
	}
	if err != nil {
		s.deps.Log.Error("media path rejected", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the file")
		return
	}

	f, err := os.Open(path) //nolint:gosec // path is derived from a database row and validated by safeJoin
	if err != nil {
		writeError(w, http.StatusNotFound, "the file is missing from storage")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the file")
		return
	}

	h := w.Header()
	h.Set("Content-Type", mime)
	// nosniff plus an explicit type: even though uploads are signature-validated,
	// this stops a browser from ever reinterpreting a stored file as HTML.
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", "inline; filename=\""+filepath.Base(path)+"\"")
	// Stored files are immutable: the name is content-addressed by a random ID
	// and a replacement gets a new row, so a long cache is safe and saves the
	// Jetson repeated reads during playback.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")

	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// ---- schedule ------------------------------------------------------------

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rules, err := s.deps.Schedule.Rules(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the schedule")
		return
	}
	overrides, _ := s.deps.Schedule.ActiveOverrides(ctx)
	now := time.Now()
	d := s.deps.Engine.Decide(now, rules, overrides)

	if rules == nil {
		rules = []schedule.Rule{}
	}
	if overrides == nil {
		overrides = []schedule.Override{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rules":       rules,
		"overrides":   overrides,
		"decision":    d,
		"timezone":    s.deps.Config.Timezone,
		"server_time": now.In(s.deps.Engine.Location()).Format(time.RFC3339),
		"next_change": s.deps.Engine.NextChange(now, rules, overrides),
	})
}

func (s *Server) handleUpsertRule(w http.ResponseWriter, r *http.Request) {
	var rule schedule.Rule
	if err := decodeJSON(w, r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule: "+err.Error())
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if _, err := s.deps.Schedule.UpsertRule(ctx, rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "schedule_updated",
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	s.notifyScheduleChanged(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/schedule/rules/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if err := s.deps.Schedule.DeleteRule(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	s.notifyScheduleChanged(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type overrideRequest struct {
	Mode            string `json:"mode"` // wake | sleep
	DurationMinutes int    `json:"duration_minutes"`
}

func (s *Server) handleOverride(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if r.Method == http.MethodDelete {
		if err := s.deps.Schedule.ClearOverrides(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "could not clear the override")
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "override_cleared",
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		s.notifyScheduleChanged(r)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	var req overrideRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	o, err := s.deps.Schedule.CreateOverride(ctx, req.Mode,
		time.Duration(req.DurationMinutes)*time.Minute, u.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "override_created",
		Detail: req.Mode + " for " + strconv.Itoa(req.DurationMinutes) + " minutes",
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	s.notifyScheduleChanged(r)
	writeJSON(w, http.StatusOK, map[string]any{"override": o})
}

// notifyScheduleChanged recomputes the decision and broadcasts it so the player
// and dashboards reflect the change without waiting for the next tick.
func (s *Server) notifyScheduleChanged(r *http.Request) {
	ctx := r.Context()
	rules, err := s.deps.Schedule.Rules(ctx)
	if err != nil {
		return
	}
	overrides, _ := s.deps.Schedule.ActiveOverrides(ctx)
	d := s.deps.Engine.Decide(time.Now(), rules, overrides)
	s.deps.Hub.NotifySchedule(d.On, d.Reason)
}

// ---- emergency -----------------------------------------------------------

func (s *Server) handleListEmergency(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.deps.Emergency.List(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list messages")
		return
	}
	if msgs == nil {
		msgs = []store.Emergency{}
	}
	active, has, _ := s.deps.Emergency.Active(r.Context())
	resp := map[string]any{"messages": msgs, "active": nil}
	if has {
		resp["active"] = active
	}
	writeJSON(w, http.StatusOK, resp)
}

type raiseEmergencyRequest struct {
	Heading         string `json:"heading"`
	Body            string `json:"body"`
	Severity        string `json:"severity"`
	DurationMinutes int    `json:"duration_minutes"`
}

func (s *Server) handleRaiseEmergency(w http.ResponseWriter, r *http.Request) {
	var req raiseEmergencyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 60
	}
	now := time.Now().UTC()
	e, err := s.deps.Emergency.Create(ctx, store.Emergency{
		Heading: req.Heading, Body: req.Body, Severity: req.Severity,
		StartsAt: now, ExpiresAt: now.Add(time.Duration(req.DurationMinutes) * time.Minute),
		CreatedBy: u.Username,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "emergency_raised",
		TargetType: "emergency", TargetID: strconv.FormatInt(e.ID, 10),
		Detail: e.Severity + ": " + e.Heading,
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	s.deps.Hub.NotifyEmergency(true, e)
	writeJSON(w, http.StatusCreated, map[string]any{"emergency": e})
}

func (s *Server) handleDismissEmergency(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/emergency/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)

	if err := s.deps.Emergency.Dismiss(ctx, id); err != nil {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "emergency_dismissed",
		TargetType: "emergency", TargetID: strconv.FormatInt(id, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	// Re-check: another message may still be in force.
	active, has, _ := s.deps.Emergency.Active(ctx)
	if has {
		s.deps.Hub.NotifyEmergency(true, active)
	} else {
		s.deps.Hub.NotifyEmergency(false, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- overview, audit, settings -------------------------------------------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	draft, _ := s.deps.Playlist.DraftRevision(ctx)
	live, liveErr := s.deps.Playlist.PublishedRevision(ctx)
	rules, _ := s.deps.Schedule.Rules(ctx)
	overrides, _ := s.deps.Schedule.ActiveOverrides(ctx)
	now := time.Now()
	decision := s.deps.Engine.Decide(now, rules, overrides)

	var liveScenes []content.Scene
	if liveErr == nil {
		liveScenes, _ = s.deps.Playlist.ListScenes(ctx, live.ID)
	}

	st := s.deps.Player.Get()
	usedBytes, mediaCount, _ := s.deps.Media.Usage(ctx)

	var freeBytes int64
	if s.deps.StorageUsage != nil {
		_, freeBytes, _ = s.deps.StorageUsage()
	}

	activeEmergency, hasEmergency, _ := s.deps.Emergency.Active(ctx)
	recentErrors, _ := s.deps.Audit.List(ctx, 10, "")

	// Derive the currently-playing and next scene from the live playlist and the
	// player's report, so the dashboard shows something sensible even if the
	// player has not reported yet.
	var current, next string
	for i, sc := range liveScenes {
		if sc.StableID == st.CurrentScene {
			current = sc.Name
			for j := 1; j <= len(liveScenes); j++ {
				cand := liveScenes[(i+j)%len(liveScenes)]
				if cand.Enabled && cand.Valid {
					next = cand.Name
					break
				}
			}
			break
		}
	}
	if current == "" && len(liveScenes) > 0 {
		for _, sc := range liveScenes {
			if sc.Enabled && sc.Valid {
				next = sc.Name
				break
			}
		}
	}

	var unpublished bool
	if liveErr == nil {
		draftScenes, _ := s.deps.Playlist.ListScenes(ctx, draft.ID)
		unpublished = len(draftScenes) != len(liveScenes)
		if !unpublished {
			for i := range draftScenes {
				if i < len(liveScenes) && draftScenes[i].UpdatedAt.After(liveScenes[i].UpdatedAt) {
					unpublished = true
					break
				}
			}
		}
	} else {
		unpublished = true
	}

	players, admins := s.deps.Hub.CountByRole()

	resp := map[string]any{
		"player": map[string]any{
			"online":         st.Online,
			"last_heartbeat": st.LastHeartbeat,
			"current_scene":  current,
			"next_scene":     next,
			"current_slide":  st.CurrentSlide,
			"browser":        st.BrowserUA,
			"last_error":     st.LastError,
			"revision":       st.RevisionID,
		},
		"display": map[string]any{
			"on":          decision.On,
			"reason":      decision.Reason,
			"source":      decision.Source,
			"driver":      s.deps.Display.Name(),
			"next_change": s.deps.Engine.NextChange(now, rules, overrides),
		},
		"playlist": map[string]any{
			"draft_revision":     draft.ID,
			"published_revision": liveRevisionID(live, liveErr),
			"scene_count":        len(liveScenes),
			"has_unpublished":    unpublished,
		},
		"storage": map[string]any{
			"media_bytes": usedBytes,
			"media_count": mediaCount,
			"free_bytes":  freeBytes,
			"low":         freeBytes > 0 && freeBytes < s.deps.Config.LowDiskWarnBytes,
		},
		"subscribers":   map[string]any{"players": players, "admins": admins},
		"server_time":   now.In(s.deps.Engine.Location()).Format(time.RFC3339),
		"timezone":      s.deps.Config.Timezone,
		"version":       s.deps.Config.Timezone, // placeholder replaced below
		"recent_events": recentErrors,
	}
	delete(resp, "version")
	if hasEmergency {
		resp["emergency"] = activeEmergency
	}
	writeJSON(w, http.StatusOK, resp)
}

func liveRevisionID(r store.Revision, err error) any {
	if err != nil {
		return nil
	}
	return r.ID
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.deps.Audit.List(r.Context(), limit, r.URL.Query().Get("action"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the audit log")
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type settingsRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// allowedSettings restricts what the settings endpoint may write.
//
// An open key-value endpoint would let an editor set arbitrary application
// state, so writes are limited to this list.
var allowedSettings = map[string]bool{
	"player.default_transition": true,
	"player.show_clock_overlay": true,
	"player.offline_message":    true,
	"branding.display_name":     true,
	"social.default_moderation": true,
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		all, err := s.deps.Settings.All(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not read settings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"settings": all,
			"config": map[string]any{
				"timezone":         s.deps.Config.Timezone,
				"max_image_bytes":  s.deps.Config.MaxImageBytes,
				"max_video_bytes":  s.deps.Config.MaxVideoBytes,
				"max_pdf_bytes":    s.deps.Config.MaxPDFBytes,
				"max_pdf_pages":    s.deps.Config.MaxPDFPages,
				"browser_enabled":  s.deps.Config.BrowserEnabled,
				"dpms_driver":      s.deps.Config.DPMSDriver,
				"backup_retention": s.deps.Config.BackupRetention,
			},
		})
		return
	}

	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var req settingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !allowedSettings[req.Key] {
		writeError(w, http.StatusBadRequest, "unknown setting "+strconv.Quote(req.Key))
		return
	}
	if err := s.deps.Settings.Set(ctx, req.Key, req.Value, u.Username); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- realtime ------------------------------------------------------------

func (s *Server) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	s.deps.Hub.ServeHTTP(w, r, realtime.RoleAdmin)
}

func (s *Server) handlePlayerEvents(w http.ResponseWriter, r *http.Request) {
	s.deps.Hub.ServeHTTP(w, r, realtime.RolePlayer)
}

// ---- player --------------------------------------------------------------

// handlePlayerState returns everything the player needs to render, and nothing
// else: no user records, no credentials, no tokens.
func (s *Server) handlePlayerState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	live, err := s.deps.Playlist.PublishedRevision(ctx)
	scenes := []content.Scene{}
	var revID int64
	if err == nil {
		revID = live.ID
		if got, lerr := s.deps.Playlist.ListScenes(ctx, live.ID); lerr == nil {
			for _, sc := range got {
				if sc.Enabled && sc.Valid {
					scenes = append(scenes, sc)
				}
			}
		}
	}

	rules, _ := s.deps.Schedule.Rules(ctx)
	overrides, _ := s.deps.Schedule.ActiveOverrides(ctx)
	now := time.Now()
	decision := s.deps.Engine.Decide(now, rules, overrides)

	resp := map[string]any{
		"revision":    revID,
		"scenes":      scenes,
		"display_on":  decision.On,
		"server_time": now.UTC().Format(time.RFC3339),
		"timezone":    s.deps.Config.Timezone,
	}
	if e, has, _ := s.deps.Emergency.Active(ctx); has {
		resp["emergency"] = map[string]any{
			"heading":    e.Heading,
			"body":       e.Body,
			"severity":   e.Severity,
			"expires_at": e.ExpiresAt,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type heartbeatRequest struct {
	RevisionID   int64          `json:"revision_id"`
	CurrentScene string         `json:"current_scene"`
	CurrentSlide string         `json:"current_slide"`
	ZoneStates   map[string]any `json:"zone_states,omitempty"`
	StorageState string         `json:"storage_state,omitempty"`
	LastError    string         `json:"last_error,omitempty"`
	Offline      bool           `json:"offline,omitempty"`
	PlayedScene  string         `json:"played_scene,omitempty"`
	PlayError    string         `json:"play_error,omitempty"`
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid heartbeat")
		return
	}
	ctx := r.Context()

	st := store.Status{
		RevisionID: req.RevisionID, CurrentScene: req.CurrentScene,
		CurrentSlide: req.CurrentSlide, BrowserUA: truncateStr(r.UserAgent(), 255),
		ZoneStates: req.ZoneStates, StorageState: req.StorageState,
		LastError: truncateStr(req.LastError, 500), Offline: req.Offline,
	}
	changed, err := s.deps.Player.Heartbeat(ctx, st)
	if err != nil {
		s.deps.Log.Error("heartbeat", "error", err)
	}
	if req.PlayedScene != "" {
		_ = s.deps.Playlist.MarkPlayed(ctx, req.PlayedScene, truncateStr(req.PlayError, 500))
	}
	if changed {
		s.deps.Hub.NotifyPlayer(s.deps.Player.Get())
	}

	// Echo the current revision so the player can detect a missed publish even if
	// it lost the event stream.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"server_revision": s.deps.Hub.Revision(),
		"server_time":     time.Now().UTC().Format(time.RFC3339),
	})
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = strings.TrimSpace
