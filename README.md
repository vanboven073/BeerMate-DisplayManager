<p align="center">
  <img src="assets/brand/logo/beermate-wordmark-full.svg" alt="BeerMate" width="320">
</p>

<h1 align="center">Display Manager</h1>

<p align="center">
  Web-based digital signage for the BeerMate Jetson Nano — managed remotely over
  Tailscale, rendered by one persistent Chromium page.
</p>

---

## What it is

A single Go service that replaces the old Bash kiosk (which switched Chromium
tabs with `xdotool`) with a managed signage platform:

- a **secure admin dashboard** reachable over Tailscale from a Windows laptop,
- a **fullscreen player** that renders and rotates content itself,
- **scenes and split-screen**, images, videos, PDFs, websites (embeddable and
  authenticated), countdowns, clocks, KPIs, QR codes, announcements, events,
  social feeds, and tickers,
- **scheduling** with correct daylight-saving handling, display power (DPMS)
  control, live updates, offline playback, backups, and a one-command migration
  from the current kiosk.

The frontend is compiled and embedded in the binary, so the Jetson never needs
Node. The whole thing ships as one ~12 MB `linux/arm64` file.

## Quick start (development)

Requires Go 1.23+ and Node 20+.

```bash
make frontend        # build the embedded frontend
make run             # run on http://127.0.0.1:8080 (browser + DPMS mocked)
```

Open <http://127.0.0.1:8080/admin> and create the first administrator.
The player is at <http://127.0.0.1:8080/player>.

## Build the production binary

```bash
make release         # frontend + CGO_ENABLED=0 GOOS=linux GOARCH=arm64 build
# -> dist/beermate-display-manager  (aarch64 ELF, frontend embedded)
```

## Deploy to the Jetson

From a QBee root shell, with the release directory present:

```bash
sudo ./scripts/install-jetson.sh
sudo ./scripts/verify-installation.sh
```

Then, from your laptop over Tailscale, open `http://<tailscale-ip>:8080/admin`
and create the administrator. Full steps, including Tailscale setup and the
legacy migration, are in [docs/jetson-deployment.md](docs/jetson-deployment.md).

## Documentation

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) | System design, data model, request flow |
| [docs/security.md](docs/security.md) | Threat model and every control |
| [docs/jetson-deployment.md](docs/jetson-deployment.md) | Install, Tailscale, migration, updates |
| [docs/authenticated-websites.md](docs/authenticated-websites.md) | The manual-login / persistent-profile flow |
| [docs/social-feeds.md](docs/social-feeds.md) | Adapters, tokens, moderation |
| [docs/split-screen.md](docs/split-screen.md) | Scenes, zones, the 16 layouts |
| [docs/branding-assets.md](docs/branding-assets.md) | Asset inventory, fonts, tokens, licensing |
| [docs/api.md](docs/api.md) | REST + SSE reference |
| [docs/troubleshooting.md](docs/troubleshooting.md) | Common problems and fixes |
| [CLAUDE.md](CLAUDE.md) | Repository map and contributor guide |
| [PROJECT_STATE.md](PROJECT_STATE.md) | Current status |

## Project layout

```
cmd/beermate-display-manager/   entrypoint, workers
internal/                       Go packages (see CLAUDE.md for the map)
web/                            Preact + TypeScript frontend
deploy/                         systemd unit, autostart, config example
scripts/                        install / update / rollback / backup / login
assets/brand/                   logos, favicons, fonts (from the brandbook)
docs/                           documentation
```

## Licensing

Application code: see repository. Bundled fonts (Plus Jakarta Sans, Epilogue) are
under the SIL Open Font License 1.1 — the licence files ship alongside the fonts
in `assets/brand/fonts/`. See [docs/branding-assets.md](docs/branding-assets.md).

BeerMate brand assets are the property of BeerMate and are included for use by
this application only.
