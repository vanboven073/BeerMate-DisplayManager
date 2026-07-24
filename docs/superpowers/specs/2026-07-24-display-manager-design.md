# BeerMate Display Manager — Design Specification

**Date:** 2026-07-24
**Status:** Approved for implementation
**Target device:** NVIDIA Jetson Nano (original), Ubuntu 18.04, ARM64, Chromium ~97

---

## 1. Problem

The current signage setup is a Bash script (`/home/beermate/bin/beermate-kiosk.sh`) that
switches Chromium tabs with `xdotool` on a 60-second timer. It is fragile: tab focus is
positional, a crashed tab breaks the rotation, content changes require SSH and file edits,
and nothing can be managed remotely by a non-technical operator.

We replace it with a managed platform: a Go service on the Jetson, an authenticated admin
dashboard reachable over Tailscale from a Windows laptop, and **one persistent Chromium
page** that renders and rotates all content itself.

## 2. Constraints that shape the design

| Constraint | Consequence |
|---|---|
| ARM64, `CGO_ENABLED=0` cross-compile from Windows | **Pure-Go SQLite** (`modernc.org/sqlite`). No `mattn/go-sqlite3`. |
| Chromium 97 (Jan 2022) | Build target `es2019`. No `:has()`, no container queries, no `Array.at`, no top-level await, no `structuredClone`. |
| Jetson Nano: 4× A57 @ 1.43 GHz, 4 GB RAM, shared with the display | Small bundles, bounded goroutines/timers, no polling loops, at most one managed browser. |
| No Node.js in production | Frontend compiled at build time, embedded via `go:embed`. Single binary deploy. |
| Runs unattended for months | Bounded everything: caches, logs, backups, retries, queues. Graceful degradation over crashes. |

## 3. Architecture

```
┌─────────────────── Jetson Nano (Ubuntu 18.04, ARM64) ───────────────────┐
│                                                                          │
│  systemd: beermate-display-manager.service (user beermate)               │
│  ┌────────────────────────────────────────────────────────────────┐     │
│  │  Single Go binary — listens 0.0.0.0:8080                       │     │
│  │                                                                 │     │
│  │  HTTP  /admin  (embedded Preact SPA, session auth)             │     │
│  │        /player (embedded Preact app, player token)             │     │
│  │        /api/v1 (REST + SSE)                                    │     │
│  │        /health (unauthenticated, non-sensitive)                │     │
│  │                                                                 │     │
│  │  Services: playlist · scenes · media · scheduler · dpms         │     │
│  │            browser(CDP) · social · backup · realtime(SSE)       │     │
│  │                                                                 │     │
│  │  SQLite (WAL)  ──►  /var/lib/beermate-display-manager/database  │     │
│  └────────────────────────────────────────────────────────────────┘     │
│         ▲                    ▲                        │                  │
│    HTTP │ 127.0.0.1     CDP  │ 127.0.0.1:9222        │ xset dpms        │
│         │                    │ (localhost ONLY)       ▼                  │
│  ┌──────┴───────┐   ┌────────┴──────────┐      ┌──────────┐             │
│  │ Chromium     │   │ Managed Chromium  │      │ HDMI     │             │
│  │ --kiosk      │   │ on Xvfb :99       │      │ display  │             │
│  │ /player      │   │ persistent profile│      └──────────┘             │
│  │ (display :0) │   │ screenshot stream │                                │
│  └──────────────┘   └───────────────────┘                                │
└──────────────────────────────────────────────────────────────────────────┘
            ▲
            │ Tailscale (private, no port forwarding)
      ┌─────┴──────┐
      │ Windows    │  http://<tailscale-ip>:8080/admin
      │ laptop     │
      └────────────┘
```

### 3.1 Why these choices

**Go + pure-Go SQLite.** The hard requirement `CGO_ENABLED=0 GOARCH=arm64` rules out
cgo-based SQLite drivers. `modernc.org/sqlite` is a transpiled pure-Go SQLite that
cross-compiles cleanly from Windows and needs no toolchain on the Jetson.

