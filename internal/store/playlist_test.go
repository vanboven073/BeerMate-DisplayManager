package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/content"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
)

func newStore(t *testing.T) (*PlaylistStore, *dbx.DB) {
	t.Helper()
	db, err := dbx.Open(dbx.Options{
		Path:   filepath.Join(t.TempDir(), "pl.db"),
		Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewPlaylistStore(db), db
}

// seedMedia inserts a media row so scenes referencing it validate.
func seedMedia(t *testing.T, db *dbx.DB, id int64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO media (id, kind, stored_name, mime, bytes, sha256, created_at, updated_at)
		VALUES (?, 'image', ?, 'image/png', 1, 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		id, "stored-"+string(rune('a'+id))+".png")
	if err != nil {
		t.Fatal(err)
	}
}

func imageScene(name string, mediaID string) *content.Scene {
	return &content.Scene{
		Name: name, Enabled: true, Layout: content.LayoutFullscreen,
		DurationMS: 60000, DaysMask: content.AllDaysMask, Transition: "fade",
		Zones: []content.Zone{{
			Slot: "a", ContentType: content.TypeImage, ContentRef: mediaID,
			Config: `{"fit":"contain"}`,
		}},
	}
}

func TestDraftRevisionExistsAfterMigration(t *testing.T) {
	ps, _ := newStore(t)
	ctx := context.Background()
	d, err := ps.DraftRevision(ctx)
	if err != nil {
		t.Fatalf("draft revision: %v", err)
	}
	if !d.IsDraft {
		t.Error("returned revision is not the draft")
	}
}

func TestSaveAndListScenes(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Roadmap", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if sc.ID == 0 {
		t.Fatal("scene ID not populated after save")
	}
	if sc.StableID == "" {
		t.Fatal("stable ID not generated")
	}

	draft, _ := ps.DraftRevision(ctx)
	scenes, err := ps.ListScenes(ctx, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if len(scenes[0].Zones) != 1 {
		t.Fatalf("got %d zones, want 1", len(scenes[0].Zones))
	}
	if scenes[0].Zones[0].ContentRef != "1" {
		t.Errorf("zone content_ref = %q, want 1", scenes[0].Zones[0].ContentRef)
	}
	if !scenes[0].Valid {
		t.Errorf("scene marked invalid: %s", scenes[0].ValidationMsg)
	}
}

// A scene referencing media that does not exist must be stored as invalid rather
// than silently accepted; otherwise the player shows a fallback with no
// indication in the admin UI of why.
func TestSaveSceneWithMissingMediaIsInvalid(t *testing.T) {
	ps, _ := newStore(t)
	ctx := context.Background()

	sc := imageScene("Ghost", "999")
	_ = ps.SaveScene(ctx, sc, "bram")

	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].Valid {
		t.Error("scene referencing missing media was marked valid")
	}
	if !strings.Contains(scenes[0].ValidationMsg, "no longer exists") {
		t.Errorf("validation message = %q, want a mention of the missing reference",
			scenes[0].ValidationMsg)
	}
}

func TestSaveSceneUpdateReplacesZones(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)
	seedMedia(t, db, 2)

	sc := imageScene("A", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}

	// Switch to a two-column layout with different content.
	sc.Layout = content.LayoutCols5050
	sc.Zones = []content.Zone{
		{Slot: "a", ContentType: content.TypeImage, ContentRef: "1", Config: `{"fit":"cover"}`},
		{Slot: "b", ContentType: content.TypeImage, ContentRef: "2", Config: `{"fit":"cover"}`},
	}
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := ps.GetScene(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Zones) != 2 {
		t.Fatalf("got %d zones after layout change, want 2 (stale zones not cleared?)", len(got.Zones))
	}
}

func TestReorderScenes(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	var ids []int64
	for _, n := range []string{"one", "two", "three"} {
		sc := imageScene(n, "1")
		if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sc.ID)
	}

	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := ps.Reorder(ctx, reversed); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	for i, want := range reversed {
		if scenes[i].ID != want {
			t.Errorf("position %d = scene %d, want %d", i, scenes[i].ID, want)
		}
	}
}

// A partial reorder would leave positions inconsistent, so the whole list must
// be supplied.
func TestReorderRequiresCompleteList(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	var ids []int64
	for _, n := range []string{"one", "two"} {
		sc := imageScene(n, "1")
		if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sc.ID)
	}
	if err := ps.Reorder(ctx, ids[:1]); err == nil {
		t.Error("partial reorder was accepted")
	}
}

