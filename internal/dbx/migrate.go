package dbx

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one forward schema step.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations reads and orders the embedded migration files.
//
// Files are named NNNN_description.sql. Only forward migrations exist: this is an
// appliance with a single deployment target, and rollback is performed by
// restoring the pre-upgrade database snapshot that update-jetson.sh always takes.
// That is both safer and simpler than maintaining reversible DDL.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		idx := strings.Index(base, "_")
		if idx < 1 {
			return nil, fmt.Errorf("migration %q: expected NNNN_name.sql", e.Name())
		}
		v, err := strconv.Atoi(base[:idx])
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version: %w", e.Name(), err)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("duplicate migration version %d (%s and %s)", v, prev, e.Name())
		}
		seen[v] = e.Name()

		body, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: base[idx+1:], SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies every migration newer than the recorded schema version.
//
// Each migration runs in its own transaction, so a failure leaves the database at
// the last good version rather than half-applied.
func (d *DB) Migrate(ctx context.Context) (applied int, err error) {
	if _, err := d.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}

	current, err := d.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}

	migrations, err := LoadMigrations()
	if err != nil {
		return 0, err
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		err := d.Tx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range splitStatements(m.SQL) {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("migration %04d_%s: %w\n--- statement ---\n%s",
						m.Version, m.Name, err, truncate(stmt, 400))
				}
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.Version, m.Name)
			return err
		})
		if err != nil {
			return applied, err
		}
		d.log.Info("migration applied", "version", m.Version, "name", m.Name)
		applied++
	}
	return applied, nil
}

// SchemaVersion returns the highest applied migration version, or 0.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := d.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

// splitStatements splits a migration file on semicolons at statement level.
//
// It is aware of string literals, line comments and block comments, and of
// BEGIN...END blocks so that CREATE TRIGGER bodies survive intact.
func splitStatements(sqlText string) []string {
	var (
		out       []string
		cur       strings.Builder
		inStr     bool
		strDelim  rune
		inLine    bool
		inBlock   bool
		beginDep  int
		runes     = []rune(sqlText)
	)

	wordBefore := func(i int) string {
		j := i
		for j > 0 && (isWordRune(runes[j-1])) {
			j--
		}
		return strings.ToUpper(string(runes[j:i]))
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		switch {
		case inLine:
			cur.WriteRune(c)
			if c == '\n' {
				inLine = false
			}
			continue
		case inBlock:
			cur.WriteRune(c)
			if c == '*' && next == '/' {
				cur.WriteRune(next)
				i++
				inBlock = false
			}
			continue
		case inStr:
			cur.WriteRune(c)
			if c == strDelim {
				if next == strDelim { // escaped quote
					cur.WriteRune(next)
					i++
				} else {
					inStr = false
				}
			}
			continue
		}

		switch {
		case c == '-' && next == '-':
			inLine = true
			cur.WriteRune(c)
		case c == '/' && next == '*':
			inBlock = true
			cur.WriteRune(c)
		case c == '\'' || c == '"' || c == '`':
			inStr, strDelim = true, c
			cur.WriteRune(c)
		case c == ';' && beginDep == 0:
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(c)
			if !isWordRune(c) || i+1 >= len(runes) || !isWordRune(next) {
				switch wordBefore(i + 1) {
				case "BEGIN":
					beginDep++
				case "END":
					if beginDep > 0 {
						beginDep--
					}
				}
			}
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
