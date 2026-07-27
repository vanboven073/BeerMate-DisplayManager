package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrPDFToolMissing is returned when pdftoppm is not installed.
var ErrPDFToolMissing = errors.New("media: pdftoppm not found; install poppler-utils to import PDFs")

// PDFConverter rasterises PDF pages into display-ready images.
//
// It shells out to pdftoppm (poppler-utils), which is a mature, widely-packaged
// tool — pure-Go PDF rasterisers are immature. Conversion happens once at import,
// never during playback, so the cost is paid up front and the player only ever
// shows pre-rendered PNGs.
type PDFConverter struct {
	store    *Store
	toolPath string
	maxPages int
	// timeout bounds the whole conversion so a malformed or enormous PDF cannot
	// pin a core indefinitely.
	timeout time.Duration
	// dpi controls render resolution; 150 is sharp on a 1080p panel without
	// producing needlessly large files on a 4 GB device.
	dpi int
}

// NewPDFConverter builds a converter.
func NewPDFConverter(store *Store, toolPath string, maxPages int) *PDFConverter {
	if toolPath == "" {
		toolPath = "pdftoppm"
	}
	if maxPages < 1 {
		maxPages = 50
	}
	return &PDFConverter{
		store:    store,
		toolPath: toolPath,
		maxPages: maxPages,
		timeout:  2 * time.Minute,
		dpi:      150,
	}
}

// Available reports whether the conversion tool is present.
func (c *PDFConverter) Available() bool {
	if _, err := exec.LookPath(c.toolPath); err == nil {
		return true
	}
	_, err := os.Stat(c.toolPath)
	return err == nil
}

// Convert renders the pages of an imported PDF into pdf_page media items.
//
// The parent PDF item must already be stored. Each page becomes an image child
// with parent_id set and page_number in order, so a scene can reference either
// the whole document (as a grouped sequence) or an individual page.
func (c *PDFConverter) Convert(ctx context.Context, pdfItem Item, actor string) ([]Item, error) {
	if pdfItem.Kind != KindPDF {
		return nil, fmt.Errorf("media: item %d is not a PDF", pdfItem.ID)
	}
	if !c.Available() {
		return nil, ErrPDFToolMissing
	}

	pdfPath, err := c.store.Path(pdfItem.StoredName)
	if err != nil {
		return nil, err
	}

	// Render into a temp directory, then import each page. A dedicated dir keeps
	// pdftoppm's numbered output isolated and easy to clean up.
	tmpDir, err := os.MkdirTemp("", "bm-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("media: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	outPrefix := filepath.Join(tmpDir, "page")
	// -png: PNG output. -r: DPI. -l: last page cap, so an enormous PDF cannot
	// render thousands of pages. -cropbox: render the visible page area.
	args := []string{
		"-png",
		"-r", strconv.Itoa(c.dpi),
		"-l", strconv.Itoa(c.maxPages),
		"-cropbox",
		pdfPath,
		outPrefix,
	}
	cmd := exec.CommandContext(runCtx, c.toolPath, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("media: PDF conversion timed out after %s", c.timeout)
		}
		return nil, fmt.Errorf("media: pdftoppm failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	// Collect the rendered pages in numeric order.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, err
	}
	var pages []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".png") {
			pages = append(pages, e.Name())
		}
	}
	if len(pages) == 0 {
		return nil, errors.New("media: PDF produced no pages")
	}
	sort.Strings(pages)

	var out []Item
	for i, name := range pages {
		if i >= c.maxPages {
			break
		}
		f, err := os.Open(filepath.Join(tmpDir, name))
		if err != nil {
			continue
		}
		// Reuse the normal image import path so each page gets a thumbnail and
		// dimensions, then re-parent it under the PDF.
		page, saveErr := c.store.Save(runCtx, f, fmt.Sprintf("%s p%d.png", pdfItem.OriginalName, i+1), actor)
		f.Close()
		if saveErr != nil {
			continue
		}
		if err := c.store.markAsPDFPage(runCtx, page.ID, pdfItem.ID, i+1); err != nil {
			continue
		}
		page.Kind = KindPDFPage
		page.ParentID = &pdfItem.ID
		page.PageNumber = i + 1
		out = append(out, page)
	}
	if len(out) == 0 {
		return nil, errors.New("media: no PDF pages could be imported")
	}
	return out, nil
}

// markAsPDFPage re-parents an imported image as a page of a PDF.
func (s *Store) markAsPDFPage(ctx context.Context, imageID, parentID int64, pageNumber int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media SET kind = 'pdf_page', parent_id = ?, page_number = ?, updated_at = ?
		  WHERE id = ?`,
		parentID, pageNumber, rfc3339(s.now()), imageID)
	return err
}
