# CLAUDE.md — BeerMate Display Manager

Guidance for any future Claude session working in this repository. Read this,
then `PROJECT_STATE.md`, then inspect git before making changes.

## Session start checklist

1. Read `CLAUDE.md` (this file).
2. Read `PROJECT_STATE.md` for current phase and status.
3. `git status` and `git log --oneline -10`.
4. Determine the active branch and phase, then continue from the documented state.

## Product summary

A web-based digital signage and display-management platform for BeerMate. An
administrator manages, over Tailscale from a Windows laptop, the fullscreen
content shown on an NVIDIA Jetson Nano. It replaces a Bash script that switched
Chromium tabs with `xdotool` on a timer. The new player is **one persistent
Chromium page** that renders and rotates content itself.

## Architecture

One Go binary serves everything and owns the scheduler, display power and the
managed browser:

- `/admin` — authenticated Preact SPA (session cookie + CSRF).
- `/player` — fullscreen Preact app (per-start player token, injected into the page).
- `/api/v1` — REST + SSE.
- `/health` — unauthenticated, non-sensitive.

The frontend is compiled by Vite and embedded via `go:embed`, so the Jetson
needs no Node. Data is SQLite (pure-Go `modernc.org/sqlite`) so `CGO_ENABLED=0`
cross-compilation works.

Every playlist entry is a **scene** containing one or more **zones**; a fullscreen
slide is a one-zone scene. This makes split-screen first-class rather than a
special case. Publishing is atomic: editing mutates a draft revision, publishing
stamps it and clones a fresh draft inside one transaction, and the player only
reads the newest published revision.

## Repository map

```
cmd/beermate-display-manager/   main, background workers, free-space (OS-specific)
internal/
  api/          HTTP server, middleware, routes, handlers, static/SPA serving
  auth/         Argon2id, sessions, CSRF, login throttling, users
  browser/      CDP client + manager for the managed Chromium (loopback only)
  config/       configuration loading and validation
  content/      scene/zone model, 16 layouts, per-type validators
  dbx/          SQLite open, pragmas, migration runner
  dbx/migrations/  *.sql, forward-only
  display/      DPMS abstraction (xset / mock / noop)
  logging/      slog with a hard redaction layer
  media/        signature validation, thumbnails, store, orphan cleanup
  realtime/     SSE hub
  schedule/     schedule engine with DST-correct wall-clock maths
  secrets/      AES-256-GCM box + key file management
  social/       adapters, moderation service, encrypted credentials
  store/        playlist, player state, settings, schedule, emergency, audit,
                website, backup, legacy migration
  version/      build-stamped version info
  web/          go:embed of the compiled frontend
web/            Vite + Preact + TypeScript source
deploy/         systemd unit, autostart entry, config.example.json
scripts/        install/update/rollback/uninstall/verify, login, backup/restore
assets/brand/   logos, favicons, fonts (extracted from the brandbook)
docs/           architecture, branding, deployment, security, etc.
```

## Technology stack

- Backend: Go 1.23, `modernc.org/sqlite`, `golang.org/x/crypto` (Argon2id),
  `golang.org/x/image` (thumbnails), `github.com/coder/websocket` (CDP),
  `github.com/skip2/go-qrcode`.
- Frontend: Preact 10 + TypeScript, built with Vite, target `chrome97`/`es2019`.
- Fonts: Plus Jakarta Sans (display) and Epilogue (body), local WOFF2, SIL OFL 1.1.

## Build / test / dev commands

Local Go toolchain is pinned under `D:\BeerMate\.toolchain\go` on the dev
workstation. On a normal machine, plain `go`/`npm` work.

```
make check          # gofmt + go vet + go test
make test           # go test ./...
make frontend       # npm ci + typecheck + vite build -> internal/web/dist
make build          # native binary (frontend must be built first)
make build-arm64    # CGO_ENABLED=0 GOOS=linux GOARCH=arm64 production binary
make release        # frontend + build-arm64
make run            # run locally, browser+dpms mocked, on 127.0.0.1:8080
make verify-scripts # bash -n on the deployment scripts
cd web && npm test  # frontend unit tests (vitest)
cd web && npm run lint
```

Production build:

```
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" \
  -o dist/beermate-display-manager ./cmd/beermate-display-manager
```

## Deployment paths

| Thing | Location |
|---|---|
| Application dir | `/opt/BeerMateDisplayManager` |
| Data dir | `/var/lib/beermate-display-manager` |
| Config dir | `/etc/beermate-display-manager` |
| Encryption key | `/etc/beermate-display-manager/secret.key` (0640, never in git/backups) |
| systemd unit | `/etc/systemd/system/beermate-display-manager.service` |
| Service name | `beermate-display-manager` |
| Player autostart | `/home/beermate/.config/autostart/beermate-player.desktop` |

## Environment constraints (the Jetson)

