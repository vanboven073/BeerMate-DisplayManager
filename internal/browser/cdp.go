package browser

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// cdpManager drives Chromium over the DevTools Protocol.
//
// It manages two Chromium roles that never run at the same time for one profile:
// a headless capture instance on the virtual display, and an interactive instance
// on the real display used only during manual login. Serialising them avoids two
// processes writing the same profile directory, which corrupts the cookie store.
type cdpManager struct {
	cfg Config
	log Logger

	mu          sync.Mutex
	state       State
	detail      string
	capture     *chromeInstance
	interactive *chromeInstance
	// lastShot caches the previous frame per profile so a capture failure can
	// return the last good image instead of a blank zone.
	lastShot map[string][]byte
}

func newCDPManager(cfg Config) *cdpManager {
	return &cdpManager{
		cfg:      cfg,
		log:      cfg.Logger,
		state:    StateStopped,
		lastShot: map[string][]byte{},
	}
}

func (m *cdpManager) Name() string { return "cdp" }

func (m *cdpManager) Status() (State, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, m.detail
}

func (m *cdpManager) setState(s State, detail string) {
	m.mu.Lock()
	m.state, m.detail = s, detail
	m.mu.Unlock()
}

// chromeInstance is a launched Chromium process plus its DevTools endpoint.
type chromeInstance struct {
	cmd       *exec.Cmd
	debugAddr string
	profile   string
	display   string
}

// launch starts Chromium against a profile on a display.
//
// Flags are limited to those Chromium 97 supports. --no-sandbox is deliberately
// omitted: it is a real security downgrade and is only needed when running as
// root, which the service must not do. The install script runs the browser as
// the beermate user precisely so the sandbox stays on.
func (m *cdpManager) launch(ctx context.Context, profileID, display, debugAddr string, headlessish bool) (*chromeInstance, error) {
	profilePath, err := safeProfilePath(m.cfg.ProfilesDir, profileID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profilePath, 0o750); err != nil {
		return nil, fmt.Errorf("browser: create profile dir: %w", err)
	}

	binary := m.cfg.Binary
	if binary == "" {
		binary = findChromium()
	}
	if binary == "" {
		return nil, errors.New("browser: no Chromium binary found; set browser_binary in config")
	}

	args := []string{
		"--remote-debugging-address=" + hostOf(debugAddr),
		"--remote-debugging-port=" + portOf(debugAddr),
		"--user-data-dir=" + profilePath,
		"--password-store=basic",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-translate",
		"--disable-features=Translate",
		"--disable-background-networking",
		"--disable-sync",
		"--metrics-recording-only",
		"--disable-infobars",
		"--noerrdialogs",
		fmt.Sprintf("--window-size=%d,%d", m.cfg.Width, m.cfg.Height),
	}
	if headlessish {
		// The capture instance runs kiosk-style on Xvfb so the whole viewport is
		// the page, with no chrome to crop out of the screenshot.
		args = append(args, "--kiosk", "--start-maximized")
	} else {
		args = append(args, "--start-maximized")
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	// Chromium is noisy on stderr; discard it rather than flooding the journal.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("browser: launch chromium: %w", err)
	}

	inst := &chromeInstance{cmd: cmd, debugAddr: debugAddr, profile: profileID, display: display}

	// Wait for the DevTools endpoint with bounded retries, so a Chromium that
	// never comes up fails fast instead of hanging a request.
	if err := waitForDevTools(ctx, debugAddr, 15*time.Second); err != nil {
		_ = inst.stop()
		return nil, err
	}
	return inst, nil
}

func (c *chromeInstance) stop() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait()
	return nil
}

// Capture returns a JPEG screenshot of targetURL rendered in the profile.
func (m *cdpManager) Capture(ctx context.Context, profileID, targetURL string) ([]byte, error) {
	m.mu.Lock()
	// The interactive instance owns the profile during login; refuse to also run
	// a capture against the same profile directory.
	if m.interactive != nil && m.interactive.profile == profileID {
		m.mu.Unlock()
		return m.cachedOrErr(profileID, errors.New("browser: profile is in interactive login"))
	}
	inst := m.capture
	m.mu.Unlock()

	if inst == nil || inst.profile != profileID {
		newInst, err := m.launch(ctx, profileID, m.cfg.XvfbDisplay, m.cfg.DebugAddr, true)
		if err != nil {
			m.setState(StateError, redactErr(err))
			return m.cachedOrErr(profileID, err)
		}
		m.mu.Lock()
		if m.capture != nil {
			_ = m.capture.stop()
		}
		m.capture = newInst
		inst = newInst
		m.mu.Unlock()
		m.setState(StateRunning, "capture instance running")
	}

	client, err := dialFirstPage(ctx, inst.debugAddr)
	if err != nil {
		return m.cachedOrErr(profileID, err)
	}
	defer client.Close()

	if err := client.navigate(ctx, targetURL); err != nil {
		return m.cachedOrErr(profileID, err)
	}
	shot, err := client.screenshotJPEG(ctx)
	if err != nil {
		return m.cachedOrErr(profileID, err)
	}

	m.mu.Lock()
	m.lastShot[profileID] = shot
	m.mu.Unlock()
	return shot, nil
}

