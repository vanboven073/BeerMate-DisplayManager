// Package browser drives a managed Chromium over the Chrome DevTools Protocol
// for websites that cannot be rendered in a plain iframe.
//
// Two classes of website need this:
//   - sites that refuse framing (X-Frame-Options / frame-ancestors), and
//   - authenticated sites, whose login cookies live in a persistent Chromium
//     profile rather than in the player page.
//
// This replaces the old xdotool approach entirely. The managed instance runs on
// a virtual X display and streams screenshots to the player; a separate interactive
// instance on the real display is used only while an administrator logs in.
//
// SECURITY: the DevTools endpoint is bound to loopback only. It grants total
// control of the browser and every stored cookie, so it must never be reachable
// over Tailscale or any other interface. The configuration layer refuses to start
// with a non-loopback debug address.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// State summarises the manager's health for the dashboard.
type State string

const (
	// StateDisabled means browser control is turned off in configuration.
	StateDisabled State = "disabled"
	// StateStopped means Chromium is not running.
	StateStopped State = "stopped"
	// StateRunning means the capture instance is up.
	StateRunning State = "running"
	// StateInteractive means an interactive login instance is active.
	StateInteractive State = "interactive"
	// StateError means the last operation failed.
	StateError State = "error"
)

// Manager is the browser-control surface used by the API and workers.
type Manager interface {
	// Name identifies the implementation.
	Name() string
	// Status reports the current state and a non-sensitive detail string.
	Status() (State, string)
	// Capture returns a JPEG screenshot of the website loaded in the profile.
	Capture(ctx context.Context, profileID, targetURL string) ([]byte, error)
	// PrepareLogin opens the site interactively on the real display for manual
	// login. It returns immediately; the administrator finishes with FinishLogin.
	PrepareLogin(ctx context.Context, profileID, targetURL string) error
	// FinishLogin closes the interactive instance and persists the profile.
	FinishLogin(ctx context.Context, profileID string) error
	// Validate loads the target URL headlessly and reports whether it resolves
	// without redirecting to a login page.
	Validate(ctx context.Context, profileID, targetURL, loginPattern string) (bool, error)
	// ClearSession deletes a profile's stored cookies and storage.
	ClearSession(ctx context.Context, profileID string) error
	// Close shuts down all managed Chromium instances.
	Close() error
}

// ErrDisabled is returned by the disabled manager for any operation.
var ErrDisabled = errors.New("browser: managed browser control is disabled")

// safeProfilePath joins a profile id under root, refusing traversal.
//
// The profile id becomes a directory holding session cookies, so a crafted id
// must not be able to point the browser at, say, /etc.
func safeProfilePath(root, profileID string) (string, error) {
	if !validProfileID(profileID) {
		return "", fmt.Errorf("browser: invalid profile id %q", profileID)
	}
	full := filepath.Join(root, profileID)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", errors.New("browser: profile path escapes the profiles directory")
	}
	return full, nil
}

// validProfileID mirrors the store's rule: a single safe path segment.
func validProfileID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// looksLikeLoginRedirect reports whether landed indicates the session expired.
//
// Detection is deliberately conservative and layered: an explicit operator-
// supplied pattern wins, then a small set of well-known login path fragments.
// A false positive shows a branded fallback instead of a dashboard, which is
// recoverable; a false negative would leave a login form on the wall, which is
// the exact failure the session model exists to prevent, so the default set errs
// toward catching redirects.
func looksLikeLoginRedirect(target, landed, loginPattern string) bool {
	if landed == "" {
		return false
	}
	lu, err := url.Parse(landed)
	if err != nil {
		return false
	}

	if p := strings.TrimSpace(loginPattern); p != "" {
		if strings.Contains(strings.ToLower(landed), strings.ToLower(p)) {
			return true
		}
	}

	// If the host changed to a known identity provider, treat it as a redirect.
	tu, err := url.Parse(target)
	if err == nil && tu.Host != "" && lu.Host != "" && !sameSite(tu.Host, lu.Host) {
		if isIdentityHost(lu.Host) {
			return true
		}
	}

	path := strings.ToLower(lu.Path)
	for _, frag := range []string{
		"/login", "/signin", "/sign-in", "/auth", "/sso", "/oauth",
		"/account/login", "/session/new", "/accounts/login",
	} {
		if strings.Contains(path, frag) {
			return true
		}
	}
	// Common query markers of an auth handshake.
	q := lu.RawQuery
	for _, marker := range []string{"redirect_uri=", "returnurl=", "return_to=", "saml", "response_type="} {
		if strings.Contains(strings.ToLower(q), marker) {
			return true
		}
	}
	return false
}

func sameSite(a, b string) bool {
	return registrable(a) == registrable(b)
}

// registrable returns the last two labels of a host, a good-enough eTLD+1 for
// same-site comparison without shipping a public suffix list.
func registrable(host string) string {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func isIdentityHost(host string) bool {
	host = strings.ToLower(host)
	for _, idp := range []string{
		"login.microsoftonline.com", "accounts.google.com", "login.okta.com",
		"auth0.com", "okta.com", "onelogin.com", "pingidentity.com",
		"login.salesforce.com", "github.com/login", "gitlab.com/users/sign_in",
	} {
		if strings.Contains(host, idp) {
			return true
		}
	}
	return false
}

// New builds a Manager. When enabled is false it returns the disabled manager.
func New(cfg Config) Manager {
	if !cfg.Enabled {
		return disabledManager{}
	}
	return newCDPManager(cfg)
}

// Config configures the browser manager.
type Config struct {
	Enabled     bool
	DebugAddr   string // loopback host:port, validated by the config layer
	Binary      string
	ProfilesDir string
	XvfbDisplay string
	RealDisplay string
	Width       int
	Height      int
	// MaxCaptures bounds concurrent screenshot operations. A split-screen scene
	// with several managed websites asks the player to fetch each zone at once,
	// and an unbounded fan-out of navigate+screenshot cycles saturates the
	// Jetson's four A57 cores. Values below 1 are treated as 1.
	MaxCaptures int
	Logger      Logger
}

// Logger is the minimal logging surface, satisfied by *slog.Logger via an adapter.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

// ---- disabled implementation ---------------------------------------------

type disabledManager struct{}

func (disabledManager) Name() string            { return "disabled" }
func (disabledManager) Status() (State, string) { return StateDisabled, "browser control is off" }
func (disabledManager) Capture(context.Context, string, string) ([]byte, error) {
	return nil, ErrDisabled
}
func (disabledManager) PrepareLogin(context.Context, string, string) error { return ErrDisabled }
func (disabledManager) FinishLogin(context.Context, string) error          { return ErrDisabled }
func (disabledManager) Validate(context.Context, string, string, string) (bool, error) {
	return false, ErrDisabled
}
func (disabledManager) ClearSession(context.Context, string) error { return ErrDisabled }
func (disabledManager) Close() error                               { return nil }
