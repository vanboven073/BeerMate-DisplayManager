package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// cdpClient is a minimal Chrome DevTools Protocol client over a WebSocket.
//
// Only the handful of methods the display manager needs are implemented
// (navigate, screenshot, evaluate). A full CDP binding would be far larger and
// most of it would never run on a signage appliance.
type cdpClient struct {
	conn   *websocket.Conn
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan cdpResponse
	closed  bool

	cancel context.CancelFunc
}

type cdpRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *cdpError       `json:"error"`
	Method string          `json:"method"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message) }

// dialCDP connects to a DevTools WebSocket URL and starts the read loop.
func dialCDP(ctx context.Context, wsURL string) (*cdpClient, error) {
	// The connection lifetime is decoupled from the caller's request context so
	// one slow navigate does not tear down the socket mid-command.
	connCtx, cancel := context.WithCancel(context.Background())
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("browser: dial devtools: %w", err)
	}
	// CDP screenshot messages are large; lift the default read limit.
	conn.SetReadLimit(32 << 20)

	c := &cdpClient{
		conn:    conn,
		pending: make(map[int64]chan cdpResponse),
		cancel:  cancel,
	}
	go c.readLoop(connCtx)
	return c, nil
}

func (c *cdpClient) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.failAll(err)
			return
		}
		var resp cdpResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			// An event (has a Method, no ID). Nothing here subscribes to events,
			// so they are dropped.
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

func (c *cdpClient) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// call sends a CDP command and waits for its reply within ctx.
func (c *cdpClient) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("browser: connection closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	req := cdpRequest{ID: id, Method: method, Params: params}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("browser: write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp, open := <-ch:
		if !open {
			return nil, fmt.Errorf("browser: connection closed awaiting %s", method)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// navigate loads a URL and waits briefly for it to settle.
func (c *cdpClient) navigate(ctx context.Context, url string) error {
	if _, err := c.call(ctx, "Page.enable", nil); err != nil {
		return err
	}
	if _, err := c.call(ctx, "Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	// A fixed settle delay rather than waiting on the load event: dashboards
	// often keep a connection open forever, so the load event may never fire.
	// The delay is bounded by ctx.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2500 * time.Millisecond):
	}
	return nil
}

// screenshotJPEG captures the viewport as a JPEG.
func (c *cdpClient) screenshotJPEG(ctx context.Context) ([]byte, error) {
	raw, err := c.call(ctx, "Page.captureScreenshot", map[string]any{
		"format":      "jpeg",
		"quality":     70,
		"fromSurface": true,
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return nil, fmt.Errorf("browser: decode screenshot: %w", err)
	}
	return decoded, nil
}

// currentURL returns the page's current location, used for redirect detection.
func (c *cdpClient) currentURL(ctx context.Context) (string, error) {
	raw, err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "window.location.href",
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}

// Close tears down the connection.
func (c *cdpClient) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

// ---- helpers --------------------------------------------------------------

func hostOf(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func portOf(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// findChromium looks for a Chromium binary in the usual Jetson locations.
func findChromium() string {
	for _, name := range []string{
		"chromium-browser", "chromium", "google-chrome", "google-chrome-stable",
	} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/usr/bin/chromium-browser", "/usr/bin/chromium",
		"/snap/bin/chromium", "/usr/bin/google-chrome",
	} {
		if fi, err := exec.Command("test", "-x", p).Output(); err == nil {
			_ = fi
			return p
		}
	}
	return ""
}

// redactErr strips anything that could carry a URL with a token from an error
// before it is stored or shown. Errors here can quote a navigated URL, which
// might contain a one-time login token in its query string.
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "?"); i >= 0 {
		msg = msg[:i] + "?[redacted]"
	}
	return msg
}
