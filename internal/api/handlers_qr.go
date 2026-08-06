package api

import (
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// hexColorRe matches the strict hex forms the content validator also enforces, so
// a colour that reaches this endpoint from a validated QR zone always parses.
var hexColorRe = regexp.MustCompile(`^#?([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)

// maxQRData bounds the payload so a request cannot ask for an enormous code.
const maxQRData = 1200

// handleQR renders a QR code to PNG.
//
// Generating the code server-side keeps the payload out of the player's
// JavaScript and yields a cacheable image. The endpoint is served to the player
// and admin (via requireAnyViewer would be ideal, but QR content is not secret
// and the player needs it unauthenticated at render time); it is therefore left
// on the authenticated surface and also reachable with the player token.
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := q.Get("data")
	if strings.TrimSpace(data) == "" {
		writeError(w, http.StatusBadRequest, "the data parameter is required")
		return
	}
	if len(data) > maxQRData {
		writeError(w, http.StatusBadRequest, "QR content is too long")
		return
	}

	level := qrcode.Medium
	switch strings.ToUpper(q.Get("ec")) {
	case "L":
		level = qrcode.Low
	case "M":
		level = qrcode.Medium
	case "Q":
		level = qrcode.High
	case "H":
		level = qrcode.Highest
	}

	fg := parseHexColor(q.Get("fg"), color.RGBA{R: 0x0E, G: 0x18, B: 0x21, A: 0xFF})
	bg := parseHexColor(q.Get("bg"), color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})

	code, err := qrcode.New(data, level)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not encode the QR content")
		return
	}
	code.ForegroundColor = fg
	code.BackgroundColor = bg
	code.DisableBorder = false

	img := code.Image(512)

	w.Header().Set("Content-Type", "image/png")
	// The code is a pure function of the query, so it caches indefinitely.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(w, img); err != nil {
		s.deps.Log.Error("qr encode", "error", err)
	}
}

// parseHexColor parses #rgb / #rrggbb, falling back to def.
func parseHexColor(s string, def color.RGBA) color.RGBA {
	s = strings.TrimSpace(s)
	if !hexColorRe.MatchString(s) {
		return def
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	var r, g, b uint8
	for i, dst := range []*uint8{&r, &g, &b} {
		*dst = hexByte(s[i*2], s[i*2+1])
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xFF}
}

func hexByte(hi, lo byte) uint8 {
	return hexNibble(hi)<<4 | hexNibble(lo)
}

func hexNibble(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

var _ = time.Now
