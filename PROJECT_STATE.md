# PROJECT_STATE.md

Factual snapshot of the BeerMate Display Manager. Update after each meaningful
phase. Not a diary.

## Current

- **Branch:** `feature/display-manager-initial-implementation`
- **Phase:** documentation complete; entering final code review and remediation.
- **Last production build:** linux/arm64, verified aarch64 ELF (machine 0xb7),
  11.8 MB stripped, frontend embedded.
- **Backend tests:** passing (`go test ./...`).
- **Frontend:** typecheck clean, 7 unit tests passing, ESLint clean.

## Completed features

- Config, structured logging with redaction, SQLite open + forward-only migrations.
- Auth: Argon2id (semaphore-bounded), sessions (hashed tokens, idle+absolute
  expiry, revocation), CSRF, two-bucket login throttling, roles, last-admin guard.
- Content model: scene/zone, 16 split-screen layouts, all slide types with
  validators, revisions, atomic publish, rollback, retention/pruning.
- Media: signature validation, SVG/executable rejection, generated names,
  thumbnails, orphan cleanup, usage reporting.
- Realtime SSE hub (bounded queues, subscriber cap); player state (in-memory,
  throttled persistence); heartbeat; health endpoint (no secrets).
- Managed browser (CDP over WebSocket, loopback-only); authenticated website
  sessions (prepare/finish/validate/clear, persistent profiles, redirect
  detection, branded fallback).
- Social: adapters (rss/atom/json/youtube/webhook/manual), encrypted tokens,
  moderation queue, backoff, per-feed cache cap, HTML-stripped text.
- Scheduling: DST-correct wall-clock engine (23h/25h days, ambiguous hour,
  overnight windows), overrides with expiry, precedence; DPMS abstraction.
- Backups: checksummed gzip tar with manifest, verify, restore with pre-restore
  snapshot and integrity check, retention. Legacy kiosk import (non-destructive).
- Admin dashboard: overview, playlist, media, websites, social, schedule,
  backups, settings; login + first-run bootstrap. Full BeerMate branding,
  accessible (keyboard reorder, non-colour status, focus-visible, reduced motion).
- Player: single persistent page, all zone renderers, transitions, offline
  cache, resource cleanup, standby, emergency overlay, branded fallbacks.
- Deployment: systemd unit, autostart, 12 scripts (`set -Eeuo pipefail`, all
  `bash -n` clean), Makefile, config example.
- QR generation endpoint; per-request player token injection.

## Partially completed

- **PDF import**: type detection, validation and the slide model are in place;
  on-device page rasterisation via `pdftoppm` is documented but the background
  conversion worker is not yet wired (upload accepts PDFs and stores them; page
  splitting is the remaining piece).
- **Social platform breadth**: official-feed adapters implemented; first-party
  Instagram/LinkedIn/Facebook/X adapters would each need the operator's API token
  and are structured as future adapters.

## Remaining

- Code review #2 (post-implementation) and #3 (strict final) with remediation.
- Final 28-point verification pass.
- Push branch; produce exact deployment instructions (in README + docs).

## Known issues

- Restore uses rename-over-open-file, correct/atomic on Linux; the corresponding
  test is skipped on Windows (dev only).
- `web/node_modules` contains a stray vendored Go file that appears in
  `go test ./...` output as "no test files"; harmless, gitignored.

## Architecture decisions

See `docs/superpowers/specs/2026-07-24-display-manager-design.md` (incl. the
pre-implementation review table) and `docs/architecture.md`.

## Security decisions

Documented in `docs/security.md` and `CLAUDE.md`. Pre-implementation review
resolved: revision retention, Argon2 concurrency bound, heartbeat write
throttling, midnight-crossing schedule, OFL licence files, capture/SSE bounds.

## Branding status

Logos, wordmarks, favicons extracted from the official brandbook as tight-viewBox
SVGs; Plus Jakarta Sans + Epilogue bundled (WOFF2, OFL 1.1). Palette, radii and
typography tokens from the brandbook applied throughout.

## Deployment status

Scripts written and syntax-checked; **not yet run on a physical Jetson**. Device
verification (`scripts/verify-installation.sh`) is the remaining on-device step.

## Next recommended actions

1. Run code review #2, remediate critical/high (and medium unless justified).
2. Run code review #3 (strict), remediate.
3. Final verification checklist; commit and push.
4. On the Jetson: run `install-jetson.sh`, then `verify-installation.sh`, then
   `import-legacy-config.sh`; review and publish the imported draft.
