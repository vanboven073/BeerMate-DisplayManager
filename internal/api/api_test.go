package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vanboven073/BeerMate-DisplayManager/internal/auth"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/browser"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/config"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/dbx"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/display"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/logging"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/media"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/realtime"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/schedule"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/secrets"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/social"
	"github.com/vanboven073/BeerMate-DisplayManager/internal/store"
)

// testPlayerToken is the per-start token the harness gives the server, standing in
// for the random one main generates.
const testPlayerToken = "test-player-token"

// harness is a fully wired server over a temporary database. The collaborators are
// real rather than mocked: the behaviour under test here is authorisation, and a
// mocked session store would test the mock.
type harness struct {
	t        *testing.T
	server   *Server
	handler  http.Handler
	users    *auth.UserStore
	sessions *auth.SessionStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	dir := t.TempDir()
	log := logging.Discard()

	db, err := dbx.Open(dbx.Options{Path: filepath.Join(dir, "api.db"), Logger: log})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.ConfigDir = dir
	cfg.BrowserEnabled = false
	cfg.DPMSDriver = "mock"
	loc, _ := time.LoadLocation("UTC")
	cfg.Location = loc
	cfg.Timezone = "UTC"

	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		t.Fatalf("box: %v", err)
	}

	hasher := auth.NewHasher(1)
	users := auth.NewUserStore(db.DB, hasher)
	sessions := auth.NewSessionStore(db.DB, cfg.SessionIdleTimeout, cfg.SessionMaxLifetime)

	deps := Deps{
		Config:    cfg,
		Log:       log,
		Users:     users,
		Sessions:  sessions,
		Throttle:  auth.NewThrottle(db.DB),
		Playlist:  store.NewPlaylistStore(db),
		Media:     media.NewStore(db, filepath.Join(dir, "uploads"), filepath.Join(dir, "thumbs"), media.Limits{Image: 1 << 20, Video: 1 << 20, PDF: 1 << 20}, log),
		Settings:  store.NewSettingsStore(db),
		Schedule:  store.NewScheduleStore(db),
		Emergency: store.NewEmergencyStore(db),
		Audit:     store.NewAuditStore(db),
		Player:    store.NewPlayerStore(db, time.Minute),
		Hub:       realtime.NewHub(8, log),
		Engine:    schedule.NewEngine(loc),
		Display:   display.New("mock", ":0", "xset", log),
		Websites:  store.NewWebsiteStore(db),
		Browser:   browser.New(browser.Config{Enabled: false, Logger: log}),
		Social:    social.NewService(db, box, 50, log),
		Backups:   store.NewBackupStore(db, filepath.Join(dir, "backups"), db.Path(), filepath.Join(dir, "uploads"), 5),
		HealthDB:  func(c context.Context) error { return db.PingContext(c) },
		StorageUsage: func() (int64, int64, error) {
			return 0, 1 << 40, nil
		},
	}

	srv := New(deps, testPlayerToken)
	h := &harness{t: t, server: srv, handler: srv.Handler(), users: users, sessions: sessions}
	t.Cleanup(deps.Hub.Close)
	return h
}

// login creates a user with the given role and returns a request decorator that
// presents that user's session and CSRF token.
func (h *harness) login(username, role string) func(*http.Request) {
	h.t.Helper()
	ctx := context.Background()

	var u auth.User
	var err error
	if role == auth.RoleAdmin {
		if need, _ := h.users.NeedsBootstrap(ctx); need {
			u, err = h.users.Bootstrap(ctx, username, username, "a-sufficiently-long-password")
		} else {
			u, err = h.users.Create(ctx, username, username, "a-sufficiently-long-password", role)
		}
	} else {
		// A non-admin cannot be the bootstrap user, so make sure an admin exists.
		if need, _ := h.users.NeedsBootstrap(ctx); need {
			if _, berr := h.users.Bootstrap(ctx, "root", "root", "a-sufficiently-long-password"); berr != nil {
				h.t.Fatalf("bootstrap: %v", berr)
			}
		}
		u, err = h.users.Create(ctx, username, username, "a-sufficiently-long-password", role)
	}
	if err != nil {
		h.t.Fatalf("create %s: %v", role, err)
	}

	token, sess, err := h.sessions.Create(ctx, u.ID, "127.0.0.1", "test")
	if err != nil {
		h.t.Fatalf("create session: %v", err)
	}
	csrf := auth.CSRFToken(sess)

	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
		r.Header.Set(auth.CSRFHeaderName, csrf)
	}
}

