package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
)

// ---- signature detection -------------------------------------------------

func pad(b []byte) []byte {
	out := make([]byte, SniffLen)
	copy(out, b)
	return out
}

func TestDetectSupportedFormats(t *testing.T) {
	tests := []struct {
		name     string
		head     []byte
		wantKind string
		wantMIME string
	}{
		{"jpeg", pad([]byte{0xFF, 0xD8, 0xFF, 0xE0}), KindImage, "image/jpeg"},
		{"png", pad([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}), KindImage, "image/png"},
		{"webp", pad(append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...)), KindImage, "image/webp"},
		{"gif87", pad([]byte("GIF87a...")), KindImage, "image/gif"},
		{"gif89", pad([]byte("GIF89a...")), KindImage, "image/gif"},
		{"pdf", pad([]byte("%PDF-1.7")), KindPDF, "application/pdf"},
		{"mp4", pad([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}), KindVideo, "video/mp4"},
		{"webm", pad([]byte{0x1A, 0x45, 0xDF, 0xA3}), KindVideo, "video/webm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Detect(tt.head)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if d.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", d.Kind, tt.wantKind)
			}
			if d.MIME != tt.wantMIME {
				t.Errorf("mime = %q, want %q", d.MIME, tt.wantMIME)
			}
		})
	}
}

// SVG is an XML document that can carry <script> and external references.
// Serving one back to the player would be stored XSS, so it must stay rejected
// until a real sanitiser exists.
func TestDetectRejectsSVG(t *testing.T) {
	variants := [][]byte{
		pad([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)),
		pad([]byte(`   <SVG viewBox="0 0 1 1"></SVG>`)),
		pad([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)),
		pad(append([]byte{0xEF, 0xBB, 0xBF}, []byte(`<svg width="1"/>`)...)),
		pad([]byte(`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN"><svg/>`)),
	}
	for i, v := range variants {
		if _, err := Detect(v); !errors.Is(err, ErrUnsupported) {
			t.Errorf("SVG variant %d was accepted: %v", i, err)
		}
	}
}

