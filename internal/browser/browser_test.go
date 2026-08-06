package browser

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The DevTools port grants full control of the browser and every stored cookie.
// A profile id that could escape the profiles directory would let a crafted
// website point Chromium's user-data-dir at an arbitrary path.
func TestSafeProfilePathRejectsTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	bad := []string{
		"../secret", "..", ".", "a/b", "a\\b", "with space", "with/slash",
		"", "profile;rm", "../../etc",
	}
	for _, id := range bad {
		if _, err := safeProfilePath(root, id); err == nil {
			t.Errorf("safeProfilePath accepted unsafe id %q", id)
		}
	}
	for _, id := range []string{"default", "dashboard-1", "beermate_cloud", "abc123"} {
		if _, err := safeProfilePath(root, id); err != nil {
			t.Errorf("safeProfilePath rejected a legitimate id %q: %v", id, err)
		}
	}
}

func TestValidProfileID(t *testing.T) {
	if validProfileID("") || validProfileID("has space") || validProfileID("dot.dot") {
		t.Error("invalid profile ids were accepted")
	}
	if !validProfileID("default") || !validProfileID("a-b_c1") {
		t.Error("valid profile ids were rejected")
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if validProfileID(string(long)) {
		t.Error("an over-long profile id was accepted")
	}
}

// A false negative here leaves a login form on the wall, which is exactly the
// failure the session model exists to prevent, so the detector must err toward
// catching redirects.
func TestLoginRedirectDetection(t *testing.T) {
	target := "https://dashboard.beermatecloud.eu/dashboard/44"

	redirects := []struct {
		landed  string
		pattern string
	}{
		{"https://dashboard.beermatecloud.eu/login", ""},
		{"https://dashboard.beermatecloud.eu/account/login?next=/dashboard", ""},
		{"https://login.microsoftonline.com/common/oauth2/authorize", ""},
		{"https://accounts.google.com/o/oauth2/v2/auth?response_type=code", ""},
		{"https://dashboard.beermatecloud.eu/sso/start?returnUrl=%2Fdashboard", ""},
		{"https://idp.example.com/saml2/sso", ""},
		{"https://dashboard.beermatecloud.eu/portal", "/portal"}, // operator pattern
	}
	for _, r := range redirects {
		if !looksLikeLoginRedirect(target, r.landed, r.pattern) {
			t.Errorf("failed to detect login redirect to %q (pattern %q)", r.landed, r.pattern)
		}
	}

	ok := []string{
		"https://dashboard.beermatecloud.eu/dashboard/44",
		"https://dashboard.beermatecloud.eu/dashboard/44?refresh=1",
		"https://dashboard.beermatecloud.eu/", // same site, no login markers
	}
	for _, landed := range ok {
		if looksLikeLoginRedirect(target, landed, "") {
			t.Errorf("false positive: %q flagged as a login redirect", landed)
		}
	}
}

func TestRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"dashboard.beermatecloud.eu": "beermatecloud.eu",
		"beermatecloud.eu":           "beermatecloud.eu",
		"a.b.c.example.com":          "example.com",
		"localhost":                  "localhost",
		"host:8080":                  "host",
	}
	for in, want := range cases {
		if got := registrable(in); got != want {
			t.Errorf("registrable(%q) = %q, want %q", in, got, want)
		}
	}
}

// The disabled manager must be safe to call: browser control off is a valid
// production configuration when only embeddable sites are used.
func TestDisabledManager(t *testing.T) {
	m := New(Config{Enabled: false})
	if m.Name() != "disabled" {
		t.Errorf("name = %q, want disabled", m.Name())
	}
	state, _ := m.Status()
	if state != StateDisabled {
		t.Errorf("state = %q, want disabled", state)
	}
	ctx := context.Background()
	if _, err := m.Capture(ctx, "default", "https://x"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Capture = %v, want ErrDisabled", err)
	}
	if err := m.PrepareLogin(ctx, "default", "https://x"); !errors.Is(err, ErrDisabled) {
		t.Errorf("PrepareLogin = %v, want ErrDisabled", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close on disabled manager: %v", err)
	}
}

func TestRedactErrStripsQueryString(t *testing.T) {
	// A navigated URL can carry a one-time login token in its query string, which
	// must never reach storage or logs.
	err := errors.New("navigate failed: https://site/callback?token=SECRET123&code=abc")
	got := redactErr(err)
	if containsSub(got, "SECRET123") || containsSub(got, "token=") {
		t.Errorf("redactErr leaked a token: %q", got)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