// do issues a request through the full middleware chain.
func (h *harness) do(method, target string, decorate ...func(*http.Request)) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "100.64.0.9:12345" // a tailnet peer, not loopback
	for _, d := range decorate {
		d(r)
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

// ---- role enforcement -----------------------------------------------------

// A backup archive is the entire database, including password hashes. Read access
// to it must not come with the read-only dashboard role.
func TestBackupDownloadRequiresAdmin(t *testing.T) {
	h := newHarness(t)

	viewer := h.login("viewer", auth.RoleViewer)
	if got := h.do(http.MethodGet, "/api/v1/backups/1/download", viewer).Code; got != http.StatusForbidden {
		t.Errorf("viewer download = %d, want %d", got, http.StatusForbidden)
	}

	editor := h.login("editor", auth.RoleEditor)
	if got := h.do(http.MethodGet, "/api/v1/backups/1/download", editor).Code; got != http.StatusForbidden {
		t.Errorf("editor download = %d, want %d", got, http.StatusForbidden)
	}

	// An admin gets past the authorisation gate; 404 here means "no such backup",
	// which is the next check and proves the role test was not what refused.
	admin := h.login("admin", auth.RoleAdmin)
	if got := h.do(http.MethodGet, "/api/v1/backups/1/download", admin).Code; got == http.StatusForbidden {
		t.Error("admin download was refused by the role check")
	}
}

func TestViewerCannotMutate(t *testing.T) {
	h := newHarness(t)
	viewer := h.login("viewer", auth.RoleViewer)

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/media"},
		{http.MethodDelete, "/api/v1/media/1"},
		{http.MethodPost, "/api/v1/websites"},
		{http.MethodDelete, "/api/v1/websites/1"},
		{http.MethodPost, "/api/v1/websites/1/prepare-login"},
		{http.MethodPost, "/api/v1/social/feeds"},
		{http.MethodDelete, "/api/v1/social/feeds/1"},
		{http.MethodPost, "/api/v1/backups"},
		{http.MethodDelete, "/api/v1/backups/1"},
		{http.MethodPost, "/api/v1/settings"},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, viewer).Code; got != http.StatusForbidden {
			t.Errorf("%s %s as viewer = %d, want %d", c.method, c.path, got, http.StatusForbidden)
		}
	}
}

func TestEditorCannotReachAdminOnlyRoutes(t *testing.T) {
	h := newHarness(t)
	editor := h.login("editor", auth.RoleEditor)

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/users"},
		{http.MethodGet, "/api/v1/audit"},
		{http.MethodPost, "/api/v1/playlist/rollback"},
		{http.MethodPost, "/api/v1/emergency/raise"},
		{http.MethodPost, "/api/v1/migrate/legacy"},
	}
	for _, c := range cases {
		if got := h.do(c.method, c.path, editor).Code; got != http.StatusForbidden {
			t.Errorf("%s %s as editor = %d, want %d", c.method, c.path, got, http.StatusForbidden)
		}
	}
}

// ---- CSRF -----------------------------------------------------------------

