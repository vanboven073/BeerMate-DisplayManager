# PROJECT_STATE.md

Factual snapshot of the BeerMate Display Manager. Update after each meaningful
phase. Not a diary.

## Current

- **Branch:** `feature/display-manager-initial-implementation` (unpushed)
- **Phase:** first full production deployment done, 4 August 2026. The Jetson
  ran `fdf9e98` and now runs `bc34807`. Four defects were found and fixed on real
  hardware in the process; see "First production deployment" below.
- **Last production build:** `bc34807`, linux/arm64, verified aarch64 ELF
  (machine 0xb7), 12.4 MB stripped, frontend embedded.
- **Backend tests:** passing (`go test ./...`), gofmt and `go vet` clean.
- **Frontend:** typecheck clean, 53 unit tests passing, ESLint clean.

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
- Scene editor (`web/src/admin/scene/`): create and edit scenes across all 16
  layouts and every content type, with per-type forms mirroring the Go
  validators, media/website/feed pickers, zone styling, custom-grid rects and
  inline server field errors. "Add to playlist" shortcuts on Media and Websites.
- Player: single persistent page, all zone renderers, transitions, offline
  cache, resource cleanup, standby, emergency overlay, branded fallbacks.
- Deployment: systemd unit, autostart, 12 scripts (`set -Eeuo pipefail`, all
  `bash -n` clean), Makefile, config example.
- QR generation endpoint; per-request player token injection.

## Partially completed

- **PDF import**: complete but synchronous — rasterisation via `pdftoppm` runs
  inside the upload request under a 3-minute bound. Fine for the deck sizes in
  use; a background worker is the natural next step, not a gap.
- **Social platform breadth**: official-feed adapters implemented; first-party
  Instagram/LinkedIn/Facebook/X adapters would each need the operator's API token
  and are structured as future adapters.

## Code review #2 — findings and remediation

Reviewed the full branch. Two confirmed with reproducing tests before fixing.

Fixed (critical):

- `realtime.Hub.Broadcast` selected targets under a read lock and sent after
  releasing it, so a subscriber disconnecting in that window was sent on after
  its channel closed — `panic: send on closed channel`. Sends now happen under
  the read lock. Regression test in `internal/realtime/hub_test.go`.
- The worker supervisor recovered panics at goroutine level, so a recovered panic
  killed that worker for the life of the process — the display scheduler would
  silently stop applying the schedule. Recovery is now per tick.

Fixed (important):

- SQLite `synchronous`, `temp_store` and `cache_size` were issued once after
  `Open`, configuring only one of four pooled connections; the other three ran
  `synchronous=FULL`, defeating the documented flash-wear mitigation for most
  writes. Moved into the DSN. Regression test in `internal/dbx/db_test.go`.
- `/player` is unauthenticated and embedded the player token in the HTML for any
  caller, so the token authenticated nothing — any tailnet peer could read it and
  pull media and managed screenshots of authenticated sites. Now issued only to
  loopback (the kiosk) or an authenticated session.
- `GET /api/v1/backups/{id}/download` had no role check: a viewer could download
  the whole database, including Argon2 hashes. Now admin-only.
- `secrets.CheckKeyPermissions` demanded 0600 while the installer writes 0640
  (root:beermate, deliberate). Every production start logged a false warning. The
  check now rejects world access and group write, and accepts 0640.
- `dpms_enabled` and `max_managed_captures` were declared, defaulted, env-wired
  and validated but never read. Both are now enforced.
- Cloning a revision stamped the new draft's `updated_at` with the publish time,
  so the dashboard reported unpublished changes the instant a publish finished;
  `Reorder` conversely left timestamps untouched, hiding a real change. Both
  fixed, with regression tests.
- `IsFeedReferenced` matched `"feed_id":5` as a substring, so feed 50 blocked
  deleting feed 5. Now parsed exactly; `ReferencedMediaIDs` no longer relies on a
  `LIKE '%_id%'` filter whose `_` was an unintended wildcard.
- The multipart upload loop treated any `NextPart` error as EOF, reporting 201
  Created for a truncated batch. Now distinguishes EOF from failure.
- `internal/api` and `internal/realtime` had no tests at all. Added
  `internal/api/api_test.go` covering role enforcement, CSRF, unauthenticated
  access, player-token handling and health-response secrecy.

Also fixed: non-constant-time player-token comparison; a `writeError` returning an
error envelope under HTTP 200; dead `version` placeholder and an unused-import
prop in `handlers_content.go`; `truncateStr` splitting UTF-8 runes; a stale
"admin-only" comment in `routes.go`.

## Scene editor (first on-device run)

The first real deployment could not publish anything. The publish path was never
at fault: `POST /api/v1/scenes` existed, was role-guarded and tested, but nothing
in the SPA ever called it — the only two callers of `/api/v1/scenes` were
`duplicate` and `delete`. With no way to create a scene, the draft revision
stayed empty, so the publish button rendered disabled and the store would have
refused with "no enabled scenes; the display would be blank".

Added `web/src/admin/scene/` (model, fields, pickers, per-type content forms,
ZoneForm, SceneEditor) plus entry points in the playlist, media and website
views. The model layer is the single place the `DisallowUnknownFields` contract
with `internal/content/types.go` is expressed; a test asserts every default
config against the Go struct field lists.

Also fixed while tracing it: `ZoneRenderer` had no `case 'social'`, so a social
zone rendered "Unsupported content type" despite the adapters, moderation and
`/api/v1/player/social/{id}` all being implemented. Added `SocialZone`.

Verified end-to-end against a local instance: create → publish → player state
carries the revision; split-screen, edit-with-layout-switch (position and
stable_id preserved) and the 422 field-error path all confirmed.

