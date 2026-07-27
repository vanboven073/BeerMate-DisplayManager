# Authenticated websites

Some content lives behind a login (the BeerMate dashboard, anything behind SSO)
or refuses to be framed. These are rendered by a **managed Chromium** driven over
the Chrome DevTools Protocol, and their login sessions are prepared manually and
reused from a persistent browser profile. **Passwords are never stored.**

## The two render modes

- **iframe** — an embeddable, public site. Rendered directly in the player's
  iframe. Cheap. No managed browser involved.
- **managed** — a site that blocks framing (`X-Frame-Options` /
  `frame-ancestors`) or requires a login. Rendered by the managed Chromium on a
  virtual display (Xvfb) and streamed to the player zone as JPEG screenshots.
  Captures are hash-gated and interval-limited, so a static dashboard costs almost
  nothing.

Marking a website "requires authentication" forces managed mode.

## Why manual login

MFA, SSO, cookie-consent banners, device approval and captcha all work when a
real human drives a real browser. Storing a username and password would be both a
security liability and fragile against any of those steps. So the flow is: a human
logs in once, and Chromium's own persistent profile keeps the resulting cookies,
localStorage and site-issued tokens.

## The workflow

1. Create a website slide and mark it **requires authentication**.
2. Click **Prepare login** (or `scripts/start-interactive-login.sh <id>`). The
   backend stops the capture instance for that profile and opens the site
   **interactively on the Jetson's real display**.
3. Log in there — locally with a keyboard and mouse, or over an **independently
   secured remote desktop** reached via Tailscale. Complete MFA/SSO/consent as
   needed.
4. Click **Finish login** (or `scripts/stop-interactive-login.sh <id>`). The
   backend closes the interactive instance (which flushes the cookies to the
   on-disk profile) and **validates** by loading the target URL and checking it
   does not redirect to a login page.
5. The player reuses the stored profile. The session survives Chromium restart,
   backend restart and Jetson reboot because it lives in
   `/var/lib/beermate-display-manager/browser-profiles/<profile>/`.

## Session states

`none → preparing → active`, and `active → expired / reauth_required / error`.
The dashboard shows the state, last successful load, last validation and last
login for each authenticated site.

## When a session expires

The player does **not** sit on a login page. The backend detects the login
redirect (an operator-supplied pattern wins, then known login path/query markers
and identity-provider hosts — the detector errs toward catching redirects, since
a false positive shows a branded fallback but a false negative would leave a login
form on the wall). It marks the site `reauthentication required`, shows a branded
fallback slide, raises a dashboard warning (also surfaced in `/health` as
`website_session_warnings`), and playback continues.

## Profiles

- **Default shared profile** (`default`) for sites that can share cookies.
- **Isolated profile per site** by giving the website a distinct profile id
  (letters, digits, dash, underscore — validated to stay a single path segment).

`Clear session` deletes the stored cookies; `Reset profile` removes the whole
profile directory.

## Security notes

- The DevTools port is `127.0.0.1` only and never exposed over Tailscale.
- Chromium runs as the unprivileged `beermate` user, so the sandbox stays on
  (`--no-sandbox` is never used).
- Cookie values, tokens and form contents are never logged; browser errors are
  stripped of query strings before storage because a URL can carry a one-time
  token.
- Profiles are excluded from downloadable backups, so an archive cannot replay a
  login.

## Requirements on the device

`chromium-browser` and `xvfb`. `browser_enabled: true` in the config, with
`browser_debug_addr` on a loopback host (the service refuses to start otherwise).
