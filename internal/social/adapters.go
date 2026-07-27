package social

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient is the timeout-bounded client every adapter uses. Signage must
// never hang on a slow upstream, so there is no unbounded fetch anywhere.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// fetchBody performs a GET with an optional bearer token and returns the body.
//
// The response is capped so a hostile or misconfigured feed cannot stream an
// unbounded body into a 4 GB device.
func fetchBody(ctx context.Context, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BeerMate-DisplayManager/1.0 (+signage)")
	req.Header.Set("Accept", "application/json, application/rss+xml, application/atom+xml, application/xml, text/xml")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("source returned %d (check the API token)", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("source is rate limiting (429)")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("source returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func init() {
	Register(rssAdapter{})
	Register(atomAdapter{})
	Register(jsonAdapter{})
	Register(youtubeAdapter{})
	Register(webhookAdapter{})
	Register(manualAdapter{})
}

// ---- RSS ------------------------------------------------------------------

type rssAdapter struct{}

func (rssAdapter) Platform() string      { return "rss" }
func (rssAdapter) NeedsCredential() bool { return false }

type rssFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			GUID        string `xml:"guid"`
			Creator     string `xml:"creator"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (rssAdapter) Fetch(ctx context.Context, feed FeedConfig, maxItems int) ([]Post, error) {
	body, err := fetchBody(ctx, feed.Source, "")
	if err != nil {
		return nil, err
	}
	var doc rssFeed
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse RSS: %w", err)
	}
	var out []Post
	for i, it := range doc.Channel.Items {
		if i >= maxItems {
			break
		}
		id := it.GUID
		if id == "" {
			id = it.Link
		}
		out = append(out, Post{
			ExternalID: id,
			Author:     it.Creator,
			Text:       cleanText(firstNonEmpty(it.Title, it.Description)),
			Permalink:  it.Link,
			PostedAt:   parseFlexibleTime(it.PubDate),
		})
	}
	return out, nil
}

// ---- Atom -----------------------------------------------------------------

type atomAdapter struct{}

func (atomAdapter) Platform() string      { return "atom" }
func (atomAdapter) NeedsCredential() bool { return false }

type atomFeed struct {
	Entries []struct {
		ID      string `xml:"id"`
		Title   string `xml:"title"`
		Summary string `xml:"summary"`
		Updated string `xml:"updated"`
		Link    []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Author struct {
			Name string `xml:"name"`
		} `xml:"author"`
	} `xml:"entry"`
}

func (atomAdapter) Fetch(ctx context.Context, feed FeedConfig, maxItems int) ([]Post, error) {
	body, err := fetchBody(ctx, feed.Source, "")
	if err != nil {
		return nil, err
	}
	var doc atomFeed
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse Atom: %w", err)
	}
	var out []Post
	for i, e := range doc.Entries {
		if i >= maxItems {
			break
		}
		link := ""
		for _, l := range e.Link {
			if l.Rel == "alternate" || l.Rel == "" {
				link = l.Href
				break
			}
		}
		out = append(out, Post{
			ExternalID: firstNonEmpty(e.ID, link),
			Author:     e.Author.Name,
			Text:       cleanText(firstNonEmpty(e.Title, e.Summary)),
			Permalink:  link,
			PostedAt:   parseFlexibleTime(e.Updated),
		})
	}
	return out, nil
}

// ---- generic JSON ---------------------------------------------------------

type jsonAdapter struct{}

func (jsonAdapter) Platform() string      { return "json" }
func (jsonAdapter) NeedsCredential() bool { return false }

// Fetch reads a JSON array of {id,author,text,media,link,posted_at} objects, or
// an object with an "items"/"posts" array. This is the escape hatch for any
// service that can expose an authorised JSON endpoint.
func (jsonAdapter) Fetch(ctx context.Context, feed FeedConfig, maxItems int) ([]Post, error) {
	body, err := fetchBody(ctx, feed.Source, feed.Token)
	if err != nil {
		return nil, err
	}

	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err != nil {
		var wrap map[string]json.RawMessage
		if err2 := json.Unmarshal(body, &wrap); err2 != nil {
			return nil, fmt.Errorf("parse JSON feed: %w", err)
		}
		for _, key := range []string{"items", "posts", "data", "results"} {
			if raw, ok := wrap[key]; ok {
				_ = json.Unmarshal(raw, &arr)
				break
			}
		}
	}

	var out []Post
	for i, item := range arr {
		if i >= maxItems {
			break
		}
		out = append(out, Post{
			ExternalID: str(item, "id", "guid", "url", "link"),
			Author:     str(item, "author", "name", "user"),
			Text:       cleanText(str(item, "text", "title", "content", "message")),
			MediaURL:   str(item, "media", "image", "media_url", "thumbnail"),
			Permalink:  str(item, "link", "url", "permalink"),
			PostedAt:   parseFlexibleTime(str(item, "posted_at", "date", "published", "created_at")),
		})
	}
	return out, nil
}

// ---- YouTube (uploads via the channel RSS feed) ---------------------------

type youtubeAdapter struct{}

func (youtubeAdapter) Platform() string      { return "youtube" }
func (youtubeAdapter) NeedsCredential() bool { return false }

// Fetch reads a channel's public uploads feed. YouTube publishes this as Atom at
// a documented URL, so no API key or scraping is involved.
func (youtubeAdapter) Fetch(ctx context.Context, feed FeedConfig, maxItems int) ([]Post, error) {
	channelID := strings.TrimSpace(feed.Source)
	url := channelID
	if !strings.HasPrefix(channelID, "http") {
		url = "https://www.youtube.com/feeds/videos.xml?channel_id=" + channelID
	}
	body, err := fetchBody(ctx, url, "")
	if err != nil {
		return nil, err
	}

	type ytEntry struct {
		ID        string `xml:"videoId"`
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Author    struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Link struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Group struct {
			Thumbnail struct {
				URL string `xml:"url,attr"`
			} `xml:"thumbnail"`
		} `xml:"group"`
	}
	type ytFeed struct {
		Entries []ytEntry `xml:"entry"`
	}
	var doc ytFeed
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse YouTube feed: %w", err)
	}
	var out []Post
	for i, e := range doc.Entries {
		if i >= maxItems {
			break
		}
		out = append(out, Post{
			ExternalID: e.ID,
			Author:     e.Author.Name,
			Text:       cleanText(e.Title),
			MediaURL:   e.Group.Thumbnail.URL,
			MediaKind:  "image",
			Permalink:  e.Link.Href,
			PostedAt:   parseFlexibleTime(e.Published),
		})
	}
	return out, nil
}

// ---- webhook (ingested posts) ---------------------------------------------

// webhookAdapter has no fetch of its own: posts arrive by POST to the webhook
// endpoint and are stored directly. Fetch is a no-op so the ingest loop can
// treat every feed uniformly.
type webhookAdapter struct{}

func (webhookAdapter) Platform() string      { return "webhook" }
func (webhookAdapter) NeedsCredential() bool { return false }
func (webhookAdapter) Fetch(context.Context, FeedConfig, int) ([]Post, error) {
	return nil, nil
}

// ---- manual (curated posts) -----------------------------------------------

// manualAdapter is entirely operator-curated; nothing is fetched.
type manualAdapter struct{}

func (manualAdapter) Platform() string      { return "manual" }
func (manualAdapter) NeedsCredential() bool { return false }
func (manualAdapter) Fetch(context.Context, FeedConfig, int) ([]Post, error) {
	return nil, nil
}

// ---- helpers --------------------------------------------------------------

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// cleanText strips HTML tags and unescapes entities, so a feed cannot inject
// markup into the display through a post body.
func cleanText(s string) string {
	s = html.UnescapeString(s)
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if len(out) > 600 {
		out = out[:600] + "…"
	}
	return out
}

func parseFlexibleTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02",
		"Mon, 02 Jan 2006 15:04:05 -0700",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}
