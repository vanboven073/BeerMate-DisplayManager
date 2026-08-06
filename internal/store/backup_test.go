package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
)

func newBackupStore(t *testing.T) (*BackupStore, *dbx.DB, string) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "database", "beermate.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o750)

	db, err := dbx.Open(dbx.Options{Path: dbPath, Logger: logging.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	bs := NewBackupStore(db, filepath.Join(root, "backups"), dbPath, filepath.Join(root, "uploads"), 3)
	return bs, db, root
}

func TestBackupCreateListVerify(t *testing.T) {
	bs, _, _ := newBackupStore(t)
	ctx := context.Background()

	b, err := bs.Create(ctx, "manual", "test backup", "bram")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.Bytes == 0 || b.SHA256 == "" {
		t.Error("backup metadata is incomplete")
	}

	list, err := bs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d backups, want 1", len(list))
	}

	manifest, err := bs.Verify(ctx, b.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if manifest.Kind != "manual" {
		t.Errorf("manifest kind = %q, want manual", manifest.Kind)
	}
	// Browser profiles and the encryption key must be excluded from a downloadable
	// backup, or a stolen archive could impersonate a website login.
	for _, exc := range []string{"browser_profiles", "encryption_key", "session_cookies"} {
		found := false
		for _, e := range manifest.Excludes {
			if e == exc {
				found = true
			}
		}
		if !found {
			t.Errorf("manifest does not record %q as excluded", exc)
		}
	}
}

// A corrupted archive must fail verification rather than being restored.
func TestBackupVerifyDetectsCorruption(t *testing.T) {
	bs, _, _ := newBackupStore(t)
	ctx := context.Background()

	b, err := bs.Create(ctx, "manual", "", "bram")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := bs.Path(b.Filename)
	// Flip some bytes in the middle of the archive.
	data, _ := os.ReadFile(path)
	if len(data) > 40 {
		data[len(data)/2] ^= 0xFF
		_ = os.WriteFile(path, data, 0o640)
	}
	if _, err := bs.Verify(ctx, b.ID); err == nil {
		t.Error("verification passed on a corrupted archive")
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	// Restore renames the extracted database over the live path. That is atomic
	// and safe on Linux (the production target) even while the old handle is
	// open, because the handle keeps the old inode. Windows refuses to rename
	// over an open file, so this exercises Linux filesystem semantics and is
	// skipped on the Windows dev workstation.
	if runtime.GOOS == "windows" {
		t.Skip("restore uses rename-over-open-file, which is Linux-only semantics")
	}
	bs, db, _ := newBackupStore(t)
	ctx := context.Background()

	// Establish a known state: one user.
	_, err := db.Exec(`INSERT INTO users (username, username_lower, password_hash, created_at, updated_at)
		VALUES ('bram','bram','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}

	b, err := bs.Create(ctx, "manual", "with one user", "bram")
	if err != nil {
		t.Fatal(err)
	}

	// Mutate: add a second user.
	if _, err := db.Exec(`INSERT INTO users (username, username_lower, password_hash, created_at, updated_at)
		VALUES ('eve','eve','y','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 users before restore, got %d", count)
	}

	// Restore replaces the on-disk database file. The live handle still points at
	// the old file, so the test opens the restored file fresh to verify contents.
	if err := bs.Restore(ctx, b.ID, "bram"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored, err := dbx.Open(dbx.Options{Path: bs.dbPath, Logger: logging.Discard()})
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restored.Close()
	_ = restored.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if count != 1 {
		t.Errorf("restored database has %d users, want 1 (the state at backup time)", count)
	}

	// The restore must itself have taken a pre-restore snapshot.
	list, _ := bs.List(ctx)
	var hasPreRestore bool
	for _, bk := range list {
		if bk.Kind == "pre_restore" {
			hasPreRestore = true
		}
	}
	if !hasPreRestore {
		t.Error("restore did not take a pre-restore snapshot")
	}
}

func TestBackupRetentionPrunes(t *testing.T) {
	bs, _, _ := newBackupStore(t) // retention 3
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		if _, err := bs.Create(ctx, "manual", "", "bram"); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := bs.List(ctx)
	if len(list) > 3 {
		t.Errorf("retention not enforced: %d backups remain, want <= 3", len(list))
	}

	// Pruned archives must be removed from disk too, not just the database.
	entries, _ := os.ReadDir(bs.dir)
	if len(entries) > 3 {
		t.Errorf("%d archive files on disk, want <= 3", len(entries))
	}
}

func TestBackupPathRejectsTraversal(t *testing.T) {
	bs, _, _ := newBackupStore(t)
	for _, name := range []string{"../etc/passwd", "a/b.tar.gz", "..", "evil.txt", "x.tar.gz/../y"} {
		if _, err := bs.Path(name); err == nil {
			t.Errorf("Path accepted suspicious filename %q", name)
		}
	}
	if _, err := bs.Path("beermate-backup-manual-20260101-000000.tar.gz"); err != nil {
		t.Errorf("Path rejected a legitimate filename: %v", err)
	}
}
