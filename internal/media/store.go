package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
)

// ErrNotFound is returned when a media row does not exist.
var ErrNotFound = errors.New("media: not found")

// ErrTooLarge is returned when a file exceeds its configured cap.
var ErrTooLarge = errors.New("media: file exceeds the size limit")

// Item is a stored media record.
type Item struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	StoredName   string    `json:"-"`
	OriginalName string    `json:"original_name"`
	MIME         string    `json:"mime"`
	Bytes        int64     `json:"bytes"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	DurationMS   int       `json:"duration_ms"`
	SHA256       string    `json:"sha256"`
	ThumbName    string    `json:"-"`
	HasThumb     bool      `json:"has_thumbnail"`
	PosterID     *int64    `json:"poster_id,omitempty"`
	ParentID     *int64    `json:"parent_id,omitempty"`
	PageNumber   int       `json:"page_number,omitempty"`
	Warning      string    `json:"warning,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Limits caps upload sizes.
type Limits struct {
	Image int64
	Video int64
	PDF   int64
}

// Store persists media files and their metadata.
type Store struct {
	db        *dbx.DB
	uploadDir string
	thumbDir  string
	limits    Limits
	log       *slog.Logger
	now       func() time.Time
}

// NewStore builds a media Store.
func NewStore(db *dbx.DB, uploadDir, thumbDir string, limits Limits, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{
		db: db, uploadDir: uploadDir, thumbDir: thumbDir,
		limits: limits, log: log, now: time.Now,
	}
}

// SetClock overrides the time source, for tests.
func (s *Store) SetClock(fn func() time.Time) { s.now = fn }

// thumbMaxDim is the longest edge of a generated thumbnail. Large enough to look
// sharp in the admin grid on a high-DPI laptop, small enough that a library of
// hundreds costs little disk on the Jetson.
const thumbMaxDim = 480

// storedName generates the on-disk filename.
//
// The client's filename is never used to build a path. It is kept only as
// display metadata, which removes path traversal, collision and encoding issues
// in one step rather than trying to sanitise arbitrary user input.
func storedName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("media: generate name: %w", err)
	}
	return hex.EncodeToString(b) + ext, nil
}

// Save validates and stores an uploaded file.
//
// The reader is consumed at most maxBytes+1 bytes so a client cannot stream an
// unbounded file and fill the disk before the size check runs.
func (s *Store) Save(ctx context.Context, r io.Reader, originalName, actor string) (Item, error) {
	head := make([]byte, SniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Item{}, fmt.Errorf("media: read header: %w", err)
	}
	head = head[:n]

	det, err := Detect(head)
	if err != nil {
		return Item{}, err
	}

	maxBytes := MaxBytesFor(det, s.limits.Image, s.limits.Video, s.limits.PDF)

	name, err := storedName(det.Extension)
	if err != nil {
		return Item{}, err
	}
	if err := os.MkdirAll(s.uploadDir, 0o750); err != nil {
		return Item{}, fmt.Errorf("media: create upload dir: %w", err)
	}
	dst := filepath.Join(s.uploadDir, name)

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return Item{}, fmt.Errorf("media: create file: %w", err)
	}
	cleanup := func() {
		f.Close()
		_ = os.Remove(dst)
	}

	hasher := sha256.New()
	// Reading one byte past the cap is what makes "exactly at the limit" pass
	// and "one byte over" fail, without buffering the whole file.
	limited := io.LimitReader(io.MultiReader(strings.NewReader(string(head)), r), maxBytes+1)
	written, err := io.Copy(io.MultiWriter(f, hasher), limited)
	if err != nil {
		cleanup()
		return Item{}, fmt.Errorf("media: write file: %w", err)
	}
	if written > maxBytes {
		cleanup()
		return Item{}, fmt.Errorf("%w: %s files are limited to %d MiB",
			ErrTooLarge, det.Kind, maxBytes>>20)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return Item{}, fmt.Errorf("media: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return Item{}, fmt.Errorf("media: close: %w", err)
	}

	item := Item{
		Kind:         det.Kind,
		StoredName:   name,
		OriginalName: sanitiseDisplayName(originalName),
		MIME:         det.MIME,
		Bytes:        written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
		Warning:      det.Warning,
		CreatedBy:    actor,
	}
	if !ExtensionMatches(originalName, det) && originalName != "" {
		item.Warning = joinWarnings(item.Warning,
			fmt.Sprintf("the file was uploaded as %q but its contents are %s; "+
				"the detected type is used", filepath.Ext(originalName), det.MIME))
	}

	// Dimensions and a thumbnail, best effort: a file we cannot decode is still
	// stored, it just shows a placeholder in the library.
	if det.Kind == KindImage {
		if w, h, err := s.decodeDimensions(dst, det.MIME); err == nil {
			item.Width, item.Height = w, h
		} else {
			s.log.Warn("could not read image dimensions", "error", err)
		}
		if thumb, err := s.makeThumbnail(dst, det.MIME); err == nil {
			item.ThumbName = thumb
		} else {
			s.log.Warn("could not generate thumbnail", "error", err)
		}
	}

	if err := s.insert(ctx, &item); err != nil {
		_ = os.Remove(dst)
		if item.ThumbName != "" {
			_ = os.Remove(filepath.Join(s.thumbDir, item.ThumbName))
		}
		return Item{}, err
	}
	return item, nil
}

