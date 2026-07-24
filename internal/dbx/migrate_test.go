package dbx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "test.db"), Logger: logging.Discard()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	n, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n == 0 {
		t.Fatal("expected at least one migration to be applied")
	}

	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v < 1 {
		t.Fatalf("schema version = %d, want >= 1", v)
	}

	// Re-running must be a no-op, not an error.
	n2, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second migrate applied %d migrations, want 0", n2)
	}
}

func TestMigrateCreatesExpectedTables(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []string{
		"users", "sessions", "login_attempts", "audit_log", "settings", "credentials",
		"media", "websites", "social_feeds", "social_posts", "playlist_revisions",
		"scenes", "zones", "schedule_rules", "schedule_overrides", "emergency_messages",
		"player_state", "backups",
	}
	for _, table := range want {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestSeedData(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var drafts int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM playlist_revisions WHERE is_draft=1`).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 1 {
		t.Errorf("draft revisions = %d, want exactly 1", drafts)
	}

	var rules int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schedule_rules WHERE kind='weekly'`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 7 {
		t.Errorf("weekly schedule rules = %d, want 7 (one per day)", rules)
	}

	var on, off string
	if err := db.QueryRowContext(ctx,
		`SELECT on_time, off_time FROM schedule_rules WHERE kind='weekly' AND weekday=0`).
		Scan(&on, &off); err != nil {
		t.Fatal(err)
	}
	if on != "08:00" || off != "17:00" {
		t.Errorf("Monday schedule = %s-%s, want 08:00-17:00 (legacy kiosk default)", on, off)
	}
}

// The single-draft invariant is what makes publishing atomic. It must be enforced
// by the database, not only by application code.
func TestSingleDraftInvariantEnforced(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO playlist_revisions (is_draft, created_at) VALUES (1, '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected a second draft revision to be rejected by the unique index")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Errorf("expected a UNIQUE constraint violation, got: %v", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// zones.scene_id references a scene that does not exist.
	_, err := db.ExecContext(ctx,
		`INSERT INTO zones (scene_id, slot) VALUES (99999, 'a')`)
	if err == nil {
		t.Fatal("expected foreign key violation; is PRAGMA foreign_keys enabled?")
	}
}

func TestIntegrityCheck(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.IntegrityCheck(ctx); err != nil {
		t.Errorf("integrity check: %v", err)
	}
	if err := db.Checkpoint(ctx); err != nil {
		t.Errorf("checkpoint: %v", err)
	}
}

func TestSplitStatements(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"simple", "SELECT 1; SELECT 2;", 2},
		{"trailing no semicolon", "SELECT 1; SELECT 2", 2},
		{"semicolon in string", "INSERT INTO t VALUES ('a;b'); SELECT 1;", 2},
		{"line comment", "SELECT 1; -- a; comment\nSELECT 2;", 2},
		{"block comment", "SELECT 1; /* a; b */ SELECT 2;", 2},
		{"escaped quote", "INSERT INTO t VALUES ('it''s; fine'); SELECT 1;", 2},
		{
			"trigger body keeps BEGIN..END together",
			`CREATE TRIGGER x AFTER INSERT ON t BEGIN UPDATE a SET b=1; UPDATE c SET d=2; END;
			 SELECT 1;`,
			2,
		},
		{"empty", "  \n  ", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitStatements(tt.in)
			if len(got) != tt.want {
				t.Errorf("split into %d statements, want %d:\n%#v", len(got), tt.want, got)
			}
		})
	}
}

func TestLoadMigrationsOrdered(t *testing.T) {
	ms, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version <= ms[i-1].Version {
			t.Errorf("migrations out of order at %d: %d after %d", i, ms[i].Version, ms[i-1].Version)
		}
	}
}
