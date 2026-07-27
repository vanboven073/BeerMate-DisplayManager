package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
)

// randSuffix returns 4 hex chars to disambiguate same-second backup filenames.
func randSuffix() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

// Backup describes one stored backup archive.
type Backup struct {
	ID        int64     `json:"id"`
	Filename  string    `json:"filename"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	Kind      string    `json:"kind"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// BackupManifest is embedded in each archive so a restore can verify what it
// contains before touching the live system.
type BackupManifest struct {
	Version    string    `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
	Kind       string    `json:"kind"`
	SchemaVer  int       `json:"schema_version"`
	Includes   []string  `json:"includes"`
	Excludes   []string  `json:"excludes"`
	MediaCount int       `json:"media_count"`
}

// BackupStore creates, lists, verifies and restores backups.
//
// A backup is a gzipped tar containing a consistent copy of the SQLite database
// plus the media metadata, and a manifest. It deliberately EXCLUDES the browser
// profiles (which hold live session cookies) and the raw encryption key, so a
// downloaded backup cannot be replayed to impersonate a website login. Media
// binaries are excluded by default to keep archives small; the manifest records
// this so a restore knows the files must already be present.
type BackupStore struct {
	db         *dbx.DB
	dir        string
	dbPath     string
	uploadsDir string
	retention  int
	now        func() time.Time
}

// NewBackupStore builds a BackupStore.
func NewBackupStore(db *dbx.DB, backupDir, dbPath, uploadsDir string, retention int) *BackupStore {
	if retention < 1 {
		retention = 10
	}
	return &BackupStore{
		db: db, dir: backupDir, dbPath: dbPath, uploadsDir: uploadsDir,
		retention: retention, now: time.Now,
	}
}

// SetClock overrides the time source, for tests.
func (s *BackupStore) SetClock(fn func() time.Time) { s.now = fn }

// Create writes a new backup archive and records it.
func (s *BackupStore) Create(ctx context.Context, kind, note, actor string) (Backup, error) {
	switch kind {
	case "manual", "pre_change", "pre_restore", "scheduled":
	default:
		kind = "manual"
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return Backup{}, err
	}

	// Checkpoint first so the copied database file is self-contained and does not
	// depend on a separate WAL that is not in the archive.
	if err := s.db.Checkpoint(ctx); err != nil {
		return Backup{}, fmt.Errorf("backup: checkpoint: %w", err)
	}

	// A short random suffix disambiguates backups taken within the same second:
	// an automatic pre-change snapshot and a manual backup can easily collide on
	// a second-granularity timestamp, and O_EXCL would then reject the second.
	ts := s.now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("beermate-backup-%s-%s-%s.tar.gz", kind, ts, randSuffix())
	fullPath := filepath.Join(s.dir, filename)

	schemaVer, _ := s.db.SchemaVersion(ctx)
	var mediaCount int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&mediaCount)

	manifest := BackupManifest{
		Version:    "1",
		CreatedAt:  s.now().UTC(),
		CreatedBy:  actor,
		Kind:       kind,
		SchemaVer:  schemaVer,
		Includes:   []string{"database", "media_metadata", "settings", "playlist", "scenes", "schedule", "social_config", "website_metadata"},
		Excludes:   []string{"browser_profiles", "encryption_key", "session_cookies", "raw_media_files"},
		MediaCount: mediaCount,
	}

	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return Backup{}, fmt.Errorf("backup: create file: %w", err)
	}

	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	gz := gzip.NewWriter(mw)
	tw := tar.NewWriter(gz)

	writeErr := func() error {
		// manifest.json
		mb, _ := json.MarshalIndent(manifest, "", "  ")
		if err := writeTarBytes(tw, "manifest.json", mb, s.now); err != nil {
			return err
		}
		// database file
		if err := writeTarFile(tw, "database/beermate.db", s.dbPath, s.now); err != nil {
			return err
		}
		return nil
	}()

	if cerr := tw.Close(); cerr != nil && writeErr == nil {
		writeErr = cerr
	}
	if cerr := gz.Close(); cerr != nil && writeErr == nil {
		writeErr = cerr
	}
	if cerr := f.Close(); cerr != nil && writeErr == nil {
		writeErr = cerr
	}
	if writeErr != nil {
		_ = os.Remove(fullPath)
		return Backup{}, fmt.Errorf("backup: write archive: %w", writeErr)
	}

	info, _ := os.Stat(fullPath)
	sum := hex.EncodeToString(hasher.Sum(nil))

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO backups (filename, bytes, sha256, kind, note, created_at, created_by)
		VALUES (?,?,?,?,?,?,?)`,
		filename, info.Size(), sum, kind, note, rfc3339(s.now()), actor)
	if err != nil {
		_ = os.Remove(fullPath)
		return Backup{}, err
	}
	id, _ := res.LastInsertId()

	if err := s.prune(ctx); err != nil {
		// A prune failure must not fail the backup that just succeeded.
		_ = err
	}

	return Backup{
		ID: id, Filename: filename, Bytes: info.Size(), SHA256: sum,
		Kind: kind, Note: note, CreatedAt: s.now().UTC(), CreatedBy: actor,
	}, nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, now func() time.Time) error {
	hdr := &tar.Header{Name: name, Mode: 0o640, Size: int64(len(data)), ModTime: now().UTC()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, name, path string, now func() time.Time) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: 0o640, Size: info.Size(), ModTime: now().UTC()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// List returns backups, newest first.
func (s *BackupStore) List(ctx context.Context) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, filename, bytes, sha256, kind, note, created_at, created_by
		  FROM backups ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Backup
	for rows.Next() {
		var b Backup
		var created string
		if err := rows.Scan(&b.ID, &b.Filename, &b.Bytes, &b.SHA256, &b.Kind,
			&b.Note, &created, &b.CreatedBy); err != nil {
			return nil, err
		}
		b.CreatedAt = parseTime(created)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get returns one backup record.
func (s *BackupStore) Get(ctx context.Context, id int64) (Backup, error) {
	var b Backup
	var created string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, filename, bytes, sha256, kind, note, created_at, created_by
		  FROM backups WHERE id = ?`, id).
		Scan(&b.ID, &b.Filename, &b.Bytes, &b.SHA256, &b.Kind, &b.Note, &created, &b.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	if err != nil {
		return Backup{}, err
	}
	b.CreatedAt = parseTime(created)
	return b, nil
}

// Path returns the absolute archive path for a backup, validating the filename.
func (s *BackupStore) Path(filename string) (string, error) {
	if filename != filepath.Base(filename) || strings.Contains(filename, "..") ||
		!strings.HasSuffix(filename, ".tar.gz") {
		return "", errors.New("backup: refusing suspicious filename")
	}
	return filepath.Join(s.dir, filename), nil
}

// Verify checks an archive's checksum and that its manifest parses.
func (s *BackupStore) Verify(ctx context.Context, id int64) (BackupManifest, error) {
	b, err := s.Get(ctx, id)
	if err != nil {
		return BackupManifest{}, err
	}
	path, err := s.Path(b.Filename)
	if err != nil {
		return BackupManifest{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: open archive: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	tr := io.TeeReader(f, hasher)
	gz, err := gzip.NewReader(tr)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: gzip: %w", err)
	}
	defer gz.Close()

	var manifest BackupManifest
	found := false
	tarr := tar.NewReader(gz)
	for {
		hdr, err := tarr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return BackupManifest{}, fmt.Errorf("backup: read archive: %w", err)
		}
		if hdr.Name == "manifest.json" {
			data, _ := io.ReadAll(io.LimitReader(tarr, 1<<20))
			if err := json.Unmarshal(data, &manifest); err != nil {
				return BackupManifest{}, fmt.Errorf("backup: manifest is corrupt: %w", err)
			}
			found = true
		} else {
			_, _ = io.Copy(io.Discard, tarr)
		}
	}
	if !found {
		return BackupManifest{}, errors.New("backup: archive has no manifest")
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	if sum != b.SHA256 {
		return manifest, fmt.Errorf("backup: checksum mismatch; the archive is corrupt or was modified")
	}
	return manifest, nil
}

// Restore replaces the live database with the one in a backup archive.
//
// A pre-restore backup is taken first so the operation is itself reversible, and
// the extracted database is integrity-checked before it is swapped in. The caller
// must restart the service afterwards: the running process still holds the old
// database handle.
func (s *BackupStore) Restore(ctx context.Context, id int64, actor string) error {
	manifest, err := s.Verify(ctx, id)
	if err != nil {
		return err
	}
	b, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	path, err := s.Path(b.Filename)
	if err != nil {
		return err
	}

	// Safety net: back up the current state before overwriting it.
	if _, err := s.Create(ctx, "pre_restore", "automatic snapshot before restore", actor); err != nil {
		return fmt.Errorf("backup: could not take a pre-restore snapshot: %w", err)
	}

	tmp := s.dbPath + ".restore-tmp"
	if err := extractDB(path, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	// Integrity-check the extracted database before it goes live.
	check, err := dbx.Open(dbx.Options{Path: tmp, ReadOnly: true})
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: extracted database will not open: %w", err)
	}
	icErr := check.IntegrityCheck(ctx)
	check.Close()
	if icErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: extracted database failed integrity check: %w", icErr)
	}

	// Swap the file into place. The WAL/SHM of the old database are removed so a
	// stale WAL cannot be replayed over the restored file on next open.
	if err := os.Rename(tmp, s.dbPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: swap database: %w", err)
	}
	_ = os.Remove(s.dbPath + "-wal")
	_ = os.Remove(s.dbPath + "-shm")

	_ = manifest
	return nil
}

func extractDB(archivePath, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name == "database/beermate.db" {
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
			if err != nil {
				return err
			}
			// Bound extraction so a maliciously crafted archive cannot fill the disk.
			if _, err := io.Copy(out, io.LimitReader(tr, 2<<30)); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		_, _ = io.Copy(io.Discard, tr)
	}
	return errors.New("backup: archive does not contain a database")
}

// prune deletes backups beyond the retention limit, removing both the row and
// the file so archives cannot grow without bound.
func (s *BackupStore) prune(ctx context.Context) error {
	backups, err := s.List(ctx)
	if err != nil {
		return err
	}
	if len(backups) <= s.retention {
		return nil
	}
	// Keep the newest N; delete the rest.
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	for _, old := range backups[s.retention:] {
		if p, err := s.Path(old.Filename); err == nil {
			_ = os.Remove(p)
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, old.ID)
	}
	return nil
}

// Delete removes one backup.
func (s *BackupStore) Delete(ctx context.Context, id int64) error {
	b, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if p, perr := s.Path(b.Filename); perr == nil {
		_ = os.Remove(p)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	return err
}
