# API reference

Versioned under `/api/v1`. JSON in, JSON out. Errors use a uniform shape:

```json
{ "error": "human message", "detail": [ { "field": "…", "message": "…" } ] }
```

`detail` is present for validation failures (422).

## Authentication

- **Admin**: session cookie `beermate_session` (HttpOnly) plus, on every
  state-changing request, the `X-BeerMate-CSRF` header carrying the value of the
  `beermate_csrf` cookie. Get the current token from `GET /api/v1/auth/me`.
- **Player**: the `X-BeerMate-Player` header (or `?token=` for the event stream),
  carrying the per-start token injected into the player document.

Roles: `viewer` < `editor` < `admin`. Each endpoint below notes its minimum role.

## Auth & setup

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/bootstrap/status` | — | Is first-run setup needed? |
| POST | `/api/v1/bootstrap` | — | Create the first administrator (once) |
| POST | `/api/v1/auth/login` | — | Sign in |
| POST | `/api/v1/auth/logout` | session | Sign out |
| GET | `/api/v1/auth/me` | viewer | Current user + CSRF token |
| POST | `/api/v1/auth/password` | viewer | Change own password |
| GET/DELETE | `/api/v1/auth/sessions` | viewer | List / revoke other sessions |
| GET/POST | `/api/v1/users` | admin | List / create users |
| GET/PATCH/DELETE | `/api/v1/users/{id}` | admin | Manage a user |

## Content

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/layouts` | viewer | The 16 layouts |
| GET | `/api/v1/content-types` | viewer | Types, transitions, limits |
| GET | `/api/v1/playlist?revision=draft\|published\|<id>` | viewer | Revision + scenes |
| POST | `/api/v1/playlist/publish` | editor | Publish the draft |
| POST | `/api/v1/playlist/discard` | editor | Reset the draft to live |
| POST | `/api/v1/playlist/reorder` | editor | Reorder scenes |
| POST | `/api/v1/playlist/bulk` | editor | Bulk enable/disable/duration |
| GET | `/api/v1/playlist/revisions` | viewer | Revision history |
| POST | `/api/v1/playlist/rollback` | admin | Roll back to a revision |
| POST | `/api/v1/scenes` | editor | Create a scene |
| GET/PUT/DELETE | `/api/v1/scenes/{id}` | editor | Manage a scene |
| POST | `/api/v1/scenes/{id}/duplicate` | editor | Duplicate a scene |

A scene save returns `422` with field-level `detail` when invalid; the scene is
still stored (so work in progress is not lost), and publishing refuses invalid
enabled scenes.

## Media

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/media?kind=&limit=&offset=` | viewer | List media + usage |
| POST | `/api/v1/media` | editor | Multipart upload (field `file`) |
| GET/PATCH/DELETE | `/api/v1/media/{id}` | viewer/editor | Get / rename / delete |
| GET | `/media/file/{id}` | viewer or player | Original file |
| GET | `/media/thumb/{id}` | viewer or player | Thumbnail |

Uploading a PDF also rasterises its pages into `pdf_page` items. Deleting media a
scene uses returns `409` unless `?force=true`.

## Websites

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET/POST | `/api/v1/websites` | viewer/editor | List / create |
| GET/PUT/DELETE | `/api/v1/websites/{id}` | viewer/editor | Manage |
| POST | `/api/v1/websites/{id}/prepare-login` | editor | Open interactive login |
| POST | `/api/v1/websites/{id}/finish-login` | editor | Finish + validate |
| POST | `/api/v1/websites/{id}/validate` | editor | Revalidate session |
| POST | `/api/v1/websites/{id}/clear-session` | editor | Clear stored session |
| GET | `/api/v1/websites/{id}/frame` | viewer or player | Redirect (iframe) or JPEG capture (managed) |

## Social

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET/POST | `/api/v1/social/feeds` | viewer/editor | List / create (token write-only) |
| GET `/posts` · POST `/refresh` `/enable` `/disable` · DELETE | `/api/v1/social/feeds/{id}/…` | viewer/editor | Manage a feed |
| POST/DELETE | `/api/v1/social/posts/{id}` | editor | Moderate / pin / delete a post |
| GET | `/api/v1/player/social/{id}` | player | Approved posts for a feed |

## Schedule & display

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/schedule` | viewer | Rules, overrides, current decision |
| POST | `/api/v1/schedule/rules` | editor | Upsert a rule |
| DELETE | `/api/v1/schedule/rules/{id}` | editor | Remove/disable a rule |
| POST/DELETE | `/api/v1/schedule/override` | editor | Manual wake/sleep / clear |
| GET | `/api/v1/emergency` | viewer | List + active message |
| POST | `/api/v1/emergency/raise` | admin | Raise a priority message |
| DELETE | `/api/v1/emergency/{id}` | admin | Dismiss |

## Status, backups, migration

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/overview` | viewer | Dashboard summary |
| GET | `/api/v1/audit` | admin | Audit log |
| GET/POST | `/api/v1/settings` | viewer/editor | Read / set allowed settings |
| GET/POST | `/api/v1/backups` | viewer/editor | List / create |
| GET `/download` `/verify` · POST `/restore` · DELETE | `/api/v1/backups/{id}/…` | viewer/editor/admin | Manage a backup |
| POST | `/api/v1/migrate/legacy` | admin | Import the old kiosk (draft) |
| GET | `/api/v1/qr?data=&ec=&fg=&bg=` | viewer or player | QR PNG |

## Realtime

- `GET /api/v1/events` (admin session) — SSE for dashboards.
- `GET /api/v1/player/events?token=` (player) — SSE for the player.

Events: `hello`, `ping`, `playlist`, `schedule`, `emergency`, `website`,
`social`, `player`, `media`. Each carries a small JSON payload; clients refetch
the affected resource.

## Player

- `GET /api/v1/player/state` — the published playlist, display state, emergency,
  server time. **Only playback data — no secrets.**
- `POST /api/v1/player/heartbeat` — status report; echoes the server revision.

## Health

- `GET /health` — overall state, build, DB, revision, player, storage, display,
  browser control, social summary, website warnings, subscribers. No secrets.