func TestStateChangingRequestsRequireCSRF(t *testing.T) {
	h := newHarness(t)
	admin := h.login("admin", auth.RoleAdmin)

	withoutCSRF := func(r *http.Request) {
		admin(r)
		r.Header.Del(auth.CSRFHeaderName)
	}
	if got := h.do(http.MethodPost, "/api/v1/playlist/publish", withoutCSRF).Code; got != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want %d", got, http.StatusForbidden)
	}

	wrongCSRF := func(r *http.Request) {
		admin(r)
		r.Header.Set(auth.CSRFHeaderName, "not-the-token")
	}
	if got := h.do(http.MethodPost, "/api/v1/playlist/publish", wrongCSRF).Code; got != http.StatusForbidden {
		t.Errorf("POST with a wrong CSRF token = %d, want %d", got, http.StatusForbidden)
	}

	// GET is not state-changing and must not require the token.
	readOnly := func(r *http.Request) {
		admin(r)
		r.Header.Del(auth.CSRFHeaderName)
	}
	if got := h.do(http.MethodGet, "/api/v1/playlist", readOnly).Code; got != http.StatusOK {
		t.Errorf("GET without CSRF = %d, want %d", got, http.StatusOK)
	}
}

// ---- authentication -------------------------------------------------------

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{
		"/api/v1/playlist",
		"/api/v1/media",
		"/api/v1/overview",
		"/api/v1/users",
		"/api/v1/backups",
		"/media/file/1",
	} {
		if got := h.do(http.MethodGet, path).Code; got != http.StatusUnauthorized {
			t.Errorf("GET %s unauthenticated = %d, want %d", path, got, http.StatusUnauthorized)
		}
	}
}

func TestHealthIsUnauthenticatedAndCarriesNoSecrets(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodGet, "/health")
	if w.Code != http.StatusOK {
		t.Fatalf("health = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, secret := range []string{testPlayerToken, "password", "token_hash", "csrf_secret"} {
		if contains(body, secret) {
			t.Errorf("health body leaks %q: %s", secret, body)
		}
	}
}

// ---- player token ---------------------------------------------------------

func TestPlayerEndpointsRequireTheToken(t *testing.T) {
	h := newHarness(t)

	if got := h.do(http.MethodGet, "/api/v1/player/state").Code; got != http.StatusUnauthorized {
		t.Errorf("player state without a token = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := h.do(http.MethodGet, "/api/v1/player/state?token=wrong").Code; got != http.StatusUnauthorized {
		t.Errorf("player state with a wrong token = %d, want %d", got, http.StatusUnauthorized)
	}

	withToken := func(r *http.Request) { r.Header.Set("X-BeerMate-Player", testPlayerToken) }
	if got := h.do(http.MethodGet, "/api/v1/player/state", withToken).Code; got != http.StatusOK {
		t.Errorf("player state with the token = %d, want %d", got, http.StatusOK)
	}
}

// The /player document is served without authentication, so it is the one place
// the per-start token could leak to the whole tailnet. Handing it to any caller
// that asks would make the token authenticate nothing: a tailnet peer could read
// it out of the HTML and then pull media and managed screenshots of authenticated
// websites.
func TestPlayerShellTokenInjection(t *testing.T) {
	h := newHarness(t)

	shellFor := func(remoteAddr string, decorate ...func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/player", nil)
		r.RemoteAddr = remoteAddr
		for _, d := range decorate {
			d(r)
		}
		w := httptest.NewRecorder()
		h.handler.ServeHTTP(w, r)
		return w
	}

	// The kiosk reaches the server over loopback and must get the token. This also
	// proves the compiled shell is embedded; without it the rest of the assertions
	// would pass vacuously against a "frontend not built" page.
	local := shellFor("127.0.0.1:54321")
	if !contains(local.Body.String(), testPlayerToken) {
		t.Skip("frontend bundle not embedded (run `make frontend`); token injection not exercised")
	}

	// An anonymous peer elsewhere on the tailnet must not.
	remote := shellFor("100.64.0.9:12345")
	if contains(remote.Body.String(), testPlayerToken) {
		t.Error("the player shell handed the player token to an unauthenticated tailnet peer")
	}

	// A signed-in operator previewing the player remotely may: they already hold
	// strictly more access than the token grants.
	admin := h.login("admin", auth.RoleAdmin)
	authed := shellFor("100.64.0.9:12345", admin)
	if !contains(authed.Body.String(), testPlayerToken) {
		t.Error("an authenticated operator was refused the player token")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
