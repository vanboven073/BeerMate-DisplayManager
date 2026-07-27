// Package social ingests posts from external sources through pluggable adapters
// and holds them for moderation before they reach the display.
//
// Design rules, all driven by platform policy and the resource budget:
//   - Every platform is an Adapter. Adding one is implementing an interface, not
//     touching the ingest loop.
//   - Only official, documented feeds are used (RSS/Atom, JSON, YouTube, webhook,
//     manual). No scraping of pages that forbid it, and no bypassing logins.
//   - API tokens are encrypted at rest and never reach the player or the logs.
//   - Fetching backs off exponentially on failure and respects a per-feed cache
//     cap, so an unreachable or rate-limited source never grows unbounded or
//     hammers the upstream.
//   - "manual" moderation is the recommended default: posts are held for approval
//     rather than shown automatically.
package social

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Post is a normalised item from any adapter.
type Post struct {
	ExternalID   string
	Author       string
	AuthorHandle string
	AvatarURL    string
	Text         string
	MediaURL     string
	MediaKind    string // image | video | ""
	Permalink    string
	PostedAt     *time.Time
}

// Adapter fetches posts from one kind of source.
type Adapter interface {
	// Platform is the adapter's identifier, matching social_feeds.platform.
	Platform() string
	// Fetch returns recent posts. It must respect ctx's deadline and must never
	// return more than maxItems.
	Fetch(ctx context.Context, feed FeedConfig, maxItems int) ([]Post, error)
	// NeedsCredential reports whether this adapter requires an API token.
	NeedsCredential() bool
}

// FeedConfig is the adapter-facing view of a feed, with the decrypted credential
// resolved by the service. The plaintext token lives only in this struct, only
// for the duration of a fetch, and is never persisted or logged.
type FeedConfig struct {
	ID       int64
	Platform string
	Source   string
	Config   map[string]any
	// Token is the decrypted API credential, empty when the feed has none.
	Token string
	// HTTP is the shared, timeout-bounded client the adapter must use.
	HTTP HTTPDoer
}

// HTTPDoer is the subset of *http.Client the adapters use, so tests can inject a
// fake without a real network.
type HTTPDoer interface {
	Do(req interface{ URL() string }) ([]byte, int, error)
}

// ConfigString reads a string field from the feed config.
func (f FeedConfig) ConfigString(key string) string {
	if v, ok := f.Config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ConfigInt reads an integer field from the feed config.
func (f FeedConfig) ConfigInt(key string, def int) int {
	if v, ok := f.Config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// registry maps platform names to adapters.
var registry = map[string]Adapter{}

// Register adds an adapter. Called from adapter init functions.
func Register(a Adapter) {
	registry[a.Platform()] = a
}

// AdapterFor returns the adapter for a platform.
func AdapterFor(platform string) (Adapter, bool) {
	a, ok := registry[platform]
	return a, ok
}

// Platforms lists the registered platform names.
func Platforms() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}

// ErrUnknownPlatform is returned for an unregistered platform.
var ErrUnknownPlatform = errors.New("social: unknown platform")

// ---- moderation and filtering --------------------------------------------

// PassesFilters reports whether a post survives the feed's keyword, hashtag and
// blocklist filters.
func PassesFilters(p Post, keywordFilter, hashtagFilter, blocklist string) bool {
	text := strings.ToLower(p.Text)
	author := strings.ToLower(p.Author + " " + p.AuthorHandle)

	// Blocklist: any listed author or phrase rejects the post outright.
	for _, term := range splitTerms(blocklist) {
		if term == "" {
			continue
		}
		if strings.Contains(author, term) || strings.Contains(text, term) {
			return false
		}
	}

	// Hashtag filter: at least one listed tag must be present.
	tags := splitTerms(hashtagFilter)
	if len(tags) > 0 {
		found := false
		for _, tag := range tags {
			needle := tag
			if !strings.HasPrefix(needle, "#") {
				needle = "#" + needle
			}
			if strings.Contains(text, strings.ToLower(needle)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Keyword filter: at least one listed keyword must be present.
	keywords := splitTerms(keywordFilter)
	if len(keywords) > 0 {
		found := false
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func splitTerms(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		t := strings.ToLower(strings.TrimSpace(f))
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---- backoff --------------------------------------------------------------

// backoffFor returns the delay before the next attempt after n consecutive
// failures, capped so a persistently failing feed settles into a slow retry
// rather than hammering the source or spinning.
func backoffFor(consecutiveFailures int, base, cap time.Duration) time.Duration {
	if consecutiveFailures <= 0 {
		return 0
	}
	d := base
	for i := 1; i < consecutiveFailures && d < cap; i++ {
		d *= 2
	}
	if d > cap {
		d = cap
	}
	return d
}

// nextAttempt computes when a feed should next be polled.
func nextAttempt(now time.Time, refreshSec, consecutiveFailures int) time.Time {
	interval := time.Duration(refreshSec) * time.Second
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if consecutiveFailures > 0 {
		backoff := backoffFor(consecutiveFailures, time.Minute, 2*time.Hour)
		if backoff > interval {
			interval = backoff
		}
	}
	return now.Add(interval)
}

var _ = fmt.Sprintf
