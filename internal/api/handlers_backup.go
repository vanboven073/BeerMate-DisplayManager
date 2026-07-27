package api

import (
	"net/http"
	"os"
	"strconv"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

// handleLegacyImport reconstructs the old Bash-kiosk content as a draft playlist.
//
// It is admin-only and non-destructive: it never touches the legacy files, and
// it publishes nothing. The operator reviews the imported draft and publishes it.
func (s *Server) handleLegacyImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := userFrom(ctx)

	var req struct {
		RoadmapImage string `json:"roadmap_image"`
		DashboardURL string `json:"dashboard_url"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	paths := store.DefaultLegacyPaths()
	if req.RoadmapImage != "" {
		paths.RoadmapImage = req.RoadmapImage
	}
	if req.DashboardURL != "" {
		paths.DashboardURL = req.DashboardURL
	}

	// Take a pre-change backup first, so an unwanted import can be undone.
	if _, err := s.deps.Backups.Create(ctx, "pre_change", "before legacy import", u.Username); err != nil {
		s.deps.Log.Warn("pre-import backup failed", "error", err)
	}

	imp := store.NewLegacyImporter(s.deps.Playlist, s.deps.Media, s.deps.Websites)
	result, err := imp.Import(ctx, paths, u.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "legacy_imported",
		Detail: strconv.Itoa(len(result.Imported)) + " item(s)",
		IP:     clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodGet {
		backups, err := s.deps.Backups.List(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list backups")
			return
		}
		if backups == nil {
			backups = []store.Backup{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
		return
	}

	// Create. Editors and above.
	u, _ := userFrom(ctx)
	if !roleAtLeastEditor(u.Role) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	b, err := s.deps.Backups.Create(ctx, "manual", req.Note, u.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the backup")
		return
	}
	_ = s.deps.Audit.Record(ctx, store.AuditEntry{
		ActorName: u.Username, Action: "backup_created",
		TargetType: "backup", TargetID: strconv.FormatInt(b.ID, 10),
		IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"backup": b})
}

func (s *Server) handleBackupByID(w http.ResponseWriter, r *http.Request) {
	id, ok := idFromPath(r.URL.Path, "/api/v1/backups/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup id")
		return
	}
	ctx := r.Context()
	u, _ := userFrom(ctx)
	action := actionFromPath(r.URL.Path, "/api/v1/backups/")

	switch {
	case r.Method == http.MethodGet && action == "download":
		b, err := s.deps.Backups.Get(ctx, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		path, err := s.deps.Backups.Path(b.Filename)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not resolve the backup")
			return
		}
		f, err := os.Open(path) //nolint:gosec // path built from a validated DB filename
		if err != nil {
			writeError(w, http.StatusNotFound, "the backup file is missing")
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+b.Filename+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, b.Filename, info.ModTime(), f)

	case r.Method == http.MethodGet && action == "verify":
		manifest, err := s.deps.Backups.Verify(ctx, id)
		if err != nil {
			writeErrorDetail(w, http.StatusUnprocessableEntity, err.Error(), manifest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "manifest": manifest})

	case r.Method == http.MethodPost && action == "restore":
		if u.Role != "admin" {
			writeError(w, http.StatusForbidden, "only an administrator may restore a backup")
			return
		}
		if err := s.deps.Backups.Restore(ctx, id, u.Username); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = s.deps.Audit.Record(ctx, store.AuditEntry{
			ActorName: u.Username, Action: "backup_restored",
			TargetType: "backup", TargetID: strconv.FormatInt(id, 10),
			IP: clientIP(r, s.deps.Config.TrustProxyHeaders),
		})
		// The running process still holds the old database handle, so the service
		// must restart to pick up the restored file. systemd does this on exit.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"message": "Backup restored. The service will restart to load the restored data. " +
				"Reconnect in a few seconds.",
			"restart_required": true,
		})

	case r.Method == http.MethodDelete && action == "":
		if !roleAtLeastEditor(u.Role) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		if err := s.deps.Backups.Delete(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
