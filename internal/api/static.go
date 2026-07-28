package api

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/auth"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/web"
)

// handleStatic serves the embedded SPA for /admin and /player.
//
// Both routes are served by one bundle: they share the brand tokens, fonts and
// most components, so a single build keeps the Jetson from downloading two
// copies of the same CSS and font files.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	assets, err := web.Assets()
	if err != nil {
		if errors.Is(err, web.ErrNotBuilt) {
			s.serveUnbuiltNotice(w, r)
			return
		}
		writeError(w, http.StatusInternalServerError, "frontend assets unavailable")
		return
	}

	upath := path.Clean("/" + r.URL.Path)

	switch {
	case upath == "/":
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	case upath == "/admin" || upath == "/player":
		// Canonical routes fall through to the SPA shell below.
	case strings.HasPrefix(upath, "/assets/"),
		strings.HasPrefix(upath, "/brand/"),
		upath == "/favicon.ico",
		upath == "/manifest.webmanifest":
		s.serveAsset(w, r, assets, strings.TrimPrefix(upath, "/"))
		return
	case strings.HasPrefix(upath, "/admin/"), strings.HasPrefix(upath, "/player/"):
		// Client-side routes: serve the shell and let the SPA route.
	default:
		// Try the path as a real asset before giving up, so anything the build
		// emits at the root still resolves.
		name := strings.TrimPrefix(upath, "/")
		if name != "" {
			if _, statErr := fs.Stat(assets, name); statErr == nil {
				s.serveAsset(w, r, assets, name)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	s.serveSPAShell(w, r, assets, strings.HasPrefix(upath, "/player"))
}

// serveAsset serves a hashed build artefact.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, assets fs.FS, name string) {
	f, err := assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Vite emits content-hashed filenames under /assets, so those are immutable.
	// Brand assets and the favicon keep stable names, so they get a short cache
	// that still lets a logo change appear without a hard refresh.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if ct := contentTypeFor(name); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, name, info.ModTime(), rs)
}

// serveSPAShell serves index.html with the player token injected when needed.
//
// The player token is embedded in the player document rather than issued over
// the API, because the player runs unattended with no login. Injecting it into
// the admin shell as well would hand it to every browser on the tailnet.
//
// The document itself is unauthenticated, so the token is only written into it
// for callers that are already entitled to it: the local Chromium kiosk, which
// reaches the server over loopback, or a signed-in operator previewing the player
// remotely. Without that restriction the token authenticates nothing at all —
// anyone on the tailnet could fetch /player, read it out of the HTML, and use it
// to pull media and managed screenshots of authenticated websites.
func (s *Server) serveSPAShell(w http.ResponseWriter, r *http.Request, assets fs.FS, isPlayer bool) {
	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "frontend shell missing")
		return
	}
	html := string(raw)

	token := ""
	if isPlayer && s.mayReceivePlayerToken(r) {
		token = s.playerToken
	}
	// A single placeholder the build emits; replaced per request.
	html = strings.Replace(html,
		"__BEERMATE_BOOTSTRAP__",
		`{"player_token":`+jsonString(token)+`,"mode":`+jsonString(modeFor(isPlayer))+`}`,
		1)

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The shell carries a per-request token, so it must never be cached.
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func modeFor(isPlayer bool) string {
	if isPlayer {
		return "player"
	}
	return "admin"
}

// mayReceivePlayerToken decides whether this request is entitled to the player
// token embedded in the player shell.
//
// Loopback covers the real player: run-player.sh points Chromium at
// http://127.0.0.1:8080/player on the Jetson itself. An authenticated session
// covers an operator opening the player remotely to check what is on the wall;
// they already hold strictly more access than the token grants, so handing it over
// gives away nothing new.
func (s *Server) mayReceivePlayerToken(r *http.Request) bool {
	if isLoopbackRequest(r) {
		return true
	}
	_, _, err := s.deps.Sessions.Lookup(r.Context(), auth.SessionTokenFromRequest(r))
	return err == nil
}

// isLoopbackRequest reports whether the peer address is on the loopback
// interface. RemoteAddr is the kernel-reported peer and cannot be spoofed by a
// header, so proxy headers are deliberately not consulted here.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// jsonString renders a Go string as a JSON string literal.
//
// Hand-rolled rather than using encoding/json because the result is interpolated
// into an HTML document: `<` and `/` are escaped so a value can never terminate
// the surrounding <script> element.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '<':
			b.WriteString(`<`)
		case '>':
			b.WriteString(`>`)
		case '&':
			b.WriteString(`&`)
		case '/':
			b.WriteString(`\/`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[(r>>4)&0xF])
				b.WriteByte(hex[r&0xF])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(name, ".webmanifest"), strings.HasSuffix(name, ".json"):
		return "application/json; charset=utf-8"
	}
	return ""
}

// serveUnbuiltNotice explains the missing build rather than returning a bare 500.
//
// This is a development-time state: the production Makefile always builds the
// frontend before the binary.
func (s *Server) serveUnbuiltNotice(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusServiceUnavailable, "frontend not built")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html>
<meta charset="utf-8">
<title>BeerMate Display Manager</title>
<style>
  body{font-family:system-ui,sans-serif;background:#0E1821;color:#F6F1EF;
       display:grid;place-items:center;height:100vh;margin:0;text-align:center}
  code{background:rgba(246,241,239,.1);padding:.2em .5em;border-radius:6px}
  .dot{color:#E86514}
</style>
<div>
  <h1>BeerMate<span class="dot">.</span> Display Manager</h1>
  <p>The frontend has not been compiled into this binary.</p>
  <p>Run <code>make frontend</code> then rebuild, or <code>make dev</code> for a
     development server.</p>
</div>`))
}
