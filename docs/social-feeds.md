# Social feeds

Social content is ingested through **adapters** — one per source kind — held for
**moderation**, and cached so an outage never blanks the display. Only official,
documented feeds are used; there is no scraping of pages that forbid it and no
bypassing of logins.

## Supported sources

| Platform | Source | Credential |
|---|---|---|
| `rss` | an RSS feed URL | none |
| `atom` | an Atom feed URL | none |
| `youtube` | a channel id (or the channel's `videos.xml` URL) | none |
| `json` | a JSON endpoint returning posts | optional bearer token |
| `webhook` | posts POSTed to the ingest endpoint | none |
| `manual` | operator-curated posts | none |

Instagram, LinkedIn, Facebook and X are supported **through** these mechanisms:
point an `rss`/`json` feed at an official API or an approved integration that
exposes one, or push posts via `webhook`. Each real platform is an adapter, so a
first-party adapter can be added later (implement `social.Adapter`, `Register` it)
without touching the ingest loop.

## Adding a feed

Dashboard → **Social** → **Add feed**. Choose the platform, give the source
(feed URL or channel id), pick **manual** moderation (recommended) or automatic,
and optionally paste an API token for `json` feeds.

## Tokens

An API token is **AES-256-GCM encrypted at rest** with the credential row id as
additional authenticated data. It is:

- never returned by the API (only a `••••abcd` hint),
- never sent to the player,
- never written to logs (the logging redaction layer plus per-adapter care),
- never included in a backup.

To create a token, follow the source platform's documentation for a read-only
API key with the minimum scope needed to read the feed. Paste it once when adding
the feed; it is decrypted only for the duration of a fetch.

## Moderation

**Manual** is the default and recommended: posts arrive `pending` and are held
until an administrator approves them. In the dashboard, each feed's queue lets you
Approve, Reject, Hide, Pin (a pinned post survives cache trimming and shows
first), and delete individual cached posts. A keyword/hashtag filter and an author
blocklist run at ingest time; the blocklist always wins.

Post text is HTML-stripped on ingest, so a feed cannot inject markup into the
display.

## Failure behaviour

When a source is unreachable:

- cached **approved** posts keep showing,
- a branded fallback appears only if there is no cached content,
- the feed's error and connection state (`ok`/`error`/`unauthorised`/
  `rate_limited`) are shown in the dashboard,
- the next attempt is pushed out with **exponential backoff** (up to 2 h), so a
  broken feed is retried slowly rather than hammered,
- one failing feed never stalls the others — each is fetched independently.

The per-feed cache is capped (`social_cache_max_posts`, default 200) so it cannot
grow without bound.

## Display templates

`single`, `cards`, `vertical`, `ticker`, `grid`, `wall`, `sidebar`, `fullscreen`,
`latest`. A social zone references a feed and a template; the player fetches
approved posts from `/api/v1/player/social/<feed>`.

## Attribution

BeerMate branding wraps embedded/fetched content; author name, handle and
permalink from each post are preserved so platform attribution requirements are
respected.
