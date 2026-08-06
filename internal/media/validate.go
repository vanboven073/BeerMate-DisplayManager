// Package media handles uploads, validation, thumbnails and PDF import.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Kind classifies stored media.
const (
	KindImage   = "image"
	KindVideo   = "video"
	KindPDF     = "pdf"
	KindPDFPage = "pdf_page"
)

// ErrUnsupported is returned for a file we will not accept.
var ErrUnsupported = errors.New("media: unsupported file type")

// Detected describes a sniffed file.
type Detected struct {
	Kind      string
	MIME      string
	Extension string
	// Warning carries a non-fatal caveat, e.g. a codec we cannot verify.
	Warning string
}

// sniffLen is how many bytes we read to identify a file. 32 covers every
// signature below; ISO-BMFF (MP4) needs the most at offset 4..12.
const sniffLen = 64

// Detect identifies a file from its leading bytes.
//
// The magic-byte signature is authoritative. A client-supplied filename and
// Content-Type are trivially forged, so accepting either as proof of type would
// let an attacker store an HTML or script payload under an image extension and
// have the player fetch it back with an image MIME type.
func Detect(head []byte) (Detected, error) {
	if len(head) < 12 {
		return Detected{}, fmt.Errorf("%w: file is too short to identify", ErrUnsupported)
	}

	switch {
	// ---- images ----------------------------------------------------------
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return Detected{Kind: KindImage, MIME: "image/jpeg", Extension: ".jpg"}, nil

	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return Detected{Kind: KindImage, MIME: "image/png", Extension: ".png"}, nil

	case bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 &&
		bytes.Equal(head[8:12], []byte("WEBP")):
		// Chromium 97 decodes WebP fine (support landed in Chrome 32); the
		// caveat is only that some encoders emit animated WebP, which we do not
		// resize. Flagged rather than rejected.
		return Detected{
			Kind: KindImage, MIME: "image/webp", Extension: ".webp",
			Warning: "WebP is supported by Chromium 97; animated WebP is displayed but not resized",
		}, nil

	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return Detected{
			Kind: KindImage, MIME: "image/gif", Extension: ".gif",
			Warning: "animated GIF loops continuously and can keep the CPU busy on a Jetson Nano",
		}, nil

	// ---- documents -------------------------------------------------------
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return Detected{Kind: KindPDF, MIME: "application/pdf", Extension: ".pdf"}, nil

	// ---- video -----------------------------------------------------------
	case isISOBMFF(head):
		brand := string(head[8:12])
		d := Detected{Kind: KindVideo, MIME: "video/mp4", Extension: ".mp4"}
		// Only the container is verifiable from the header; the codec inside is
		// not. H.264 is the only codec with reliable hardware decode on a Jetson
		// Nano running Chromium 97, so anything unusual is flagged for the
		// operator rather than silently accepted.
		switch brand {
		case "isom", "iso2", "mp41", "mp42", "avc1", "M4V ", "dash":
		default:
			d.Warning = fmt.Sprintf("unrecognised MP4 brand %q; if playback fails, "+
				"re-encode as H.264 baseline or main profile", strings.TrimSpace(brand))
		}
		return d, nil

	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return Detected{
			Kind: KindVideo, MIME: "video/webm", Extension: ".webm",
			Warning: "WebM/Matroska playback on Chromium 97 depends on the codec inside " +
				"(VP8/VP9 usually work, AV1 does not decode in hardware on a Jetson Nano); " +
				"verify on the device before relying on it",
		}, nil

	// ---- explicitly rejected --------------------------------------------
	case looksLikeSVG(head):
		// SVG is an XML document that can carry <script>, external references and
		// foreignObject. Serving it back to the player would be stored XSS. It
		// stays rejected until a real sanitiser is in place.
		return Detected{}, fmt.Errorf("%w: SVG uploads are not accepted because an SVG can "+
			"contain scripts; export as PNG or WebP instead", ErrUnsupported)

	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return Detected{}, fmt.Errorf("%w: archives (zip, docx, pptx) are not accepted; "+
			"export the slides as PDF or images", ErrUnsupported)

	case bytes.HasPrefix(head, []byte{0x7F, 'E', 'L', 'F'}),
		bytes.HasPrefix(head, []byte("MZ")),
		bytes.HasPrefix(head, []byte("#!")):
		return Detected{}, fmt.Errorf("%w: executable files are never accepted", ErrUnsupported)
	}

	return Detected{}, fmt.Errorf("%w: the file's contents do not match any supported "+
		"image, video or PDF format", ErrUnsupported)
}

// isISOBMFF reports whether the header looks like an ISO base media file (MP4).
//
// Layout: [4-byte big-endian box size][4-byte "ftyp"][4-byte brand].
func isISOBMFF(head []byte) bool {
	if len(head) < 12 {
		return false
	}
	if !bytes.Equal(head[4:8], []byte("ftyp")) {
		return false
	}
	// A sane box size guards against a file that merely happens to contain
	// "ftyp" at offset 4.
	size := binary.BigEndian.Uint32(head[0:4])
	return size >= 8 && size <= 1<<20
}

// looksLikeSVG detects an SVG document, allowing for a BOM, leading whitespace
// and an XML declaration or DOCTYPE before the root element.
func looksLikeSVG(head []byte) bool {
	s := head
	s = bytes.TrimPrefix(s, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	s = bytes.TrimLeft(s, " \t\r\n")
	lower := bytes.ToLower(s)
	if bytes.HasPrefix(lower, []byte("<svg")) {
		return true
	}
	if bytes.HasPrefix(lower, []byte("<?xml")) || bytes.HasPrefix(lower, []byte("<!doctype")) {
		return bytes.Contains(lower, []byte("<svg")) || bytes.Contains(lower, []byte("svg\""))
	}
	return false
}

// ExtensionMatches reports whether a filename's extension agrees with the
// detected type. A mismatch is reported to the operator but is not fatal on its
// own: the signature has already decided what the file actually is.
func ExtensionMatches(filename string, d Detected) bool {
	i := strings.LastIndex(filename, ".")
	if i < 0 {
		return false
	}
	ext := strings.ToLower(filename[i:])
	switch d.MIME {
	case "image/jpeg":
		return ext == ".jpg" || ext == ".jpeg"
	case "image/png":
		return ext == ".png"
	case "image/webp":
		return ext == ".webp"
	case "image/gif":
		return ext == ".gif"
	case "application/pdf":
		return ext == ".pdf"
	case "video/mp4":
		return ext == ".mp4" || ext == ".m4v"
	case "video/webm":
		return ext == ".webm" || ext == ".mkv"
	}
	return false
}

// SniffLen is the number of leading bytes callers should read for Detect.
const SniffLen = sniffLen

// MaxBytesFor returns the configured size cap for a detected kind.
func MaxBytesFor(d Detected, maxImage, maxVideo, maxPDF int64) int64 {
	switch d.Kind {
	case KindImage:
		return maxImage
	case KindVideo:
		return maxVideo
	case KindPDF:
		return maxPDF
	}
	return maxImage
}