func TestDetectRejectsDangerousTypes(t *testing.T) {
	tests := map[string][]byte{
		"elf binary":   pad([]byte{0x7F, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0}),
		"windows exe":  pad([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00")),
		"shell script": pad([]byte("#!/bin/sh\nrm -rf /\n\n\n\n")),
		"zip archive":  pad([]byte("PK\x03\x04\x14\x00\x00\x00\x08\x00\x00\x00")),
		"html":         pad([]byte("<!DOCTYPE html><html><body>hi</body></html>")),
		"plain text":   pad([]byte("just some text, definitely not an image")),
	}
	for name, head := range tests {
		if _, err := Detect(head); err == nil {
			t.Errorf("%s was accepted as valid media", name)
		}
	}
}

// A forged extension must not influence the decision: the signature decides.
func TestDetectIgnoresClaimedExtension(t *testing.T) {
	shellScript := pad([]byte("#!/bin/bash\necho pwned\n\n\n\n"))
	if _, err := Detect(shellScript); err == nil {
		t.Fatal("a shell script was accepted regardless of its name")
	}

	// Conversely, a real PNG named .txt is still a PNG.
	d, err := Detect(pad([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}))
	if err != nil {
		t.Fatal(err)
	}
	if d.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", d.MIME)
	}
	if ExtensionMatches("payload.txt", d) {
		t.Error("ExtensionMatches should report a mismatch for a PNG named .txt")
	}
}

func TestDetectRejectsTruncatedInput(t *testing.T) {
	if _, err := Detect([]byte{0xFF, 0xD8}); err == nil {
		t.Error("a two-byte file was accepted")
	}
	if _, err := Detect(nil); err == nil {
		t.Error("an empty file was accepted")
	}
}

// "ftyp" appearing at offset 4 with an absurd box size is not an MP4.
func TestISOBMFFRejectsImplausibleBoxSize(t *testing.T) {
	head := pad([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	if _, err := Detect(head); err == nil {
		t.Error("a file with an implausible ISO-BMFF box size was accepted as MP4")
	}
}

func TestUnknownMP4BrandWarns(t *testing.T) {
	head := pad([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'X', 'Y', 'Z', 'W'})
	d, err := Detect(head)
	if err != nil {
		t.Fatal(err)
	}
	if d.Warning == "" {
		t.Error("an unrecognised MP4 brand should warn about codec compatibility")
	}
}

// ---- path safety ---------------------------------------------------------

// Stored names come from the database, but a corrupted row must still not be
// able to escape the media directory.
func TestSafeJoinRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	bad := []string{
		"../../../etc/passwd",
		"..\\..\\windows\\system32\\config\\sam",
		"sub/dir/file.png",
		"sub\\file.png",
		"..",
		".",
		"",
		"a/../../b.png",
	}
	for _, name := range bad {
		if _, err := safeJoin(dir, name); err == nil {
			t.Errorf("safeJoin accepted %q", name)
		}
	}
	if _, err := safeJoin(dir, "abc123.png"); err != nil {
		t.Errorf("safeJoin rejected a legitimate name: %v", err)
	}
}

func TestSanitiseDisplayName(t *testing.T) {
	cases := map[string]string{
		"photo.png":                 "photo.png",
		"../../etc/passwd":          "passwd",
		`C:\Users\bram\roadmap.png`: "roadmap.png",
		"with\x00null.png":          "withnull.png",
		// Control characters, tab included, are stripped: the name is echoed into
		// the admin UI and should not carry layout-breaking whitespace.
		"tab\there.png": "tabhere.png",
		"..":            "",
	}
	for in, want := range cases {
		if got := sanitiseDisplayName(in); got != want {
			t.Errorf("sanitiseDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 400) + ".png"
	if got := sanitiseDisplayName(long); len(got) > 200 {
		t.Errorf("long name not truncated: %d chars", len(got))
	}
}

// ---- store ---------------------------------------------------------------

func newMediaStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	up := filepath.Join(root, "uploads")
	th := filepath.Join(root, "thumbs")

	db, err := dbx.Open(dbx.Options{
		Path: filepath.Join(root, "m.db"), Logger: logging.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	s := NewStore(db, up, th, Limits{Image: 1 << 20, Video: 4 << 20, PDF: 1 << 20}, logging.Discard())
	return s, up, th
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x14, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaveImageGeneratesThumbnailAndMetadata(t *testing.T) {
	s, up, th := newMediaStore(t)
	ctx := context.Background()

	data := makePNG(t, 800, 600)
	it, err := s.Save(ctx, bytes.NewReader(data), "roadmap.png", "bram")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if it.Kind != KindImage || it.MIME != "image/png" {
		t.Errorf("kind/mime = %s/%s", it.Kind, it.MIME)
	}
	if it.Width != 800 || it.Height != 600 {
		t.Errorf("dimensions = %dx%d, want 800x600", it.Width, it.Height)
	}
	if it.Bytes != int64(len(data)) {
		t.Errorf("bytes = %d, want %d", it.Bytes, len(data))
	}
	if it.SHA256 == "" {
		t.Error("sha256 not recorded")
	}
	if !it.HasThumb {
		t.Error("no thumbnail generated")
	}

	// The stored name must be generated, never the client's.
	if strings.Contains(it.StoredName, "roadmap") {
		t.Errorf("stored name %q derives from the client filename", it.StoredName)
	}
	if it.OriginalName != "roadmap.png" {
		t.Errorf("original name = %q, want it kept as metadata", it.OriginalName)
	}

	if _, err := os.Stat(filepath.Join(up, it.StoredName)); err != nil {
		t.Errorf("uploaded file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(th, it.ThumbName)); err != nil {
		t.Errorf("thumbnail missing: %v", err)
	}
}

func TestThumbnailIsBoundedAndPreservesAspect(t *testing.T) {
	s, _, th := newMediaStore(t)
	ctx := context.Background()

	it, err := s.Save(ctx, bytes.NewReader(makePNG(t, 1600, 400)), "wide.png", "bram")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(th, it.ThumbName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > thumbMaxDim || cfg.Height > thumbMaxDim {
		t.Errorf("thumbnail %dx%d exceeds the %dpx cap", cfg.Width, cfg.Height, thumbMaxDim)
	}
	// 1600x400 is 4:1; the thumbnail must stay 4:1.
	got := float64(cfg.Width) / float64(cfg.Height)
	if got < 3.8 || got > 4.2 {
		t.Errorf("thumbnail aspect ratio = %.2f, want ~4.0", got)
	}
}

// Upscaling a tiny image only wastes disk.
func TestThumbnailNeverUpscales(t *testing.T) {
	s, _, th := newMediaStore(t)
	it, err := s.Save(context.Background(), bytes.NewReader(makePNG(t, 40, 30)), "tiny.png", "bram")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(filepath.Join(th, it.ThumbName))
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 40 || cfg.Height > 30 {
		t.Errorf("thumbnail %dx%d upscaled a 40x30 source", cfg.Width, cfg.Height)
	}
}

func TestSaveRejectsOversizeFile(t *testing.T) {
	s, up, _ := newMediaStore(t)
	ctx := context.Background()

	// A valid JPEG header followed by enough padding to exceed the 1 MiB cap.
	big := append(makeJPEG(t, 8, 8), bytes.Repeat([]byte{0x00}, 2<<20)...)
	_, err := s.Save(ctx, bytes.NewReader(big), "huge.jpg", "bram")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("save oversize = %v, want ErrTooLarge", err)
	}

	// The partial file must not be left behind.
	entries, _ := os.ReadDir(up)
	if len(entries) != 0 {
		t.Errorf("%d partial file(s) left on disk after a rejected upload", len(entries))
	}
}

func TestSaveRejectsUnsupportedContent(t *testing.T) {
	s, up, _ := newMediaStore(t)
	payload := []byte("<svg xmlns='http://www.w3.org/2000/svg'><script>alert(1)</script></svg>")
	if _, err := s.Save(context.Background(), bytes.NewReader(payload), "logo.svg", "bram"); err == nil {
		t.Fatal("an SVG upload was accepted")
	}
	entries, _ := os.ReadDir(up)
	if len(entries) != 0 {
		t.Errorf("%d file(s) written for a rejected upload", len(entries))
	}
}

func TestSaveWarnsOnExtensionMismatch(t *testing.T) {
	s, _, _ := newMediaStore(t)
	it, err := s.Save(context.Background(), bytes.NewReader(makePNG(t, 20, 20)), "actually.jpg", "bram")
	if err != nil {
		t.Fatal(err)
	}
	if it.Warning == "" {
		t.Error("a PNG uploaded as .jpg should carry a warning")
	}
	if it.MIME != "image/png" {
		t.Errorf("mime = %q; the detected type must win over the claimed extension", it.MIME)
	}
}

func TestDeleteRemovesFilesAndRow(t *testing.T) {
	s, up, th := newMediaStore(t)
	ctx := context.Background()

	it, err := s.Save(ctx, bytes.NewReader(makePNG(t, 100, 100)), "x.png", "bram")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, it.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, it.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(up, it.StoredName)); !os.IsNotExist(err) {
		t.Error("upload file still present after delete")
	}
	if _, err := os.Stat(filepath.Join(th, it.ThumbName)); !os.IsNotExist(err) {
		t.Error("thumbnail still present after delete")
	}
}

func TestUsageReporting(t *testing.T) {
	s, _, _ := newMediaStore(t)
	ctx := context.Background()
	var total int64
	for i := 0; i < 3; i++ {
		data := makePNG(t, 50+i*10, 50)
		it, err := s.Save(ctx, bytes.NewReader(data), "x.png", "bram")
		if err != nil {
			t.Fatal(err)
		}
		total += it.Bytes
	}
	bytesUsed, count, err := s.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if bytesUsed != total {
		t.Errorf("bytes = %d, want %d", bytesUsed, total)
	}
}

// Orphan cleanup must not delete a file an in-flight upload just wrote.
func TestCleanOrphansRespectsMinAge(t *testing.T) {
	s, up, _ := newMediaStore(t)
	ctx := context.Background()

	if err := os.MkdirAll(up, 0o750); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(up, "orphan.png")
	if err := os.WriteFile(orphan, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	// A file written moments ago must survive: it may belong to an upload whose
	// transaction has not committed yet.
	removed, err := s.CleanOrphans(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d recent file(s); an in-flight upload could be deleted", removed)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("recent orphan was deleted: %v", err)
	}

	// Age the file past the threshold; now it is genuinely abandoned.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	removed, err = s.CleanOrphans(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d orphans, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("aged orphan was not removed")
	}
}

func TestCleanOrphansKeepsReferencedFiles(t *testing.T) {
	s, _, _ := newMediaStore(t)
	ctx := context.Background()

	it, err := s.Save(ctx, bytes.NewReader(makePNG(t, 60, 60)), "keep.png", "bram")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanOrphans(ctx, 1); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Path(it.StoredName)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("a file with a database row was deleted as an orphan: %v", err)
	}
}

func TestListAndRename(t *testing.T) {
	s, _, _ := newMediaStore(t)
	ctx := context.Background()

	it, err := s.Save(ctx, bytes.NewReader(makePNG(t, 30, 30)), "original.png", "bram")
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx, KindImage, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("listed %d items, want 1", len(items))
	}

	if err := s.Rename(ctx, it.ID, "../../etc/passwd"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, it.ID)
	if strings.Contains(got.OriginalName, "/") || strings.Contains(got.OriginalName, "..") {
		t.Errorf("rename stored a path-like name: %q", got.OriginalName)
	}
}