**Preact + TypeScript + Vite** over React. Preact is ~4 KB vs React's ~45 KB, uses the
same component model, and Vite targets `es2019` cleanly for Chromium 97. On a device where
the browser competes with the OS for 4 GB, bundle size and runtime overhead are real costs.
`preact/compat` remains available if a React-only library is ever needed.

**SSE, not WebSocket.** Updates flow one direction (server → player/admin). `EventSource`
has automatic reconnection with backoff built into the browser — exactly the recovery
behaviour required after backend restarts and network drops, with no client reconnect code
to get wrong. The player reports status via ordinary `POST` heartbeats. WebSocket would add
a bidirectional framing layer we do not need.

**Scene-first content model.** Rather than bolting split-screen onto a slide list, *every*
playlist entry is a **scene** containing one or more **zones**. A traditional fullscreen
slide is simply a scene with a single zone. This makes split-screen a first-class feature
(as required) instead of a special case, and means scheduling, revisions, validation, and
publishing have exactly one code path.

**Revision-based atomic publish.** Edits mutate a *draft* revision. Publishing inserts a new
`playlist_revision` row and flips a single pointer inside one transaction. The player only
ever reads the published revision, so a half-edited playlist can never go live, and rollback
is a pointer flip to an earlier revision.

### 3.2 Website rendering — the hard part

Websites split into two classes:

1. **Embeddable** (no `X-Frame-Options`/frame-ancestors restriction, no login) → rendered in
   a sandboxed `<iframe>` directly in the player page. Cheap.
2. **Non-embeddable or authenticated** (the BeerMate dashboard, anything behind SSO) →
   rendered by a **managed Chromium** driven over the Chrome DevTools Protocol.

The managed browser runs on a **virtual X display (`Xvfb :99`)** with a persistent profile
directory, and streams JPEG screenshots to the player zone. Frames are pushed only when the
image hash changes, at a configurable interval (default 5 s for dashboards), so a static
dashboard costs almost nothing.

This replaces `xdotool` entirely and keeps exactly one visible browser page.

**CDP exposure:** `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222`.
Localhost only, never `0.0.0.0`, never reachable over Tailscale. CDP grants full control of
the browser and its cookies — treating it as a loopback-only interface is non-negotiable.

### 3.3 Authenticated website sessions

Passwords are never stored. The flow is:

1. Admin marks a website slide as requiring authentication.
2. Admin clicks **Prepare login session** → backend stops the capture instance and starts the
   *same profile* on the **real display `:0`**, windowed.
3. Admin logs in physically at the Jetson (or via an independently secured remote desktop over
   Tailscale). MFA, SSO, consent banners, captcha all work because a real human drives a real
   browser.
4. Admin clicks **Finish login setup** → backend closes the interactive instance, restarts on
   Xvfb, then **validates** by loading the target URL and checking it does not redirect to a
   login page.
5. Cookies/localStorage live in `/var/lib/beermate-display-manager/browser-profiles/<id>/`
   and therefore survive Chromium restart, backend restart, and reboot.

When a session expires, the player does **not** sit on a login page: the backend detects the
login redirect, marks the site `reauthentication_required`, shows a branded fallback slide,
raises a dashboard warning, and playback advances after a bounded timeout.

## 4. Data model (core tables)