## First production deployment (4 August 2026)

Upgraded the Jetson from `fdf9e98` to `bc34807` with `update-jetson.sh`, run from
the staged bundle. The deployment guide held up; the manual bundle staging in
stage 2 is still the most error-prone step.

Proven on real hardware:

- The Xvfb unit installs, enables and starts, and
  `systemctl show -p JoinsNamespaceOf` confirms systemd accepts the `/tmp`
  namespace sharing the capture Chromium depends on.
- The scene editor and the publish path work end to end. An operator created a
  scene and published it; `import-legacy-config.sh` then imported the roadmap
  image, the dashboard website and the countdown as a draft, which published as
  revision 4. `playlist_revision` had been `0` since install.
- The legacy Bash kiosk is retired: its autostart entry is disabled and the whole
  old setup is backed up under `backups/legacy-kiosk-*`.

Four defects surfaced only because content finally reached the screen, and are
fixed in the commits named above:

1. Player media requests were unauthenticated, so every image 401'd and rendered
   "Image unavailable" (`4a4f874`).
2. `useServerClock` returned an unstable object, which made the player refetch
   state on every render — ~1.5/s — and that churn reset the scene dwell timer,
   freezing rotation permanently (`522ef22`).
3. RSS/Atom feeds never extracted images, so a social zone was text-only
   (`bc34807`).
4. Tiled social cards let the image take the whole card, clipping the caption
   (`bc34807`).

Environment correction: the device runs **Chromium 112.0.5615.49**, not ~97. The
`es2019`/`chrome97` build target and the ESLint bans are therefore stricter than
the hardware requires. Left as-is deliberately — it costs nothing and keeps the
player portable — but the constraint documented in `CLAUDE.md` is looser in
practice.

## Remaining

- Social zone display work: no paging or scrolling through posts, and the tiled
  templates are fixed at three columns, so the layout does not adapt to the
  configured post count. Rows now have definite heights so nothing is clipped,
  but this is only half done.
- Remote website login: logging into an authenticated site still needs a keyboard
  at the Jetson. Design agreed (screenshot polling plus CDP input forwarding, on
  the Xvfb display) but not built.
- Rotate the administrator password: it was exposed in a terminal transcript
  during deployment.
- Code review #3 (strict final) with remediation.
- Push branch.

## Known issues

- Restarting `beermate-xvfb` on its own leaves the service holding a stale `/tmp`
  namespace, because it reaches the X socket via
  `JoinsNamespaceOf=beermate-xvfb.service`. Always restart
  `beermate-display-manager` afterwards. `update-jetson.sh` orders this correctly;
  a human running `systemctl restart beermate-xvfb` will not.
- `run-player.sh` sends Chromium's stdout and stderr to `/dev/null`, so a player
  that will not start gives an operator nothing to diagnose from. It also has no
  guard against a second instance being launched against the same
  `--user-data-dir`, which Chromium answers with "Opening in existing browser
  session" and an immediate exit — read as a crash loop by the supervisor.
- Instagram video posts cannot be played. rss.app (and Instagram generally)
  expose only the cover frame as `<media:content medium="image">`; no video URL
  exists in the feed. Uploading MP4s to the media library is the only route to
  moving footage.
- Social media URLs are refreshed on every poll, which keeps them alive while a
  post is still in the feed window. A post that has aged out of the feed but is
  still approved or pinned will eventually show a broken image, because its signed
  CDN URL stops being renewed. Ingesting images into the media store is the
  durable fix if that becomes a problem.
- `install-jetson.sh` expects the binary at the release-directory root while
  `make` writes it to `dist/`, so the operator must stage a bundle by hand. A
  `make package` target would close this.
- `rollback-jetson.sh` only recognises `.prev` binaries and `pre-update-*.db`
  snapshots, both produced by `update-jetson.sh`. Re-running `install-jetson.sh`
  as an upgrade path leaves nothing to roll back to.
- Restore uses rename-over-open-file, correct/atomic on Linux; the corresponding
  test is skipped on Windows (dev only).
- `web/node_modules` contains a stray vendored Go file that appears in
  `go test ./...` output as "no test files"; harmless, gitignored.
- `cdpManager` keeps one capture instance, so alternating captures between two
  managed profiles relaunches Chromium each time. Concurrency is now bounded, but
  a split-screen scene mixing two managed sites will thrash; per-profile instances
  are the fix if that configuration is ever used.
- `/health` is unauthenticated and walks the whole data directory on every call.
  Cheap today; worth caching if anything starts polling it hard.
- The race detector cannot run on the Windows dev workstation (`-race` needs
  cgo). The hub regression test reproduces the panic without it, but a Linux CI
  run with `-race` would be worth having.

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

**Deployed and running** on the Jetson as of 4 August 2026, version `bc34807`,
reached over Tailscale at `100.75.229.42:8080`. `verify-installation.sh` passes
every critical check including the new Xvfb one. Repeat deployments use
`update-jetson.sh` from a staged bundle; `install-jetson.sh` is only for a fresh
device.

Not yet exercised on hardware: managed/authenticated website capture. Xvfb and the
namespace sharing are confirmed present, but no managed site has been rendered
through them, so that path is installed rather than proven.

## Next recommended actions

1. Rotate the administrator password (exposed during deployment).
2. Social zone display work: adapt the tiled layout to the configured post count
   and page or scroll through more posts than fit.
3. Remote website login, per
   `docs/superpowers/specs/2026-08-04-remote-website-login-design.md`.
4. Add a `make package` target so the bundle staging in deployment stage 2 stops
   being manual.
5. Run code review #3 (strict), remediate, and push the branch.
