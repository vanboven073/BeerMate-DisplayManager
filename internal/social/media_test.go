package social

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The shape rss.app produces for an Instagram feed: a Media RSS <media:content>
// on every item, the image also embedded in the description HTML, and the caption
// in the title. Trimmed from the real feed for BEERMATE (@beermate.events).
const rssAppInstagram = `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:dc="http://purl.org/dc/elements/1.1/"
     xmlns:content="http://purl.org/rss/1.0/modules/content/"
     version="2.0" xmlns:media="http://search.yahoo.com/mrss/">
<channel>
  <title><![CDATA[BEERMATE (@beermate.events)]]></title>
  <item>
    <title><![CDATA[Successful BeerMate implementation completed!]]></title>
    <description><![CDATA[<div><img src="https://cdn.example.com/in-description.jpg?oe=1" style="width: 100%;" /><div>caption</div></div>]]></description>
    <link>https://www.instagram.com/p/DbF3hkVI-0g</link>
    <guid isPermaLink="false">5ed10350f4cc96f22f483356870bd26e</guid>
    <dc:creator><![CDATA[beermate.events]]></dc:creator>
    <pubDate>Wed, 22 Jul 2026 10:29:19 GMT</pubDate>
    <media:content medium="image" url="https://cdn.example.com/structured.jpg?stp=dst-jpg&amp;oe=6A77A7BC"/>
  </item>
</channel>
</rss>`

func serveBody(t *testing.T, body, contentType string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The bug: an Instagram feed rendered as text only, because the RSS adapter
// parsed no image element at all and the player renders an image solely when
// media_kind says image.
func TestRSSAdapterExtractsMediaRSSImage(t *testing.T) {
	url := serveBody(t, rssAppInstagram, "application/rss+xml")

	posts, err := rssAdapter{}.Fetch(context.Background(), FeedConfig{Source: url}, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	p := posts[0]

	// The structured element wins over the one embedded in the description.
	if want := "https://cdn.example.com/structured.jpg?stp=dst-jpg&oe=6A77A7BC"; p.MediaURL != want {
		t.Errorf("MediaURL = %q, want %q", p.MediaURL, want)
	}
	if p.MediaKind != "image" {
		t.Errorf("MediaKind = %q, want image (the player gates the <img> on this)", p.MediaKind)
	}
	if p.Author != "beermate.events" {
		t.Errorf("Author = %q", p.Author)
	}
	if p.PostedAt == nil {
		t.Error("PostedAt was not parsed")
	}
}

// Plenty of bridges carry no Media RSS element and only embed the image in the
// description, so that fallback has to work too.
func TestRSSAdapterFallsBackToDescriptionImage(t *testing.T) {
	body := `<?xml version="1.0"?><rss version="2.0"><channel><item>
	  <title>no structured media</title>
	  <description><![CDATA[<p>text</p><img src='https://cdn.example.com/from-html.png'/>]]></description>
	  <link>https://example.com/p/1</link>
	</item></channel></rss>`

	posts, err := rssAdapter{}.Fetch(context.Background(), FeedConfig{Source: serveBody(t, body, "text/xml")}, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if posts[0].MediaURL != "https://cdn.example.com/from-html.png" || posts[0].MediaKind != "image" {
		t.Errorf("got %q / %q", posts[0].MediaURL, posts[0].MediaKind)
	}
}

func TestRSSAdapterReadsEnclosure(t *testing.T) {
	body := `<?xml version="1.0"?><rss version="2.0"><channel><item>
	  <title>enclosure only</title>
	  <link>https://example.com/p/2</link>
	  <enclosure url="https://cdn.example.com/pic.jpg" type="image/jpeg" length="1234"/>
	</item></channel></rss>`

	posts, err := rssAdapter{}.Fetch(context.Background(), FeedConfig{Source: serveBody(t, body, "text/xml")}, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if posts[0].MediaURL != "https://cdn.example.com/pic.jpg" || posts[0].MediaKind != "image" {
		t.Errorf("got %q / %q", posts[0].MediaURL, posts[0].MediaKind)
	}
}

// A post with no media at all must stay empty rather than acquire a bogus kind,
// or the player would render a broken <img> instead of a clean text card.
func TestRSSAdapterLeavesMediaEmptyWhenAbsent(t *testing.T) {
	body := `<?xml version="1.0"?><rss version="2.0"><channel><item>
	  <title>text only</title><description>just words</description>
	  <link>https://example.com/p/3</link>
	</item></channel></rss>`

	posts, err := rssAdapter{}.Fetch(context.Background(), FeedConfig{Source: serveBody(t, body, "text/xml")}, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if posts[0].MediaURL != "" || posts[0].MediaKind != "" {
		t.Errorf("expected no media, got %q / %q", posts[0].MediaURL, posts[0].MediaKind)
	}
}

func TestAtomAdapterExtractsMedia(t *testing.T) {
	body := `<?xml version="1.0"?>
	<feed xmlns="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/">
	  <entry>
	    <id>tag:example,1</id><title>hello</title><updated>2026-07-22T10:29:19Z</updated>
	    <link rel="alternate" href="https://example.com/p/1"/>
	    <media:thumbnail url="https://cdn.example.com/thumb.jpg"/>
	  </entry>
	</feed>`

	posts, err := atomAdapter{}.Fetch(context.Background(), FeedConfig{Source: serveBody(t, body, "application/atom+xml")}, 10)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if posts[0].MediaURL != "https://cdn.example.com/thumb.jpg" || posts[0].MediaKind != "image" {
		t.Errorf("got %q / %q", posts[0].MediaURL, posts[0].MediaKind)
	}
}

func TestInferMediaKind(t *testing.T) {
	cases := []struct {
		name, url, mime, medium, want string
	}{
		{"explicit mime", "https://x/y", "image/png", "", "image"},
		{"explicit medium", "https://x/y", "", "image", "image"},
		{"video mime", "https://x/y", "video/mp4", "", "video"},
		{"video medium", "https://x/y", "", "video", "video"},
		// Signed CDN URLs carry a long query string after the filename.
		{"signed jpg", "https://cdn/x.jpg?stp=dst-jpg&oe=6A77A7BC", "", "", "image"},
		{"mp4 by extension", "https://cdn/clip.mp4?token=1", "", "", "video"},
		{"extensionless defaults to image", "https://cdn/abc123", "", "", "image"},
		{"empty stays empty", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inferMediaKind(c.url, c.mime, c.medium); got != c.want {
				t.Errorf("inferMediaKind(%q,%q,%q) = %q, want %q", c.url, c.mime, c.medium, got, c.want)
			}
		})
	}
}

// Media RSS marks the cover frame of a video post as medium="image", which is
// exactly what a signage zone can render. An actual video URL must not be
// preferred over an available still.
func TestPickMediaPrefersImageOverVideo(t *testing.T) {
	refs := [][]xmlMediaRef{{
		{URL: "https://cdn/clip.mp4", Medium: "video"},
		{URL: "https://cdn/cover.jpg", Medium: "image"},
	}}
	url, kind := pickMedia(refs)
	if url != "https://cdn/cover.jpg" || kind != "image" {
		t.Errorf("got %q / %q", url, kind)
	}
}
