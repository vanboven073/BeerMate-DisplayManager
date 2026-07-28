package dbx

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestPragmasApplyToEveryPooledConnection checks that the per-connection pragmas
// Open executes are in force on all four pooled connections, not just whichever
// one happened to serve the ExecContext call.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "probe.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Pin four distinct connections open simultaneously so each must be created.
	conns := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	for i, c := range conns {
		var sync int
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
			t.Fatalf("conn %d read synchronous: %v", i, err)
		}
		var cache int
		if err := c.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cache); err != nil {
			t.Fatalf("conn %d read cache_size: %v", i, err)
		}
		var journal string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatalf("conn %d read journal_mode: %v", i, err)
		}
		t.Logf("conn %d: synchronous=%d cache_size=%d journal_mode=%s", i, sync, cache, journal)

		// 1 == NORMAL, which is what Open intends under WAL.
		if sync != 1 {
			t.Errorf("conn %d: synchronous=%d, want 1 (NORMAL)", i, sync)
		}
		if cache != -8000 {
			t.Errorf("conn %d: cache_size=%d, want -8000", i, cache)
		}
	}

	for _, c := range conns {
		c.Close()
	}
}