func (m *cdpManager) cachedOrErr(profileID string, err error) ([]byte, error) {
	m.mu.Lock()
	shot := m.lastShot[profileID]
	m.mu.Unlock()
	if len(shot) > 0 {
		return shot, nil
	}
	return nil, err
}

// PrepareLogin opens the site on the real display for manual login.
func (m *cdpManager) PrepareLogin(ctx context.Context, profileID, targetURL string) error {
	m.mu.Lock()
	// Free the profile from the capture instance so the two never share it.
	if m.capture != nil && m.capture.profile == profileID {
		_ = m.capture.stop()
		m.capture = nil
	}
	if m.interactive != nil {
		_ = m.interactive.stop()
		m.interactive = nil
	}
	m.mu.Unlock()

	inst, err := m.launch(ctx, profileID, m.cfg.RealDisplay, "127.0.0.1:9223", false)
	if err != nil {
		m.setState(StateError, redactErr(err))
		return err
	}

	client, derr := dialFirstPage(ctx, inst.debugAddr)
	if derr == nil {
		_ = client.navigate(ctx, targetURL)
		client.Close()
	}

	m.mu.Lock()
	m.interactive = inst
	m.mu.Unlock()
	m.setState(StateInteractive, "interactive login open on the Jetson display")
	return nil
}

// FinishLogin closes the interactive instance, flushing the profile to disk.
func (m *cdpManager) FinishLogin(ctx context.Context, profileID string) error {
	_ = ctx
	m.mu.Lock()
	inst := m.interactive
	m.interactive = nil
	m.mu.Unlock()

	if inst == nil {
		return errors.New("browser: no interactive login is in progress")
	}
	// Stopping Chromium cleanly is what writes the session cookies into the
	// on-disk profile so they survive a reboot.
	if err := inst.stop(); err != nil {
		return err
	}
	m.setState(StateStopped, "login finished; profile saved")
	return nil
}

// Validate loads the target headlessly and checks for a login redirect.
func (m *cdpManager) Validate(ctx context.Context, profileID, targetURL, loginPattern string) (bool, error) {
	inst, err := m.launch(ctx, profileID, m.cfg.XvfbDisplay, "127.0.0.1:9224", true)
	if err != nil {
		return false, err
	}
	defer func() { _ = inst.stop() }()

	client, err := dialFirstPage(ctx, inst.debugAddr)
	if err != nil {
		return false, err
	}
	defer client.Close()

	if err := client.navigate(ctx, targetURL); err != nil {
		return false, err
	}
	landed, err := client.currentURL(ctx)
	if err != nil {
		return false, err
	}
	if looksLikeLoginRedirect(targetURL, landed, loginPattern) {
		return false, nil
	}
	return true, nil
}

// ClearSession deletes a profile directory.
func (m *cdpManager) ClearSession(ctx context.Context, profileID string) error {
	_ = ctx
	m.mu.Lock()
	if m.capture != nil && m.capture.profile == profileID {
		_ = m.capture.stop()
		m.capture = nil
	}
	if m.interactive != nil && m.interactive.profile == profileID {
		_ = m.interactive.stop()
		m.interactive = nil
	}
	delete(m.lastShot, profileID)
	m.mu.Unlock()

	path, err := safeProfilePath(m.cfg.ProfilesDir, profileID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("browser: clear profile: %w", err)
	}
	return nil
}

func (m *cdpManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.capture != nil {
		_ = m.capture.stop()
		m.capture = nil
	}
	if m.interactive != nil {
		_ = m.interactive.stop()
		m.interactive = nil
	}
	m.state = StateStopped
	return nil
}

// ---- DevTools HTTP endpoint ----------------------------------------------

// waitForDevTools polls the /json/version endpoint until Chromium is ready.
func waitForDevTools(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := "http://" + addr + "/json/version"
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("browser: DevTools endpoint %s did not become ready", addr)
}

// pageTarget describes one DevTools page target.
type pageTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	URL                  string `json:"url"`
}

// dialFirstPage connects to the first page target's DevTools WebSocket.
func dialFirstPage(ctx context.Context, addr string) (*cdpClient, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("browser: list targets: %w", err)
	}
	defer resp.Body.Close()

	var targets []pageTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("browser: decode targets: %w", err)
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return dialCDP(ctx, t.WebSocketDebuggerURL)
		}
	}
	return nil, errors.New("browser: no page target available")
}

var _ = sha256.Sum256
var _ = base64.StdEncoding