func TestDuplicateScene(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Original", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	dup, err := ps.DuplicateScene(ctx, sc.ID, "bram")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if dup.ID == sc.ID {
		t.Error("duplicate shares the original's row ID")
	}
	if dup.StableID == sc.StableID {
		t.Error("duplicate shares the original's stable ID")
	}
	if !strings.Contains(dup.Name, "copy") {
		t.Errorf("duplicate name = %q, expected it to be marked as a copy", dup.Name)
	}

	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	if len(scenes) != 2 {
		t.Errorf("got %d scenes after duplicate, want 2", len(scenes))
	}
}

// ---- publishing ----------------------------------------------------------

func TestPublishMakesDraftLiveAndOpensNewDraft(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Roadmap", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	before, _ := ps.DraftRevision(ctx)

	res, err := ps.Publish(ctx, "bram", "first publish")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.PublishedRevision != before.ID {
		t.Errorf("published revision = %d, want the old draft %d", res.PublishedRevision, before.ID)
	}
	if res.NewDraftRevision == before.ID {
		t.Error("a new draft revision was not created")
	}
	if res.SceneCount != 1 {
		t.Errorf("scene count = %d, want 1", res.SceneCount)
	}

	live, err := ps.PublishedRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if live.ID != res.PublishedRevision {
		t.Errorf("live revision = %d, want %d", live.ID, res.PublishedRevision)
	}
	if live.IsDraft {
		t.Error("the live revision is still flagged as a draft")
	}

	// The new draft must be a full copy, so editing continues from the live state.
	newDraft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, newDraft.ID)
	if len(scenes) != 1 {
		t.Fatalf("new draft has %d scenes, want 1 cloned from the published revision", len(scenes))
	}
	if len(scenes[0].Zones) != 1 {
		t.Error("zones were not cloned into the new draft")
	}
	if scenes[0].StableID != sc.StableID {
		t.Error("stable ID did not survive the publish clone; history would break")
	}
}

// Editing the draft after publishing must not change what is on screen. This is
// the whole point of the revision model.
func TestEditingDraftDoesNotAffectLiveRevision(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Live", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	res, err := ps.Publish(ctx, "bram", "")
	if err != nil {
		t.Fatal(err)
	}

	// Now edit the fresh draft heavily.
	draft, _ := ps.DraftRevision(ctx)
	draftScenes, _ := ps.ListScenes(ctx, draft.ID)
	draftScenes[0].Name = "Edited in draft"
	if err := ps.SaveScene(ctx, &draftScenes[0], "bram"); err != nil {
		t.Fatal(err)
	}
	extra := imageScene("Added in draft", "1")
	if err := ps.SaveScene(ctx, extra, "bram"); err != nil {
		t.Fatal(err)
	}

	liveScenes, err := ps.ListScenes(ctx, res.PublishedRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveScenes) != 1 {
		t.Errorf("live revision now has %d scenes; draft edits leaked into it", len(liveScenes))
	}
	if liveScenes[0].Name != "Live" {
		t.Errorf("live scene name = %q, want %q; draft edits leaked into it",
			liveScenes[0].Name, "Live")
	}
}

func TestPublishRefusesInvalidScenes(t *testing.T) {
	ps, _ := newStore(t)
	ctx := context.Background()

	sc := imageScene("Broken", "999") // media does not exist
	_ = ps.SaveScene(ctx, sc, "bram")

	if _, err := ps.Publish(ctx, "bram", ""); err == nil {
		t.Error("publishing a playlist with an invalid enabled scene was allowed")
	}
}

// Publishing an empty playlist would black out the display.
func TestPublishRefusesEmptyPlaylist(t *testing.T) {
	ps, _ := newStore(t)
	ctx := context.Background()
	if _, err := ps.Publish(ctx, "bram", ""); err == nil {
		t.Error("publishing with no enabled scenes was allowed; the display would be blank")
	}
}

func TestPublishSkipsDisabledInvalidScenes(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	good := imageScene("Good", "1")
	if err := ps.SaveScene(ctx, good, "bram"); err != nil {
		t.Fatal(err)
	}
	// A disabled broken scene should not block publishing.
	bad := imageScene("Broken", "999")
	bad.Enabled = false
	_ = ps.SaveScene(ctx, bad, "bram")

	if _, err := ps.Publish(ctx, "bram", ""); err != nil {
		t.Errorf("publish blocked by a disabled invalid scene: %v", err)
	}
}