```
users(id, username, password_hash, role, created_at, ...)
sessions(id, user_id, expires_at, ip, user_agent, revoked_at)
audit_log(id, actor, action, target_type, target_id, detail, created_at)

playlist_revisions(id, created_at, created_by, published_at, note)
scenes(id, revision_id, name, position, enabled, layout, duration_ms,
       background, transition, active_from, active_until, days_mask, ...)
zones(id, scene_id, slot, x, y, w, h, content_type, content_ref, config_json, style_json)

media(id, kind, stored_name, original_name, mime, bytes, width, height,
      duration_ms, sha256, thumb_name, created_at, created_by)
websites(id, name, url, requires_auth, profile_id, render_mode, session_state,
         last_ok_at, last_validated_at, ...)
social_feeds(id, platform, source, config_json, credential_id, refresh_sec, ...)
social_posts(id, feed_id, external_id, author, text, media_url, posted_at,
             moderation_state, pinned, ...)
credentials(id, name, ciphertext, nonce, created_at)      -- AES-256-GCM
settings(key, value_json, updated_at, updated_by)
schedule_rules(id, kind, weekday, date, on_time, off_time, enabled)
player_state(id, online, current_scene, revision, last_heartbeat, detail_json)
backups(id, filename, bytes, sha256, kind, created_at)
```

Zones carry `content_type` + `content_ref` + `config_json`. Adding a new slide type means
adding a `content_type` constant, a validator, and a player renderer — no schema change.

## 5. Security model

- **Passwords:** Argon2id (64 MB, t=2, p=2 — tuned down from OWASP default because the Jetson
  has 4 GB shared with the display; still far above bcrypt cost 12).
- **Sessions:** opaque 256-bit random ID, stored hashed; `HttpOnly`, `SameSite=Lax`,
  `Secure` when served over HTTPS; sliding expiry with absolute cap; server-side revocation.
