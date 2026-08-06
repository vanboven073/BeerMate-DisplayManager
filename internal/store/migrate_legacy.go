package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/content"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/media"
)

// LegacyPaths locates the files the old Bash kiosk used on the Jetson.
type LegacyPaths struct {
	RoadmapImage string // /home/beermate/Pictures/Roadmap_2026.png
	DashboardURL string // BeerMate lifetime dashboard
	CountdownDoc string // countdown.html (for reference only)
	KioskScript  string // beermate-kiosk.sh (for reference only)
}

// DefaultLegacyPaths returns the known locations from the existing installation.
func DefaultLegacyPaths() LegacyPaths {
	return LegacyPaths{
		RoadmapImage: "/home/beermate/Pictures/Roadmap_2026.png",
		DashboardURL: "https://dashboard.beermatecloud.eu/dashboard/44-beermate-lifetime-dashboard",
		CountdownDoc: "/home/beermate/beermate-kiosk/countdown.html",
		KioskScript:  "/home/beermate/bin/beermate-kiosk.sh",
	}
}

// LegacyImportResult reports what the import did.
type LegacyImportResult struct {
	Imported []string `json:"imported"`
	Warnings []string `json:"warnings"`
	SceneIDs []int64  `json:"scene_ids"`
}

// LegacyImporter reconstructs the old kiosk's content as a draft playlist.
//
// It is deliberately additive and non-destructive: it creates scenes in the
// draft revision and leaves every legacy file untouched, so the old kiosk can be
// restored by rollback-jetson.sh if anything is wrong. Nothing here is published;
// an operator reviews the imported draft and publishes it themselves.
type LegacyImporter struct {
	playlist *PlaylistStore
	media    *media.Store
	websites *WebsiteStore
	now      func() time.Time
}

// NewLegacyImporter builds a LegacyImporter.
func NewLegacyImporter(pl *PlaylistStore, m *media.Store, ws *WebsiteStore) *LegacyImporter {
	return &LegacyImporter{playlist: pl, media: m, websites: ws, now: time.Now}
}

// Import reconstructs the legacy content: the roadmap image, the dashboard
// website, and the 14 August 2026 countdown, each at the legacy 60-second
// duration.
func (imp *LegacyImporter) Import(ctx context.Context, paths LegacyPaths, actor string) (LegacyImportResult, error) {
	var res LegacyImportResult

	// 1. Roadmap image.
	if paths.RoadmapImage != "" {
		if f, err := os.Open(paths.RoadmapImage); err == nil {
			item, saveErr := imp.media.Save(ctx, f, filepath.Base(paths.RoadmapImage), actor)
			f.Close()
			if saveErr != nil {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("roadmap image found but could not be imported: %v", saveErr))
			} else {
				sc := &content.Scene{
					Name: "Roadmap 2026", Enabled: true, Layout: content.LayoutFullscreen,
					DurationMS: 60000, DaysMask: content.AllDaysMask, Transition: "fade",
					Zones: []content.Zone{{
						Slot: "a", ContentType: content.TypeImage,
						ContentRef: fmt.Sprint(item.ID), Config: `{"fit":"contain"}`,
					}},
				}
				if err := imp.playlist.SaveScene(ctx, sc, actor); err == nil {
					res.Imported = append(res.Imported, "Roadmap 2026 image")
					res.SceneIDs = append(res.SceneIDs, sc.ID)
				}
			}
		} else {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("roadmap image not found at %s; skipping", paths.RoadmapImage))
		}
	}

	// 2. BeerMate lifetime dashboard website.
	if paths.DashboardURL != "" {
		site, err := imp.websites.Create(ctx, Website{
			Name: "BeerMate lifetime dashboard", URL: paths.DashboardURL,
			RenderMode: RenderIframe, CreatedBy: actor,
		})
		if err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("could not import the dashboard website: %v", err))
		} else {
			sc := &content.Scene{
				Name: "Lifetime dashboard", Enabled: true, Layout: content.LayoutFullscreen,
				DurationMS: 60000, DaysMask: content.AllDaysMask, Transition: "fade",
				Zones: []content.Zone{{
					Slot: "a", ContentType: content.TypeWebsite,
					ContentRef: fmt.Sprint(site.ID), Config: `{"refresh_on_show":true}`,
				}},
			}
			if err := imp.playlist.SaveScene(ctx, sc, actor); err == nil {
				res.Imported = append(res.Imported, "BeerMate lifetime dashboard")
				res.SceneIDs = append(res.SceneIDs, sc.ID)
			}
		}
	}

	// 3. Countdown to 14 August 2026 00:00 Europe/Amsterdam.
	countdownCfg := `{"title":"BeerMate","subtitle":"Counting down to the big day",` +
		`"target":"2026-08-14T00:00:00+02:00","timezone":"Europe/Amsterdam",` +
		`"completion_message":"The day is here!","hide_zero_units":false}`
	sc := &content.Scene{
		Name: "Countdown to 14 August 2026", Enabled: true, Layout: content.LayoutFullscreen,
		DurationMS: 60000, DaysMask: content.AllDaysMask, Transition: "fade",
		Zones: []content.Zone{{
			Slot: "a", ContentType: content.TypeCountdown, Config: countdownCfg,
		}},
	}
	if err := imp.playlist.SaveScene(ctx, sc, actor); err == nil {
		res.Imported = append(res.Imported, "Countdown to 14 August 2026")
		res.SceneIDs = append(res.SceneIDs, sc.ID)
	} else {
		res.Warnings = append(res.Warnings, fmt.Sprintf("could not create the countdown scene: %v", err))
	}

	if len(res.Imported) == 0 {
		res.Warnings = append(res.Warnings,
			"nothing was imported; check the legacy paths and file permissions")
	}
	return res, nil
}
