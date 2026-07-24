// Package display abstracts physical display power control.
//
// The interface exists so scheduling logic can be tested without an X server,
// and so a failure to talk to the display never takes the service down: a Jetson
// that cannot blank its screen should keep serving the admin UI and keep the
// player running.
package display

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// State is the display power state.
type State string

const (
	// StateOn means the panel is powered.
	StateOn State = "on"
	// StateOff means the panel is in DPMS standby.
	StateOff State = "off"
	// StateUnknown means the state could not be determined.
	StateUnknown State = "unknown"
)

// Controller drives display power.
type Controller interface {
	// Set applies the desired state.
	Set(ctx context.Context, s State) error
	// Get reports the current state, or StateUnknown.
	Get(ctx context.Context) (State, error)
	// Name identifies the implementation for the dashboard.
	Name() string
}

// commandTimeout bounds every external call. An xset that hangs (for example
// because the X server is mid-restart) must not wedge the scheduler goroutine.
const commandTimeout = 5 * time.Second

// XSetController drives DPMS through the xset utility on the Jetson.
type XSetController struct {
	display string
	binary  string
	log     *slog.Logger

	mu       sync.Mutex
	lastErr  error
	lastSeen State
}

// NewXSet builds a controller for the given X display (typically ":0").
func NewXSet(displayName, binary string, log *slog.Logger) *XSetController {
	if binary == "" {
		binary = "xset"
	}
	if log == nil {
		log = slog.Default()
	}
	return &XSetController{display: displayName, binary: binary, log: log, lastSeen: StateUnknown}
}

// Name implements Controller.
func (c *XSetController) Name() string { return "xset" }

func (c *XSetController) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary, args...)
	// DISPLAY must be set explicitly: the service runs from systemd, which does
	// not inherit the graphical session's environment. XAUTHORITY is inherited
	// from the unit so the beermate user's session cookie applies.
	cmd.Env = append(cmd.Environ(), "DISPLAY="+c.display)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("xset timed out after %s: %w", commandTimeout, ctx.Err())
		}
		return string(out), fmt.Errorf("xset %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Set implements Controller.
func (c *XSetController) Set(ctx context.Context, s State) error {
	var args []string
	switch s {
	case StateOn:
		// "dpms force on" alone leaves the screensaver free to blank the panel
		// again moments later, so the saver is disabled at the same time.
		args = []string{"dpms", "force", "on"}
	case StateOff:
		args = []string{"dpms", "force", "off"}
	default:
		return fmt.Errorf("display: cannot set state %q", s)
	}

	_, err := c.run(ctx, args...)

	c.mu.Lock()
	c.lastErr = err
	if err == nil {
		c.lastSeen = s
	}
	c.mu.Unlock()

	if err != nil {
		return err
	}
	if s == StateOn {
		// Best effort: keep the X screensaver from re-blanking the panel. A
		// failure here is not fatal to the state change that just succeeded.
		if _, err := c.run(ctx, "s", "off", "-dpms"); err != nil {
			c.log.Debug("could not disable screensaver", "error", err)
		}
		// Re-enable DPMS so a later "force off" still works.
		if _, err := c.run(ctx, "+dpms"); err != nil {
			c.log.Debug("could not re-enable dpms", "error", err)
		}
	}
	return nil
}

// Get implements Controller by parsing `xset q`.
func (c *XSetController) Get(ctx context.Context) (State, error) {
	out, err := c.run(ctx, "q")
	if err != nil {
		return StateUnknown, err
	}
	low := strings.ToLower(out)
	// xset q prints "Monitor is On" / "Monitor is Off" / "Monitor is in Standby".
	switch {
	case strings.Contains(low, "monitor is on"):
		return StateOn, nil
	case strings.Contains(low, "monitor is off"),
		strings.Contains(low, "monitor is in standby"),
		strings.Contains(low, "monitor is in suspend"):
		return StateOff, nil
	}
	return StateUnknown, nil
}

// LastError reports the most recent failure, for the health endpoint.
func (c *XSetController) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// ---- mock ------------------------------------------------------------------

// MockController records calls instead of touching hardware. Used in development
// on Windows and throughout the test suite.
type MockController struct {
	mu     sync.Mutex
	state  State
	calls  []State
	FailOn State // when set, Set returns an error for this state
}

// NewMock builds a MockController that starts powered off.
func NewMock() *MockController { return &MockController{state: StateOff} }

// Name implements Controller.
func (m *MockController) Name() string { return "mock" }

// Set implements Controller.
func (m *MockController) Set(_ context.Context, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, s)
	if m.FailOn != "" && s == m.FailOn {
		return errors.New("display: simulated failure")
	}
	m.state = s
	return nil
}

// Get implements Controller.
func (m *MockController) Get(_ context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, nil
}

// Calls returns the states Set was asked for, in order.
func (m *MockController) Calls() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]State, len(m.calls))
	copy(out, m.calls)
	return out
}

// Reset clears recorded calls.
func (m *MockController) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// ---- noop ------------------------------------------------------------------

// NoopController does nothing, for deployments where the display is managed
// externally.
type NoopController struct{}

// Name implements Controller.
func (NoopController) Name() string { return "noop" }

// Set implements Controller.
func (NoopController) Set(context.Context, State) error { return nil }

// Get implements Controller.
func (NoopController) Get(context.Context) (State, error) { return StateUnknown, nil }

// New builds a Controller from a driver name.
func New(driver, displayName, binary string, log *slog.Logger) Controller {
	switch driver {
	case "xset":
		return NewXSet(displayName, binary, log)
	case "mock":
		return NewMock()
	default:
		return NoopController{}
	}
}
