package social

import (
	"testing"
	"time"
)

func TestPassesFiltersBlocklist(t *testing.T) {
	post := Post{Author: "spammer", Text: "buy now cheap"}
	if PassesFilters(post, "", "", "spammer") {
		t.Error("a blocklisted author should be rejected")
	}
	if PassesFilters(Post{Author: "ok", Text: "contains casino link"}, "", "", "casino") {
		t.Error("a blocklisted phrase in the text should be rejected")
	}
	if !PassesFilters(Post{Author: "ok", Text: "clean post"}, "", "", "spammer,casino") {
		t.Error("a clean post should pass the blocklist")
	}
}

func TestPassesFiltersHashtag(t *testing.T) {
	// With a hashtag filter, at least one listed tag must be present.
	if !PassesFilters(Post{Text: "great night #beermate cheers"}, "", "beermate", "") {
		t.Error("post with the required hashtag should pass")
	}
	if PassesFilters(Post{Text: "no tags here"}, "", "beermate", "") {
		t.Error("post without any required hashtag should be filtered out")
	}
	// The leading # is optional in the filter configuration.
	if !PassesFilters(Post{Text: "tap in #festival2026"}, "", "#festival2026", "") {
		t.Error("hashtag filter with a leading # should still match")
	}
}

func TestPassesFiltersKeyword(t *testing.T) {
	if !PassesFilters(Post{Text: "our new lager is out"}, "lager,ale", "", "") {
		t.Error("post containing a keyword should pass")
	}
	if PassesFilters(Post{Text: "unrelated content"}, "lager,ale", "", "") {
		t.Error("post without any keyword should be filtered out")
	}
}

// Blocklist must take precedence: a post that matches both a keyword and a
// blocked term is still rejected.
func TestBlocklistBeatsKeyword(t *testing.T) {
	if PassesFilters(Post{Text: "lager from a blocked brand"}, "lager", "", "blocked brand") {
		t.Error("blocklist should override a keyword match")
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	base := time.Minute
	cap := 2 * time.Hour

	if got := backoffFor(0, base, cap); got != 0 {
		t.Errorf("no failures should mean no backoff, got %v", got)
	}
	var prev time.Duration
	for f := 1; f <= 15; f++ {
		d := backoffFor(f, base, cap)
		if d < prev {
			t.Errorf("backoff decreased at failure %d: %v < %v", f, d, prev)
		}
		if d > cap {
			t.Errorf("backoff exceeded cap at failure %d: %v", f, d)
		}
		prev = d
	}
	if got := backoffFor(100, base, cap); got != cap {
		t.Errorf("backoff at 100 failures = %v, want the cap %v", got, cap)
	}
}

// A healthy feed polls on its own interval; a failing one is pushed out.
func TestNextAttemptRespectsFloorAndBackoff(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	// Below the 30s floor is clamped up.
	next := nextAttempt(now, 5, 0)
	if next.Sub(now) < 30*time.Second {
		t.Errorf("refresh interval below the floor should be clamped, got %v", next.Sub(now))
	}

	// A healthy feed uses its configured interval.
	next = nextAttempt(now, 900, 0)
	if next.Sub(now) != 900*time.Second {
		t.Errorf("healthy interval = %v, want 900s", next.Sub(now))
	}

	// After failures, backoff can exceed the configured interval.
	next = nextAttempt(now, 900, 6)
	if next.Sub(now) <= 900*time.Second {
		t.Errorf("after repeated failures the interval should grow beyond 900s, got %v", next.Sub(now))
	}
}

func TestCleanTextStripsMarkup(t *testing.T) {
	cases := map[string]string{
		"<p>hello <b>world</b></p>":             "hello world",
		"plain text":                            "plain text",
		"&lt;script&gt;alert(1)&lt;/script&gt;": "alert(1)",
		"line\n\nbreaks   collapsed":            "line breaks collapsed",
		"<a href='x'>link</a> text":             "link text",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// A post body must not be able to carry HTML into the display.
func TestCleanTextNeutralisesInjection(t *testing.T) {
	got := cleanText(`<img src=x onerror="alert(1)">caption`)
	if containsAny(got, "<", ">", "onerror") {
		t.Errorf("cleanText left markup in: %q", got)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

func TestAdaptersRegistered(t *testing.T) {
	for _, platform := range []string{"rss", "atom", "json", "youtube", "webhook", "manual"} {
		if _, ok := AdapterFor(platform); !ok {
			t.Errorf("adapter %q is not registered", platform)
		}
	}
	if _, ok := AdapterFor("myspace"); ok {
		t.Error("an unknown platform should not resolve to an adapter")
	}
}

func TestParseFlexibleTime(t *testing.T) {
	// RSS and Atom feeds use different date formats; both must parse.
	for _, s := range []string{
		"2026-07-24T12:00:00Z",
		"Mon, 24 Jul 2026 12:00:00 +0000",
		"2026-07-24",
	} {
		if parseFlexibleTime(s) == nil {
			t.Errorf("parseFlexibleTime(%q) returned nil", s)
		}
	}
	if parseFlexibleTime("not a date") != nil {
		t.Error("an unparseable date should return nil, not a zero time")
	}
}