Ubuntu 18.04, ARM64, Chromium ~97, 4 GB RAM shared with the desktop and browser,
flash storage, ethernet to a Teltonika RUT, Tailscale for remote access. Runs
unattended for months.

- **Chromium 97**: build target `es2019`/`chrome97`. No `structuredClone` (98),
  `:has()` (105), container queries (105), CSS nesting (112). ESLint bans the JS
  ones; the Vite target handles the CSS/JS transpilation.
- **Flash wear**: player status is in-memory, flushed at most once a minute;
  session `last_seen` is only written when it moves > 1 minute; SQLite uses
  `synchronous=NORMAL` under WAL.
- **Memory**: Argon2id at 64 MiB behind a concurrency semaphore; bounded SSE
  clients, managed captures, caches, logs, backups, revisions.

## Security model

- Argon2id (64 MiB, t=2) behind a concurrency semaphore that bounds memory
  independently of rate limiting.
- Sessions store only the SHA-256 of the token; HttpOnly, SameSite=Lax, Secure
  when HTTPS; idle + absolute expiry; server-side revocation.
- CSRF double-submit, validated in middleware for every state-changing method.
- Login throttling on two buckets (per-IP and per-username).
- Uploads: magic-byte signature is authoritative; SVG rejected; generated
  storage names; served by DB id, never by path.
- CSP per route (locked on /admin, frame-permissive on /player).
- API tokens AES-256-GCM encrypted with the credential row id as AAD.
- The player endpoint returns only playback data — never tokens/cookies/users.
- Logging redaction layer strips cookies, Authorization, tokens, form bodies.
- CDP debug port is loopback-only; config refuses a non-loopback address.

## Website session model

Websites are `iframe` (embeddable, cheap) or `managed` (CDP-driven Chromium,
screenshot-streamed). Authenticated sites are always managed. Login is manual:
`prepare-login` opens the site on the real display, the admin logs in by hand,
`finish-login` closes it (flushing cookies to the persistent profile) and
validates that the target opens without a login redirect. Passwords are never
stored. Expired sessions show a branded fallback, not a login form.

## Social feed model

Adapter per platform (`rss`, `atom`, `json`, `youtube`, `webhook`, `manual`),
official feeds only. Tokens encrypted at rest, never logged or sent to the
player. Manual moderation is the default. Fetches back off exponentially; the
cache is capped per feed; post text is HTML-stripped.

## Split-screen architecture

16 layouts in `internal/content/layout.go`. Fixed layouts define authoritative
zone geometry (client-supplied rects are overwritten); the custom grid takes zone
geometry from the scene, validated for bounds and overlap. Zones carry content by
`(content_type, content_ref)` so a new type needs no schema change.

## How to add …

- **A slide type**: add a `Type*` constant and config struct + validator in
  `internal/content/types.go`; render it in `web/src/player/ZoneRenderer.tsx`.
- **A scene layout**: add a `Layout` to the `layouts` slice in
  `internal/content/layout.go` (must tile to 100% with no overlap — a test
  enforces this).
- **A social adapter**: implement `social.Adapter` and `Register(...)` it in an
  `init` in `internal/social/adapters.go`.
- **An authenticated website**: it is data, not code — created via the API /
  dashboard; the managed-browser flow handles it.

## Database migrations

Forward-only SQL files in `internal/dbx/migrations/`, named `NNNN_name.sql`,
applied in one transaction each by `dbx.Migrate`. Rollback is by restoring the
pre-upgrade database snapshot that `update-jetson.sh` always takes, not by
reversible DDL. Add a new file with the next number; never edit an applied one.

## Coding / API conventions

- Timestamps stored as RFC3339 UTC TEXT; booleans as 0/1 INTEGER.
- Handlers return the uniform `{"error":...,"detail":...}` shape via `writeError`.
- Stores take a `now func() time.Time` and expose `SetClock` for deterministic
  tests.
- Every outbound fetch has a timeout and bounded retries; failures degrade to
  cached or branded-fallback content.

## Verification before commit

Run `make check` (fmt, vet, test), `cd web && npm run typecheck && npm test &&
npm run lint`, and for a release `make release` plus a check that the ARM64 ELF
reports machine `0xb7`. Confirm no secrets are staged (`git diff --cached`).

## Known limitations

- SVG uploads are rejected (no sanitiser in v1).
- PDF import needs `pdftoppm` on the device; the conversion worker is stubbed for
  wiring but page rasterisation runs on-device (documented in troubleshooting).
- Social platform adapters cover official feeds; Instagram/LinkedIn/etc. require
  the operator to supply an official API/JSON endpoint or use RSS/webhook.
- The managed browser requires Xvfb and a Chromium binary on the device.

## Updating project state

After each meaningful phase, update `PROJECT_STATE.md`: completed vs remaining
features, known issues, last build/verification results, next actions. Keep it
factual and concise — not a diary.