func TestRollback(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	first := imageScene("Version one", "1")
	if err := ps.SaveScene(ctx, first, "bram"); err != nil {
		t.Fatal(err)
	}
	v1, err := ps.Publish(ctx, "bram", "v1")
	if err != nil {
		t.Fatal(err)
	}

	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	scenes[0].Name = "Version two"
	if err := ps.SaveScene(ctx, &scenes[0], "bram"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Publish(ctx, "bram", "v2"); err != nil {
		t.Fatal(err)
	}

	live, _ := ps.PublishedRevision(ctx)
	liveScenes, _ := ps.ListScenes(ctx, live.ID)
	if liveScenes[0].Name != "Version two" {
		t.Fatalf("expected v2 live, got %q", liveScenes[0].Name)
	}

	// Roll back to v1.
	if _, err := ps.Rollback(ctx, v1.PublishedRevision, "bram"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	live, _ = ps.PublishedRevision(ctx)
	liveScenes, _ = ps.ListScenes(ctx, live.ID)
	if liveScenes[0].Name != "Version one" {
		t.Errorf("after rollback the live scene is %q, want %q", liveScenes[0].Name, "Version one")
	}

	// Rollback is itself a new revision, so v2 is still in history and the
	// operator can undo the undo.
	revs, _ := ps.ListRevisions(ctx, 50)
	if len(revs) < 4 {
		t.Errorf("revision history has %d entries; rollback should append, not delete", len(revs))
	}
}

func TestRollbackRejectsUnpublishedTarget(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)
	sc := imageScene("x", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	draft, _ := ps.DraftRevision(ctx)
	if _, err := ps.Rollback(ctx, draft.ID, "bram"); err == nil {
		t.Error("rolling back to an unpublished draft was allowed")
	}
}

func TestDiscardDraft(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Published", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Publish(ctx, "bram", ""); err != nil {
		t.Fatal(err)
	}

	extra := imageScene("Unpublished experiment", "1")
	if err := ps.SaveScene(ctx, extra, "bram"); err != nil {
		t.Fatal(err)
	}

	if _, err := ps.DiscardDraft(ctx, "bram"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	if len(scenes) != 1 {
		t.Errorf("draft has %d scenes after discard, want 1 matching the live revision", len(scenes))
	}
	if scenes[0].Name != "Published" {
		t.Errorf("draft scene = %q, want the live scene restored", scenes[0].Name)
	}
}

// ---- retention -----------------------------------------------------------

// Without pruning, an appliance running for years accumulates a full scene/zone
// copy per publish and media can never be reclaimed.
func TestPruneRevisionsKeepsLiveAndRecent(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Scene", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := ps.Publish(ctx, "bram", ""); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	before, _ := ps.ListRevisions(ctx, 200)
	liveBefore, _ := ps.PublishedRevision(ctx)

	n, err := ps.PruneRevisions(ctx, 3)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n == 0 {
		t.Fatal("prune removed nothing despite 10 published revisions and a limit of 3")
	}

	after, _ := ps.ListRevisions(ctx, 200)
	if len(after) >= len(before) {
		t.Errorf("revisions before=%d after=%d; prune had no effect", len(before), len(after))
	}

	// The live revision must survive, or the display loses its content.
	liveAfter, err := ps.PublishedRevision(ctx)
	if err != nil {
		t.Fatalf("no live revision after prune: %v", err)
	}
	if liveAfter.ID != liveBefore.ID {
		t.Errorf("live revision changed from %d to %d during prune", liveBefore.ID, liveAfter.ID)
	}
	// The draft must survive too, or editing breaks.
	if _, err := ps.DraftRevision(ctx); err != nil {
		t.Errorf("draft revision lost during prune: %v", err)
	}
}

func TestPruneRespectsKeepFlag(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Scene", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	first, err := ps.Publish(ctx, "bram", "milestone")
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.SetKeep(ctx, first.PublishedRevision, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := ps.Publish(ctx, "bram", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ps.PruneRevisions(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.GetRevision(ctx, first.PublishedRevision); err != nil {
		t.Errorf("a pinned revision was pruned: %v", err)
	}
}

// ---- reference tracking --------------------------------------------------

func TestMediaReferenceTracking(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)
	seedMedia(t, db, 2)

	sc := imageScene("Uses media 1", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}

	used, err := ps.IsMediaReferenced(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("media 1 is used by a scene but reported unreferenced; it could be deleted")
	}
	used, err = ps.IsMediaReferenced(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if used {
		t.Error("media 2 is unused but reported as referenced")
	}
}

// Media referenced only from inside a config blob (a video poster, a countdown
// completion image) must still count as in use, or orphan cleanup deletes a file
// that a live scene renders.
func TestMediaReferencedFromConfigJSON(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)
	seedMedia(t, db, 7)

	sc := &content.Scene{
		Name: "Countdown", Enabled: true, Layout: content.LayoutFullscreen,
		DurationMS: 30000, DaysMask: content.AllDaysMask,
		Zones: []content.Zone{{
			Slot: "a", ContentType: content.TypeCountdown,
			Config: `{"title":"Launch","target":"2026-08-14T00:00:00+02:00",` +
				`"timezone":"Europe/Amsterdam","completion_image_id":7}`,
		}},
	}
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatalf("save: %v", err)
	}

	used, err := ps.IsMediaReferenced(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("media referenced only from config JSON was reported unreferenced")
	}
}

func TestGetSceneNotFound(t *testing.T) {
	ps, _ := newStore(t)
	if _, err := ps.GetScene(context.Background(), 12345); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetScene(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeleteScene(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Doomed", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	if err := ps.DeleteScene(ctx, sc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := ps.DeleteScene(ctx, sc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}

	// Zones must go with the scene (ON DELETE CASCADE).
	var zones int
	if err := db.QueryRow(`SELECT COUNT(*) FROM zones WHERE scene_id = ?`, sc.ID).Scan(&zones); err != nil {
		t.Fatal(err)
	}
	if zones != 0 {
		t.Errorf("%d orphaned zones remain after deleting the scene", zones)
	}
}

func TestBulkOperations(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	var ids []int64
	for i := 0; i < 3; i++ {
		sc := imageScene("Scene", "1")
		if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sc.ID)
	}

	if err := ps.BulkSetEnabled(ctx, ids, false, "bram"); err != nil {
		t.Fatal(err)
	}
	draft, _ := ps.DraftRevision(ctx)
	scenes, _ := ps.ListScenes(ctx, draft.ID)
	for _, s := range scenes {
		if s.Enabled {
			t.Errorf("scene %d still enabled after bulk disable", s.ID)
		}
	}

	if err := ps.BulkSetDuration(ctx, ids, 45000, "bram"); err != nil {
		t.Fatal(err)
	}
	scenes, _ = ps.ListScenes(ctx, draft.ID)
	for _, s := range scenes {
		if s.DurationMS != 45000 {
			t.Errorf("scene %d duration = %d, want 45000", s.ID, s.DurationMS)
		}
	}

	if err := ps.BulkSetDuration(ctx, ids, 10, "bram"); err == nil {
		t.Error("bulk duration below the minimum was accepted")
	}
}

func TestStableIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewStableID()
		if seen[id] {
			t.Fatalf("duplicate stable ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestMarkPlayed(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Played", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkPlayed(ctx, sc.StableID, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := ps.GetScene(ctx, sc.ID)
	if got.LastPlayedAt == nil {
		t.Error("last_played_at not recorded")
	}

	if err := ps.MarkPlayed(ctx, sc.StableID, "image decode failed"); err != nil {
		t.Fatal(err)
	}
	got, _ = ps.GetScene(ctx, sc.ID)
	if got.LastError != "image decode failed" {
		t.Errorf("last_error = %q, want the reported failure", got.LastError)
	}
}

func TestPublishIsAtomicUnderConcurrentReads(t *testing.T) {
	ps, db := newStore(t)
	ctx := context.Background()
	seedMedia(t, db, 1)

	sc := imageScene("Scene", "1")
	if err := ps.SaveScene(ctx, sc, "bram"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Publish(ctx, "bram", ""); err != nil {
		t.Fatal(err)
	}

	// Continuously read the live revision while publishing repeatedly. A reader
	// must never observe a revision without scenes.
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			live, err := ps.PublishedRevision(ctx)
			if err != nil {
				errCh <- err
				return
			}
			scenes, err := ps.ListScenes(ctx, live.ID)
			if err != nil {
				errCh <- err
				return
			}
			if len(scenes) == 0 {
				errCh <- errors.New("observed a live revision with zero scenes mid-publish")
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < 10; i++ {
		if _, err := ps.Publish(ctx, "bram", ""); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	<-done
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}
