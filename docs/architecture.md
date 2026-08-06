# Architecture

## Overview

One Go binary serves the admin dashboard, the player, the REST/SSE API and the
health endpoint, and owns the scheduler, display-power control and the managed
browser. SQLite (pure-Go) is the only datastore. The frontend is compiled by Vite
and embedded via `go:embed`.

```
┌──────────────── Jetson Nano (Ubuntu 18.04, ARM64) ────────────────┐
│ systemd: beermate-display-manager.service (user beermate)         │
│  ┌──────────────────────────────────────────────────────────┐    │
│  │ Go binary — 0.0.0.0:8080                                  │    │
│  │  /admin  embedded Preact SPA  (session + CSRF)            │    │
│  │  /player embedded Preact app  (player token)             │    │
│  │  /api/v1 REST + SSE                                       │    │
│  │  /health unauthenticated                                 │    │
│  │  services: playlist scenes media scheduler dpms          │    │
│  │            browser(CDP) social backup realtime(SSE)      │    │
│  │  SQLite (WAL) → /var/lib/.../database                     │    │
│  └──────────────────────────────────────────────────────────┘    │
│     ▲ 127.0.0.1        ▲ 127.0.0.1:9222 (CDP, loopback)  │ xset   │
│  Chromium --kiosk   Managed Chromium on Xvfb :99         ▼        │
│  /player (:0)       persistent profiles                HDMI      │
└──────────────────────────────────────────────────────────────────┘
        ▲ Tailscale (no public port forwarding)
   Windows laptop → http://<tailscale-ip>:8080/admin
```

## Why these choices

- **Go + `modernc.org/sqlite`**: the hard requirement `CGO_ENABLED=0 GOARCH=arm64`
  rules out cgo drivers. Pure-Go SQLite cross-compiles from Windows with no
  device toolchain.
- **Preact + Vite, target `chrome97`**: ~4 KB runtime vs React's ~45 KB, on a
  device where the browser competes for 4 GB. Vite pins the transpile target so
  Chromium 97 never meets an unsupported feature.
- **SSE, not WebSocket**: updates flow server→client only; `EventSource`
  reconnects with backoff for free, which is exactly the recovery required after
  a backend restart. The player reports status via ordinary POSTs.
- **Scene-first model**: every playlist entry is a scene of one or more zones, so
  split-screen needs no special-casing in scheduling, validation, publishing or
  rendering.

## Data model (core tables)

```
users, sessions, login_attempts, audit_log          -- identity & access
settings, credentials(AES-GCM)                        -- config & secrets
media (image/video/pdf/pdf_page)                      -- uploads
websites                                              -- iframe / managed sites
social_feeds, social_posts                            -- feeds & moderation
playlist_revisions → scenes → zones                   -- content
schedule_rules, schedule_overrides, emergency_messages
player_state (single row), backups
```

`playlist_revisions` enforces a single draft via a partial unique index. `scenes`
carry a `stable_id` that survives publish-cloning so history comparisons work.
`zones` carry content by `(content_type, content_ref)` + `config_json`, so a new
slide type needs no schema change; referential integrity is enforced at publish
time and by delete-protection instead of foreign keys.

## Atomic publishing

Editing mutates the single draft revision. Publishing, in one transaction:
stamps the draft as published, then clones it into a fresh draft. The player only
ever reads the newest *published* revision, so a half-saved playlist can never
reach the screen. Rollback appends a new revision (clone of a past one) rather
than deleting newer ones, so it is itself reversible. A concurrency test hammers
reads during repeated publishes and asserts a reader never sees a revision with
zero scenes.

## Request flow (admin write)

1. Request arrives → `securityHeaders` → `requestLogger` → route.
2. `requireAuth(minRole)` looks up the session, checks the role, and — for any
   state-changing method — validates CSRF.
3. Handler validates input, calls a store method (parameterised SQL, often in a
   transaction), records an audit entry, and broadcasts an SSE event.
4. The SSE hub fans the event to the player and admin dashboards; the player (or
   dashboard) refetches the affected data.

## Player loop

The player is one persistent page. It fetches `/api/v1/player/state`, caches it
in `localStorage`, and rotates the playable scenes on a `setTimeout` keyed to each
scene's duration. It subscribes to SSE for prompt updates and reconciles on a slow
timer in case an event is missed. Scenes are keyed by `stable_id` so Preact tears
down the previous DOM entirely (videos are explicitly unloaded), preventing
detached-DOM and stalled-media growth over weeks of uptime. A single one-second
interval drives every countdown and clock. Offline, it keeps playing the cached
playlist's local-only content and shows a discreet badge.

## Background workers

`cmd/.../workers.go` runs three goroutines, each registered in a WaitGroup and
exiting on context cancel (so a leak is a test failure, not a slow memory creep):

- **display-scheduler** (30 s): applies the schedule to DPMS, reasserting state so
  standby is re-applied even if something else woke the panel.
- **player-watchdog** (15 s): notices a missed heartbeat and marks the player
  offline.
- **social-ingest** (60 s): refreshes due feeds, each independently, with backoff.
- **housekeeping** (6 h): prunes sessions, throttle rows, overrides, audit,
  revisions, and sweeps orphaned media (older than an hour, so an in-flight upload
  is never removed).

## Managed browser

Websites that refuse framing or need a login are rendered by a CDP-driven
Chromium on Xvfb, streamed to the player as JPEGs (see
`docs/authenticated-websites.md`). The DevTools port is loopback-only.

## Reliability

systemd `Restart=on-failure`. Graceful shutdown drains SSE clients, flushes
player state and checkpoints the WAL. Every outbound fetch has a timeout and
bounded retries; failures degrade to cached or branded-fallback content. Caches,
logs, backups and revisions all have hard retention limits.