func joinWarnings(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// sanitiseDisplayName strips control characters and path separators from the
// original filename. It is display-only metadata, but it is echoed into the
// admin UI, so it must not carry anything surprising.
func sanitiseDisplayName(s string) string {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "." || s == ".." || s == "/" {
		return ""
	}
	return s
}

func (s *Store) decodeDimensions(path, mime string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	if mime == "image/webp" {
		img, err := webp.Decode(f)
		if err != nil {
			return 0, 0, err
		}
		b := img.Bounds()
		return b.Dx(), b.Dy(), nil
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func (s *Store) makeThumbnail(srcPath, mime string) (string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var img image.Image
	switch mime {
	case "image/jpeg":
		img, err = jpeg.Decode(f)
	case "image/png":
		img, err = png.Decode(f)
	case "image/gif":
		img, err = gif.Decode(f)
	case "image/webp":
		img, err = webp.Decode(f)
	default:
		return "", fmt.Errorf("media: no thumbnailer for %s", mime)
	}
	if err != nil {
		return "", err
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return "", errors.New("media: image has no area")
	}
	scale := float64(thumbMaxDim) / float64(max(w, h))
	if scale > 1 {
		scale = 1 // never upscale; it only wastes disk
	}
	tw, th := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))

	dstImg := image.NewRGBA(image.Rect(0, 0, tw, th))
	// CatmullRom is noticeably sharper than bilinear for photographic content and
	// this runs once per upload, not per frame.
	draw.CatmullRom.Scale(dstImg, dstImg.Bounds(), img, b, draw.Over, nil)

	if err := os.MkdirAll(s.thumbDir, 0o750); err != nil {
		return "", err
	}
	name, err := storedName(".jpg")
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(filepath.Join(s.thumbDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if err := jpeg.Encode(out, dstImg, &jpeg.Options{Quality: 82}); err != nil {
		_ = os.Remove(filepath.Join(s.thumbDir, name))
		return "", err
	}
	return name, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Store) insert(ctx context.Context, item *Item) error {
	now := rfc3339(s.now())
	item.CreatedAt, item.UpdatedAt = s.now().UTC(), s.now().UTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO media (kind, stored_name, original_name, mime, bytes, width, height,
			duration_ms, sha256, thumb_name, poster_id, parent_id, page_number, warning,
			created_at, created_by, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.Kind, item.StoredName, item.OriginalName, item.MIME, item.Bytes,
		item.Width, item.Height, item.DurationMS, item.SHA256, item.ThumbName,
		item.PosterID, item.ParentID, item.PageNumber, item.Warning,
		now, item.CreatedBy, now)
	if err != nil {
		return fmt.Errorf("media: insert: %w", err)
	}
	item.ID, _ = res.LastInsertId()
	item.HasThumb = item.ThumbName != ""
	return nil
}

// Get loads one media item.
func (s *Store) Get(ctx context.Context, id int64) (Item, error) {
	return s.scan(s.db.QueryRowContext(ctx, mediaSelect+` WHERE id = ?`, id))
}

const mediaSelect = `
	SELECT id, kind, stored_name, original_name, mime, bytes, width, height,
	       duration_ms, sha256, thumb_name, poster_id, parent_id, page_number,
	       warning, created_at, created_by, updated_at
	  FROM media`

func (s *Store) scan(row *sql.Row) (Item, error) {
	var (
		it      Item
		poster  sql.NullInt64
		parent  sql.NullInt64
		created string
		updated string
	)
	err := row.Scan(&it.ID, &it.Kind, &it.StoredName, &it.OriginalName, &it.MIME,
		&it.Bytes, &it.Width, &it.Height, &it.DurationMS, &it.SHA256, &it.ThumbName,
		&poster, &parent, &it.PageNumber, &it.Warning, &created, &it.CreatedBy, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	if poster.Valid {
		it.PosterID = &poster.Int64
	}
	if parent.Valid {
		it.ParentID = &parent.Int64
	}
	it.CreatedAt, it.UpdatedAt = parseTime(created), parseTime(updated)
	it.HasThumb = it.ThumbName != ""
	return it, nil
}

// List returns media of a kind, newest first. Passing an empty kind lists all
// except individual PDF pages, which are shown nested under their parent.
func (s *Store) List(ctx context.Context, kind string, limit, offset int) ([]Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := mediaSelect + ` WHERE kind != 'pdf_page'`
	args := []any{}
	if kind != "" {
		q = mediaSelect + ` WHERE kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var (
			it      Item
			poster  sql.NullInt64
			parent  sql.NullInt64
			created string
			updated string
		)
		if err := rows.Scan(&it.ID, &it.Kind, &it.StoredName, &it.OriginalName, &it.MIME,
			&it.Bytes, &it.Width, &it.Height, &it.DurationMS, &it.SHA256, &it.ThumbName,
			&poster, &parent, &it.PageNumber, &it.Warning, &created, &it.CreatedBy, &updated); err != nil {
			return nil, err
		}
		if poster.Valid {
			it.PosterID = &poster.Int64
		}
		if parent.Valid {
			it.ParentID = &parent.Int64
		}
		it.CreatedAt, it.UpdatedAt = parseTime(created), parseTime(updated)
		it.HasThumb = it.ThumbName != ""
		out = append(out, it)
	}
	return out, rows.Err()
}

// Pages returns the imported pages of a PDF, in order.
func (s *Store) Pages(ctx context.Context, parentID int64) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx,
		mediaSelect+` WHERE parent_id = ? ORDER BY page_number`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var (
			it      Item
			poster  sql.NullInt64
			parent  sql.NullInt64
			created string
			updated string
		)
		if err := rows.Scan(&it.ID, &it.Kind, &it.StoredName, &it.OriginalName, &it.MIME,
			&it.Bytes, &it.Width, &it.Height, &it.DurationMS, &it.SHA256, &it.ThumbName,
			&poster, &parent, &it.PageNumber, &it.Warning, &created, &it.CreatedBy, &updated); err != nil {
			return nil, err
		}
		if parent.Valid {
			it.ParentID = &parent.Int64
		}
		it.CreatedAt, it.UpdatedAt = parseTime(created), parseTime(updated)
		it.HasThumb = it.ThumbName != ""
		out = append(out, it)
	}
	return out, rows.Err()
}

// Path returns the absolute path of a stored file.
//
// The name comes from the database, never from a request, and is re-validated
// here so a corrupted row cannot escape the upload directory.
func (s *Store) Path(storedName string) (string, error) {
	return safeJoin(s.uploadDir, storedName)
}

// ThumbPath returns the absolute path of a thumbnail.
func (s *Store) ThumbPath(thumbName string) (string, error) {
	return safeJoin(s.thumbDir, thumbName)
}

// safeJoin refuses any name that is not a plain filename.
func safeJoin(dir, name string) (string, error) {
	if name == "" {
		return "", errors.New("media: empty filename")
	}
	// "." and ".." survive a Base() round-trip and a ".." substring check
	// respectively resolves to the directory itself and its parent, so they are
	// rejected by name before any path arithmetic.
	if name == "." || name == ".." {
		return "", fmt.Errorf("media: refusing relative path element %q", name)
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) ||
		strings.Contains(name, "..") {
		return "", fmt.Errorf("media: refusing suspicious filename %q", name)
	}
	full := filepath.Join(dir, name)
	// Belt and braces: confirm the result really is inside the directory.
	rel, err := filepath.Rel(dir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("media: path escapes the media directory")
	}
	return full, nil
}

// Delete removes a media item and its files.
func (s *Store) Delete(ctx context.Context, id int64) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	// Collect child pages first so their files are removed too; the row cascade
	// handles the database side.
	children, _ := s.Pages(ctx, id)

	res, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	for _, c := range append(children, it) {
		if p, err := s.Path(c.StoredName); err == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Warn("could not remove media file", "id", c.ID, "error", err)
			}
		}
		if c.ThumbName != "" {
			if p, err := s.ThumbPath(c.ThumbName); err == nil {
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					s.log.Warn("could not remove thumbnail", "id", c.ID, "error", err)
				}
			}
		}
	}
	return nil
}

// Rename updates the display name.
func (s *Store) Rename(ctx context.Context, id int64, name string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media SET original_name = ?, updated_at = ? WHERE id = ?`,
		sanitiseDisplayName(name), rfc3339(s.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPoster attaches a poster image to a video.
func (s *Store) SetPoster(ctx context.Context, videoID, posterID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media SET poster_id = ?, updated_at = ? WHERE id = ? AND kind = 'video'`,
		posterID, rfc3339(s.now()), videoID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Usage reports total bytes and item count.
func (s *Store) Usage(ctx context.Context) (bytes int64, count int, err error) {
	var b sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(bytes),0), COUNT(*) FROM media`).Scan(&b, &count)
	return b.Int64, count, err
}

// CleanOrphans removes files on disk with no database row.
//
// Files are only removed when older than minAge, so a file written moments ago
// by an upload still mid-transaction is never swept away.
func (s *Store) CleanOrphans(ctx context.Context, minAge time.Duration) (removed int, err error) {
	known := map[string]bool{}
	for _, spec := range []struct{ col, dir string }{
		{"stored_name", s.uploadDir},
		{"thumb_name", s.thumbDir},
	} {
		// #nosec G201 -- col is a compile-time constant from this literal slice.
		rows, err := s.db.QueryContext(ctx,
			"SELECT "+spec.col+" FROM media WHERE "+spec.col+" != ''")
		if err != nil {
			return removed, err
		}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return removed, err
			}
			known[spec.dir+string(filepath.Separator)+n] = true
		}
		rows.Close()
	}

	cutoff := s.now().Add(-minAge)
	for _, dir := range []string{s.uploadDir, s.thumbDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if known[full] {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(full); err != nil {
				s.log.Warn("could not remove orphaned file", "path", full, "error", err)
				continue
			}
			removed++
		}
	}
	return removed, nil
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