- **CSRF:** double-submit token on all state-changing requests, validated constant-time.
- **Rate limiting:** per-IP + per-username token bucket on login; exponential lockout.
- **Uploads:** extension ∪ declared MIME ∪ **magic-byte signature** must all agree; size caps;
  generated storage names (never the client's); SVG rejected outright.
- **Path traversal:** all media served by database ID, never by user-supplied path.
- **CSP:** strict on `/admin`; relaxed only for the `frame-src` the player needs.
- **Secrets:** API tokens encrypted at rest (AES-256-GCM) with a key from
  `/etc/beermate-display-manager/secret.key` (mode 0600, generated at install, never in git).
- **Player scope:** the player endpoint returns *only* playback data — never tokens, cookies,
  credentials, or user records.
- **Logging:** a redaction layer strips cookie values, `Authorization`, tokens, and form
  bodies before anything reaches the journal.

## 6. Scheduling

Server time is authoritative. All schedule maths uses `time.LoadLocation("Europe/Amsterdam")`
with a **tzdata fallback embedded in the binary** (`import _ "time/tzdata"`), because Ubuntu
18.04's system zoneinfo may lag on DST rule changes.

DST correctness is tested explicitly at the March and October transitions: on the spring
transition 02:00→03:00 does not exist, and on the autumn transition 02:00–03:00 occurs twice.
The engine resolves both by comparing wall-clock in the configured zone rather than by adding
durations to UTC.

Precedence: **emergency override > manual override > date-specific override > weekday rule >
global default**.

## 7. Reliability

- systemd `Restart=on-failure`, `RestartSec=5`, journal logging.
- Player caches the last valid published revision in `localStorage`; if the backend is
  unreachable it keeps playing local content (images/video/countdown/clock) and shows a
  discreet offline indicator.
- Every outbound fetch (social, KPI, website) has a timeout, bounded retries, and exponential
  backoff. Failures degrade to cached or branded fallback content — never a blank screen.
- All caches, logs, and backups have hard retention limits.
- Graceful shutdown drains SSE clients and closes the managed browser.

## 8. Testing strategy

- **Go:** table-driven unit tests for auth, CSRF, rate limiting, upload validation, signature
  sniffing, scene/zone validation, atomic publish, revision rollback, scheduling incl. both DST
  transitions, countdown maths, URL validation, backup retention, migration up/down, social
  cache bounds, session-expiry transitions, DPMS abstraction (mock), health payload redaction.
- **Frontend:** component tests for the editors and the player's scene renderer, with a fake
  clock; accessibility assertions on dialogs and forms.
- **Integration:** in-process HTTP server + temp SQLite covering the full flows (bootstrap →
  login → upload → build scene → publish → player receives update).

## 9. Deployment

Single binary to `/opt/BeerMateDisplayManager/`, data in
`/var/lib/beermate-display-manager/`, config in `/etc/beermate-display-manager/`.
`scripts/install-jetson.sh` is idempotent and preserves data and backups on re-run.
`scripts/rollback-jetson.sh` restores the previous binary and database snapshot.
Legacy migration imports the roadmap image, dashboard URL, 14-Aug-2026 countdown, 60 s
durations, and the 08:00–17:00 schedule, while leaving the old kiosk files intact for rollback.

## 10. Explicitly out of scope

- Public internet exposure / port forwarding (Tailscale only).
- Built-in VNC/noVNC (documented as an independently secured remote desktop instead).
- Storing website passwords (manual login + persistent profile instead).
- Scraping platforms that forbid it — social support is adapter-based, official APIs only.
- SVG uploads (no sanitizer in v1).

## 11. Pre-implementation architecture review

Reviewed 2026-07-24, before implementation. Findings and resolutions:

| # | Severity | Finding | Resolution |
|---|---|---|---|
| 1 | High | Publishing clones every scene/zone into a new revision; unbounded growth over years, and media referenced only by an old revision is never reclaimable | `revision_retention` (default 25) + `playlist_revisions.keep` flag exempting the live and operator-pinned revisions |
| 2 | High | Argon2id at 64 MB per hash is a memory-exhaustion vector on a 4 GB device; per-IP/per-account rate limits do not bound *concurrent* hashes | `max_concurrent_hash` semaphore (default 2), enforced independently of rate limiting |
| 3 | High | Persisting `player_state` on every heartbeat means a flash write every few seconds forever | Status held in memory, flushed at most once per `heartbeat_persist_interval` (default 60 s) and immediately on an online/offline edge |
| 4 | High | Schedule model assumed `on_time < off_time`, so it cannot express an evening window crossing midnight — precisely BeerMate's festival/stadium use case. 08:00–17:00 works, which is why this would have shipped unnoticed | `off_time <= on_time` now defines a midnight-crossing window; covered by tests |
| 5 | High | SIL OFL 1.1 requires the licence accompany redistributed fonts; it was absent | `OFL-PlusJakartaSans.txt` and `OFL-Epilogue.txt` bundled alongside the WOFF2 files |
| 6 | Medium | Unbounded concurrent managed-website captures multiply CPU cost in split-screen scenes | `max_managed_captures` (default 2); only currently-visible zones capture |
| 7 | Medium | Nothing prevented deleting media still referenced by a scene | `idx_zones_content` index + publish-time validation + delete protection; player falls back to a branded slide if a reference vanishes at runtime |
| 8 | Medium | SSE subscribers unbounded; forgotten admin tabs accumulate goroutines | `max_sse_clients` (default 16) |
| 9 | Medium | `structuredClone` is Chrome 98; target is 97 | Pinned in the frontend build target and lint configuration rather than left to prose |

## 12. Known trade-offs accepted

| Decision | Cost | Why accepted |
|---|---|---|
| Screenshot streaming for non-embeddable sites | Not interactive; CPU cost per capture | Signage is display-only; hash-gated capture makes static dashboards nearly free |
| Xvfb dependency | One extra apt package | Only way to run a persistent-profile browser invisibly on Chromium 97 |
| `pdftoppm` dependency for PDF import | One extra apt package | Pure-Go PDF rasterisers are immature; conversion is one-time at import, not at playback |
| Argon2id at 64 MB not 128 MB | Slightly lower hashing cost | 4 GB device shared with display; still strong for a Tailscale-only admin surface |
| Preact over React | Smaller ecosystem familiarity | Halves the runtime on a resource-constrained device |
