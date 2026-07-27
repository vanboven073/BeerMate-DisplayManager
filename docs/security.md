# Security

The admin surface is reachable only over Tailscale, but it is treated as
security-sensitive regardless. This document lists the threat model and every
control.

## Trust boundaries

- **Player endpoint** (`/api/v1/player/*`, `/player`): authorised by a per-start
  token injected into the player document. Returns only playback data — never
  tokens, cookies, credentials or user records.
- **Admin surface** (`/admin`, `/api/v1/*`): session cookie + CSRF, role-gated.
- **Health** (`/health`): unauthenticated, deliberately carries nothing sensitive.
- **DevTools / CDP** (`127.0.0.1:9222`): loopback only. The config layer refuses
  to start with a non-loopback debug address, because that port grants full
  control of the browser and every stored cookie.

## Authentication

- **Passwords**: Argon2id, 64 MiB / t=2 / p≤4, per-password random salt, PHC
  string format. Verification reads parameters from the stored hash, so a
  parameter change does not invalidate existing hashes; weaker hashes are
  transparently upgraded on next login.
- **Memory bound**: a semaphore (`max_concurrent_hash`, default 2) caps concurrent
  hashes. Rate limiting is per-IP/per-account and does not bound concurrency, so
  without the semaphore a burst of logins could exhaust the 4 GB device — the
  semaphore is the actual defence.
- **Username enumeration**: `Authenticate` verifies a real dummy hash when the
  user does not exist, so the failure path's timing matches a wrong password. A
  test asserts the dummy hash decodes, so the defence cannot silently become a
  no-op. All login failures return one message.

## Sessions

- Opaque 256-bit token; only its SHA-256 is stored, so a database or backup copy
  cannot be replayed as a live session.
- `HttpOnly`, `SameSite=Lax`; `Secure` when served over HTTPS (auto-detected, or
  pinned via config). The `__Host-` prefix is intentionally not used because the
  Jetson serves plain HTTP inside the Tailscale tunnel.
- Idle timeout **and** absolute lifetime; server-side revocation. Changing a
  password revokes all other sessions. Disabling a user revokes their sessions
  immediately.

## CSRF

Double-submit: the CSRF token is derived from the session's server-side secret
and echoed by the SPA in a header. Validated constant-time in the auth
middleware for **every** state-changing method, so a new endpoint cannot forget
it. Logout is the one deliberate exception (idempotent, only ever removes access).

## Login throttling

Two independent buckets per attempt: source IP and submitted username. The IP
bucket stops a password spray across many accounts; the username bucket stops a
distributed attack on one account. Exponential lockout, capped; the window resets
so a slow trickle of genuine typos never accumulates into a permanent lockout.

## Uploads

- The **magic-byte signature is authoritative**. Filename and Content-Type are
  trivially forged, so accepting either would let a script be stored under an
  image extension and served back with an image MIME type.
- **SVG is rejected** outright (it is XML that can carry `<script>` and external
  references — stored XSS). Executables, archives and HTML are rejected.
- Storage filenames are **generated**, never derived from the client's, removing
  traversal, collision and encoding issues in one step. Files are served by
  database id, never by a client-supplied path; a `safeJoin` guard rejects any
  non-plain filename even from the database.
- Uploads read at most `cap+1` bytes so an unbounded stream cannot fill the disk
  before the size check; partial files are removed on rejection.

## Injection defences

- **SQL**: parameterised queries throughout; the few dynamic table/column names
  are compile-time constants (`#nosec G201` annotated at those sites).
- **Command**: the only exec calls are `xset`, `pdftoppm` and Chromium, always
  with fixed argument arrays and validated inputs (profile ids and filenames are
  restricted to safe character sets).
- **Path traversal**: media, thumbnails, backups and browser profiles all pass
  through validators that reject `..`, separators and non-plain names.
- **Stored/reflected XSS**: colour fields accept only strict hex (they are
  interpolated into inline styles); social post text is HTML-stripped; the
  server-injected bootstrap JSON escapes `<`, `>`, `&`, `/`; config decoding
  rejects unknown fields.
- **URL validation**: absolute http(s) only, no embedded credentials, HTTPS
  required unless the operator opts into HTTP. (DNS-rebinding SSRF for
  server-fetched URLs is a documented residual — see Residual risks.)

## Secrets

- Third-party API tokens are **AES-256-GCM** encrypted with the credential row id
  as additional authenticated data, so a token cannot be lifted from one row and
  decrypted under another. The key lives in
  `/etc/beermate-display-manager/secret.key` (0640), generated at install with
  `O_EXCL`, never committed and never included in a downloadable backup.
- Website login sessions are stored as Chromium profile cookies on disk, outside
  the database and outside backups. Passwords are never stored.

## Logging

A redaction layer in the slog handler strips any attribute whose key looks
sensitive (password, token, cookie, authorization, secret, csrf, …) at any nesting
depth, and browser/social errors are stripped of query strings before storage
because a navigated URL can carry a one-time token. The request logger omits the
query string entirely.

## Response headers

Per-route CSP (locked on `/admin`, frame-permissive on `/player` for embeddable
sites, `default-src 'none'` on the API); `X-Content-Type-Options: nosniff`,
`X-Frame-Options: SAMEORIGIN`, `Referrer-Policy: same-origin`,
`Permissions-Policy` disabling camera/mic/geolocation/etc.

## Audit

Sensitive actions (login, bootstrap, password change, publish, rollback, user
and website management, emergency, backups, restore, legacy import) are recorded
with actor, action, target and IP. The log is pruned to a bounded size.

## Remote access for website login

There is **no built-in VNC/noVNC**. Manual website login is completed either
locally at the Jetson with a keyboard and mouse, or through an independently
secured remote desktop reached over Tailscale. This keeps an unauthenticated
remote-control surface out of the application.

## Residual risks (documented, accepted for v1)

- **SSRF via DNS rebinding**: URL *format* is validated, but a hostname can
  resolve to a private address after validation. Server-side fetches (KPI APIs,
  social JSON) are operator-configured, so this is a trusted-input surface;
  network-level egress restriction on the Jetson is the recommended mitigation.
- **SVG**: unsupported until a sanitiser is added.
- **Managed browser**: renders operator-configured sites; a hostile site cannot
  reach the loopback DevTools port, but runs with the profile's stored cookies by
  design.
