// Package logging provides structured logging with a hard redaction layer.
//
// The redaction layer exists because this service handles website session cookies,
// social API tokens and login form contents. None of those may ever reach the
// systemd journal. Redaction is applied centrally in the slog Handler rather than
// at each call site, so a careless log statement elsewhere cannot leak a secret.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Sensitive attribute keys are replaced wholesale. Matching is case-insensitive
// and substring-based so "set-cookie", "X-Auth-Token" and "refresh_token" all hit.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "cookie", "authorization",
	"auth_header", "credential", "apikey", "api_key", "session_id", "sessionid",
	"csrf", "assertion", "private_key", "ciphertext", "nonce", "hash",
}

const redacted = "[REDACTED]"

// Options configures the logger.
type Options struct {
	// Level is the minimum level emitted.
	Level slog.Level
	// JSON selects JSON output (production/journal) instead of text (development).
	JSON bool
	// Writer defaults to os.Stderr.
	Writer io.Writer
	// AddSource includes file:line. Off by default; it is measurable overhead on
	// a Jetson and the messages are specific enough without it.
	AddSource bool
}

// New builds a redacting slog.Logger.
func New(o Options) *slog.Logger {
	w := o.Writer
	if w == nil {
		w = os.Stderr
	}
	ho := &slog.HandlerOptions{
		Level:       o.Level,
		AddSource:   o.AddSource,
		ReplaceAttr: replaceAttr,
	}
	var h slog.Handler
	if o.JSON {
		h = slog.NewJSONHandler(w, ho)
	} else {
		h = slog.NewTextHandler(w, ho)
	}
	return slog.New(&redactHandler{inner: h})
}

// ParseLevel maps a configuration string to a slog level, defaulting to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// replaceAttr redacts values whose key looks sensitive, at any nesting depth.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if isSensitive(a.Key) {
		return slog.String(a.Key, redacted)
	}
	// A group whose *name* is sensitive redacts wholesale.
	for _, g := range groups {
		if isSensitive(g) {
			return slog.String(a.Key, redacted)
		}
	}
	return a
}

// redactHandler additionally scrubs attributes attached via With(), which
// ReplaceAttr alone does not reliably cover across handler implementations.
type redactHandler struct{ inner slog.Handler }

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = replaceAttr(nil, a)
	}
	return &redactHandler{inner: h.inner.WithAttrs(out)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name)}
}

// Discard returns a logger that drops everything, for tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}
