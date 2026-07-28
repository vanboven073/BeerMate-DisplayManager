# Deployment guide — step by step

The complete procedure for taking this repository to a running display on the
Jetson Nano, in the order you should do it. Every command here is grounded in
what the scripts in `scripts/` and the unit files in `deploy/` actually do.

For a short copy-paste cheat sheet once you already know the flow, see
[jetson-deployment.md](jetson-deployment.md). For symptoms and fixes, see
[troubleshooting.md](troubleshooting.md).

**Time required:** ~45 minutes for a first deployment, ~5 minutes for updates.

---

## Contents

| Stage | What you do |
|---|---|
| [0](#0-before-you-start) | Prerequisites and decisions |
| [1](#1-build-the-release-on-the-workstation) | Build the ARM64 binary on Windows |
| [2](#2-assemble-the-release-bundle) | Lay out the bundle the installer expects |
| [3](#3-prepare-the-jetson) | Packages, Tailscale, clock, Xvfb |
| [4](#4-transfer-the-bundle-to-the-jetson) | Copy the bundle to the device |
| [5](#5-install) | Run the installer, review the config |
| [6](#6-verify-the-installation) | Automated checks |
| [7](#7-create-the-administrator-do-this-immediately) | First-run bootstrap |
| [8](#8-start-the-player-on-the-display) | Get content on the panel |
| [9](#9-configure-content-schedule-and-websites) | Playlist, schedule, authenticated sites |
| [10](#10-migrate-the-legacy-kiosk) | Import the old Bash kiosk |
| [11](#11-acceptance-checklist) | Sign-off checklist |
| [Day 2](#day-2-operations) | Updates, rollback, backup, logs, uninstall |
| [Reference](#reference) | Paths, ports, config keys, script index |

---

## 0. Before you start

### What you need

| | |
|---|---|
| **Jetson** | Original NVIDIA Jetson Nano, Ubuntu 18.04, ARM64, graphical auto-login as `beermate`, HDMI panel attached, ethernet to the Teltonika RUT |
| **Device access** | A root shell — QBee remote shell, SSH, or keyboard and monitor |
| **Workstation** | Windows laptop with Go 1.23+ and Node 20+ (toolchain pinned at `D:\BeerMate\.toolchain\go` on the current dev machine) |
| **Accounts** | A Tailscale account both devices can join |

### Decisions to make up front

- **Timezone** — `Europe/Amsterdam` unless the display lives elsewhere. It drives
  the schedule and countdowns.
- **Do you need managed or authenticated websites?** If yes, you must run an
  Xvfb virtual display ([step 3.5](#35-set-up-xvfb-99-only-if-you-need-managed-websites)).
  If every website you show can be embedded in an `iframe`, you can skip it and
  optionally set `browser_enabled: false`.
- **Who creates the first administrator?** The bootstrap endpoint is open until
  an administrator exists — whoever reaches it first on the tailnet claims the
  account. Plan to do [step 7](#7-create-the-administrator-do-this-immediately)
  within minutes of starting the service.

### What you end up with

One `systemd` service (`beermate-display-manager`) serving the admin dashboard,
the player, the REST API and `/health` on port 8080, plus a Chromium kiosk on the
Jetson's panel pointed at `http://127.0.0.1:8080/player`.

---

## 1. Build the release on the workstation

The Jetson never compiles anything: it receives one static `linux/arm64` binary
with the frontend embedded.

### 1.1 Point at the pinned toolchain (dev workstation only)

```powershell
$env:PATH = "D:\BeerMate\.toolchain\go\bin;$env:PATH"
go version    # expect go1.23 or newer
node -v       # expect v20 or newer
```

### 1.2 Build the frontend

Vite writes into `internal/web/dist`, which `internal/web/embed.go` embeds. This
must happen **before** the Go build. Skip it and the service still starts, but
`/admin` and `/player` serve a "frontend assets are not built" notice instead of
the dashboard — a failure you only discover on the device.

```powershell
cd web
npm ci --no-audit --no-fund
npm run typecheck
npm run build
cd ..
```

Confirm `internal/web/dist/index.html` now exists and is freshly dated.

### 1.3 Run the tests (recommended)

```powershell
go vet ./...
go test ./...
cd web; npm test; npm run lint; cd ..
```

### 1.4 Cross-compile the ARM64 binary

With `make` available:

```powershell
make release      # frontend + build-arm64
```

Without `make`, in PowerShell:

```powershell
$env:CGO_ENABLED = "0"; $env:GOOS = "linux"; $env:GOARCH = "arm64"
$VERSION = (git describe --tags --always --dirty)
$COMMIT  = (git rev-parse --short HEAD)
$DATE    = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$PKG     = "github.com/vanboven073/BeerMate-DisplayManager"
go build -trimpath -ldflags "-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT -X $PKG/internal/version.BuildDate=$DATE" -o dist\beermate-display-manager .\cmd\beermate-display-manager
Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH
```

### 1.5 Verify the artifact is genuinely ARM64

A wrong-architecture binary installs fine and then fails to exec on the device.
Check the ELF machine field is `0xB7` (183 = aarch64):

```powershell
$b = Get-Content dist\beermate-display-manager -Encoding Byte -TotalCount 20
[BitConverter]::ToUInt16($b[18..19], 0)     # expect 183
```

Expect a binary of roughly 12 MB.

---

## 2. Assemble the release bundle

`install-jetson.sh` looks for the binary **at the root of the release directory**,
alongside `deploy/` and `scripts/`. `make` writes it to `dist/`, so this copy step
is required:

```
release/
├── beermate-display-manager                          <- the ARM64 binary
├── deploy/
│   ├── systemd/beermate-display-manager.service
│   └── autostart/beermate-player.desktop
└── scripts/
    └── *.sh
```

On Windows:

```powershell
$stage = "dist\release"
Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $stage | Out-Null
Copy-Item dist\beermate-display-manager $stage\
Copy-Item -Recurse deploy  $stage\deploy
Copy-Item -Recurse scripts $stage\scripts
Compress-Archive -Path "$stage\*" -DestinationPath dist\beermate-release.zip -Force
```

> The installer refuses to continue if the binary is missing, and warns (but
> continues) if `file` reports a non-ARM64 binary.

---

## 3. Prepare the Jetson

All commands in this stage run **on the Jetson** as root.

### 3.1 Install the device packages

```bash
sudo apt-get update
sudo apt-get install -y chromium-browser xvfb poppler-utils curl
```

| Package | Needed for |
|---|---|
| `chromium-browser` | the kiosk player **and** the managed browser |
| `xvfb` | virtual display for managed-website capture |
| `poppler-utils` | `pdftoppm`, used by PDF import |
| `curl` | required by the install and health-check scripts |

Optional: `ffmpeg` (video probing), `unclutter` (hides the mouse cursor —
`run-player.sh` uses it if present), `sqlite3` (lets `backup.sh` checkpoint the
WAL and lets `restore.sh` integrity-check an archive before swapping it in).

### 3.2 Join the tailnet

On the Jetson:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up          # follow the printed URL to authenticate
tailscale ip -4            # note this address
```

On the Windows laptop, install from <https://tailscale.com/download/windows>,
sign into the **same** tailnet, then:

```powershell
ping <jetson-tailscale-ip>
```

**Do not configure any public port forwarding on the RUT.** The admin UI binds
`0.0.0.0:8080` so that it is reachable over the Tailscale interface; its only
protection from the public internet is the absence of a route to it.

### 3.3 Check the clock

The original Nano has no RTC battery, so a cold-booted clock can be badly wrong
until NTP settles — and the schedule engine trusts server time.

```bash
timedatectl        # expect "System clock synchronized: yes"
```

### 3.4 Confirm the service account

The installer, the unit file and the autostart entry all assume the user
`beermate`, auto-logged into the graphical session.

```bash
id beermate
ls /home/beermate
```

- If `beermate` already exists (the normal case on the Jetson), the installer
  leaves it exactly as it is.
- If it does not exist, the installer creates it as a **system user with
  `/usr/sbin/nologin`** — fine for the service, but such an account cannot own a
  graphical session, so the player autostart will not fire. On a fresh device,
  create a normal desktop user named `beermate` and enable auto-login first.

### 3.5 Set up Xvfb `:99` (only if you need managed websites)

The service launches its capture Chromium with `DISPLAY=:99` but **does not start
Xvfb itself**, and the installer does not install a unit for it. Without a
virtual display on `:99`, managed and authenticated websites fail to capture
(everything else keeps working).

```bash
sudo tee /etc/systemd/system/beermate-xvfb.service >/dev/null <<'EOF'
[Unit]
Description=Xvfb virtual display :99 for BeerMate managed websites
After=network.target

[Service]
Type=simple
User=beermate
Group=beermate
ExecStart=/usr/bin/Xvfb :99 -screen 0 1920x1080x24 -nolisten tcp
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now beermate-xvfb
systemctl is-active beermate-xvfb
```

Keep the screen geometry aligned with `browser_width`/`browser_height` in the
config (default 1920×1080). If you skip this step, set `"browser_enabled": false`
in [step 5.2](#52-review-the-configuration) so the service does not try.

---

## 4. Transfer the bundle to the Jetson

Over Tailscale from the laptop (adjust the user if you SSH as someone else):

```powershell
scp -r dist\release\* beermate@<jetson-tailscale-ip>:/tmp/beermate-release/
```

Alternatives: the QBee file transfer, or a USB stick. Then on the Jetson, restore
the executable bits that a Windows copy or a zip round-trip strips:

```bash
cd /tmp/beermate-release
chmod +x beermate-display-manager scripts/*.sh
ls -l                    # binary and scripts should be executable
```

---

## 5. Install

### 5.1 Run the installer

```bash
cd /tmp/beermate-release
sudo ./scripts/install-jetson.sh
```

It performs, in order:

1. Checks for root, `install`, `systemctl`, `curl`, and that the binary exists.
2. Warns if the binary is not ARM64.
3. Creates the `beermate` user and group if missing.
4. Creates the data tree (`database`, `uploads`, `thumbnails`,
   `browser-profiles`, `backups`, `cache`, `social-cache`, `runtime`) at 0750,
   owned by `beermate`.
5. **Generates the encryption key** at `/etc/beermate-display-manager/secret.key`
   (root:beermate, 0640) — only if one does not already exist.
6. Snapshots an existing database to `backups/pre-install-<timestamp>.db`.
7. Stops the service, installs the binary atomically, installs the scripts, the
   systemd unit and the player autostart entry.
8. Seeds `/etc/beermate-display-manager/config.json` **only if absent** — your
   edits are never clobbered.
9. `daemon-reload`, `enable`, `restart`, then waits up to 30s for `/health`.

It is safe to re-run. Expect it to end with `installation complete` and the admin
/ player / health URLs.

> **Never delete or regenerate `secret.key` on an existing install.** It encrypts
> stored API tokens and website credentials with AES-256-GCM; losing it makes
> every stored credential permanently undecryptable. Copy it somewhere safe and
> offline now — it is deliberately excluded from every backup archive.

### 5.2 Review the configuration

The seeded file contains only nine keys; everything else comes from the built-in
defaults in `internal/config/config.go`. The full annotated set of keys is in
[`deploy/config.example.json`](../deploy/config.example.json).

```bash
sudo cat /etc/beermate-display-manager/config.json
```

```json
{
  "listen_addr": "0.0.0.0:8080",
  "timezone": "Europe/Amsterdam",
  "log_level": "info",
  "log_format": "json",
  "browser_enabled": true,
  "browser_debug_addr": "127.0.0.1:9222",
  "dpms_enabled": true,
  "dpms_driver": "xset",
  "dpms_display": ":0"
}
```

Adjust if needed, then:

```bash
sudo systemctl restart beermate-display-manager
```

Rules worth knowing:

- `browser_debug_addr` **must** be a loopback address. The service refuses to
  start otherwise — the DevTools port grants full browser control including every
  stored session cookie.
- `timezone` must be a valid IANA name; the binary embeds its own tzdata, so it
  does not depend on Ubuntu 18.04's stale zoneinfo.
- `dpms_driver` is `xset` on the Jetson (`mock`/`noop` are for development).
- Environment variables override the file — see the
  [reference table](#configuration-keys-and-environment-overrides).

### 5.3 Validate without starting (optional)

```bash
sudo -u beermate /opt/BeerMateDisplayManager/beermate-display-manager -check
```

Prints the first configuration problem, or `configuration OK; schema version N`.

> Run it as `beermate`, **not** as root: `-check` opens the database and applies
> pending migrations before exiting, and doing that as root would leave
> root-owned files in the data directory that the service then cannot write.

---

## 6. Verify the installation

```bash
sudo ./scripts/verify-installation.sh
```

| Check | Meaning if it fails |
|---|---|
| binary present / runs `-version` | wrong architecture or a truncated copy |
| service enabled and active | check `journalctl -u beermate-display-manager -n 50` |
| data directories exist and are owned by `beermate` | re-run the installer; it fixes ownership |
| encryption key present, mode 600 or 640 | key missing or too permissive |
| health endpoint responds | the service is not up — check the logs |
| listening on 8080 | bind address wrong or port taken |
| DevTools port loopback-only | **security failure** — fix `browser_debug_addr` |
| player autostart entry present | warning only; the panel would stay dark |

It exits non-zero if any critical check fails. Then confirm by hand:

```bash
curl -s http://127.0.0.1:8080/health
systemctl status beermate-display-manager
journalctl -u beermate-display-manager -n 50 --no-pager
```

`/health` is unauthenticated by design and contains no secrets.

---

## 7. Create the administrator (do this immediately)

Until the first administrator exists, `POST /api/v1/bootstrap` is unauthenticated
— **anyone who can reach port 8080 on the tailnet becomes the administrator by
being first.** Close that window now.

From the laptop, over Tailscale:

```
http://<jetson-tailscale-ip>:8080/admin
```

The first-run setup screen appears automatically. Create the administrator with a
strong, unique password. Then confirm the window is closed:

```bash
curl -s http://127.0.0.1:8080/api/v1/bootstrap/status
# {"needs_bootstrap":false}
```

Roles are `viewer` (read-only), `editor` (content and publishing) and `admin`
(users, rollback, backups and backup downloads). The dashboard's Settings view
covers password change and session revocation only — **there is no user-management
screen yet**, so further accounts are created through the API by an admin:

```bash
S=<session-cookie>                       # beermate_session value
C=$(curl -fsS -H "Cookie: beermate_session=$S" \
      http://127.0.0.1:8080/api/v1/auth/me | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')

curl -fsS -X POST http://127.0.0.1:8080/api/v1/users \
  -H "Cookie: beermate_session=$S" -H "X-BeerMate-CSRF: $C" \
  -H "Content-Type: application/json" \
  -d '{"username":"kim","display_name":"Kim","password":"<strong-password>","role":"editor"}'
```

`GET /api/v1/users` lists them; `PATCH /api/v1/users/{id}` changes role, disables
an account or sets a new password. The last remaining admin cannot be removed or
demoted.

---

## 8. Start the player on the display

The autostart entry runs on the next graphical login of `beermate`. To start it
now without rebooting:

```bash
sudo -u beermate DISPLAY=:0 /opt/BeerMateDisplayManager/scripts/run-player.sh &
```

`run-player.sh` waits for `/health`, disables the X screensaver and X's own DPMS
(the schedule owns display power), then runs Chromium in kiosk mode against
`http://127.0.0.1:8080/player` and relaunches it if it exits.

What you should see:

- **Branded standby card with the time** — the display is outside its scheduled
  hours, or nothing is published yet. Normal.
- **Fully blank panel** — not normal; see
  [troubleshooting](troubleshooting.md#the-display-is-blank).

Reboot once and confirm the player comes back on its own:

```bash
sudo reboot
```

---

## 9. Configure content, schedule and websites

From the dashboard, over Tailscale:

1. **Media** — upload images, videos (H.264 MP4 decodes reliably on Chromium 97)
   and PDFs. The file's magic bytes decide its type, not its name; SVG is
   rejected by design.
2. **Playlist** — build scenes. A fullscreen slide is a one-zone scene; pick one
   of the 16 layouts for split-screen. Editing changes a **draft**;
   **Publish** stamps it atomically and the player picks it up over SSE within
   seconds.
3. **Schedule** — set weekly on/off windows. An off-time earlier than the on-time
   means an overnight window (e.g. 18:00–02:00). DST is handled correctly.
   Overrides expire on their own.
4. **Websites** — `iframe` for embeddable sites, `managed` for the rest.
   Authenticated sites are always managed and need Xvfb from
   [step 3.5](#35-set-up-xvfb-99-only-if-you-need-managed-websites).
5. **Social feeds** — official feeds only (`rss`, `atom`, `json`, `youtube`,
   `webhook`, `manual`); moderation is manual by default.

### Logging into an authenticated website

Passwords are never stored — a human logs in once on the real panel and the
cookies live in a persistent Chromium profile.

```bash
# From the dashboard: Websites -> Prepare login. Or from a shell:
BEERMATE_SESSION=<session-cookie> \
  /opt/BeerMateDisplayManager/scripts/start-interactive-login.sh <website-id>

# ... log in at the Jetson's keyboard, then:
BEERMATE_SESSION=<session-cookie> \
  /opt/BeerMateDisplayManager/scripts/stop-interactive-login.sh <website-id>
```

`finish-login` flushes cookies to the profile and validates that the target URL
opens without redirecting to a login page. Details in
[authenticated-websites.md](authenticated-websites.md).

To get the session cookie value: sign in to the dashboard, open the browser dev
console and run `document.cookie`, then copy the `beermate_session` value.

---

## 10. Migrate the legacy kiosk

Non-destructive: it backs up the old kiosk and creates a **draft** you review and
publish. Nothing is deleted, so rollback to the old kiosk stays possible.

```bash
BEERMATE_SESSION=<session-cookie> \
  sudo -u beermate /opt/BeerMateDisplayManager/scripts/import-legacy-config.sh
```

Run without `BEERMATE_SESSION` first if you like: it still takes the backup and
then prints exactly how to supply the session.

It copies `~/beermate-kiosk`, `~/Pictures/Roadmap_2026.png`, `~/bin/beermate-kiosk.sh`
and the old autostart entry to `backups/legacy-kiosk-<timestamp>/`, then recreates
the roadmap image, the dashboard website and the countdown as draft scenes.

Afterwards: open the dashboard, review the imported scenes, publish, and confirm
the panel shows the new content. Only then disable the old kiosk autostart:

```bash
mv /home/beermate/.config/autostart/beermate-kiosk.desktop \
   /home/beermate/.config/autostart/beermate-kiosk.desktop.disabled
```

To go back to the old kiosk, restore that file from the backup directory and
disable `beermate-player.desktop` the same way.

---

## 11. Acceptance checklist

- [ ] `verify-installation.sh` exits 0
- [ ] `/health` responds locally and the dashboard loads over Tailscale
- [ ] Administrator created; `needs_bootstrap` is `false`
- [ ] Dashboard is **not** reachable from outside the tailnet
- [ ] DevTools port 9222 is loopback-only
- [ ] A playlist is published and rendering on the panel
- [ ] Schedule sleeps and wakes the panel at the configured times
- [ ] Player recovers after `sudo reboot`
- [ ] Player recovers after Chromium is killed (`pkill chromium`; it relaunches in ~3s)
- [ ] Service recovers after `sudo systemctl kill -s SIGKILL beermate-display-manager`
- [ ] A backup has been created and downloaded off the device
- [ ] `secret.key` **and** the `uploads/` tree are copied to secure offline storage
      (a backup archive contains neither)
- [ ] Clock is NTP-synchronised (`timedatectl`)
- [ ] Managed websites capture correctly (or `browser_enabled` is `false`)

---

## Day 2 operations

### Update to a new version

Build and assemble exactly as in stages [1](#1-build-the-release-on-the-workstation)
and [2](#2-assemble-the-release-bundle), copy to the device, then:

```bash
cd /tmp/beermate-release
sudo ./scripts/update-jetson.sh
```

It snapshots the database to `backups/pre-update-<timestamp>.db`, keeps the
outgoing binary as `beermate-display-manager.prev`, installs the new one
atomically, refreshes the scripts and unit, and waits for health — exiting
non-zero if the service does not come back.

> Use `update-jetson.sh` for upgrades, not `install-jetson.sh`. The installer
> snapshots the database as `pre-install-*` and does not keep a `.prev` binary,
> and `rollback-jetson.sh` only looks for `.prev` and `pre-update-*` snapshots.

### Roll back a bad update

```bash
sudo /opt/BeerMateDisplayManager/scripts/rollback-jetson.sh            # binary + database
sudo /opt/BeerMateDisplayManager/scripts/rollback-jetson.sh --keep-db  # binary only
```

The failed binary is kept as `.failed` and the replaced database as
`beermate.db.rolledback-<timestamp>` for inspection.

### Backups

From the dashboard (**Backups → Create**), or on the device:

```bash
sudo /opt/BeerMateDisplayManager/scripts/backup.sh
```

Archives are checksummed gzip tars containing exactly two members: `manifest.json`
and `database/beermate.db`. They deliberately **exclude** the encryption key,
browser profiles, session cookies and the raw media files. Service-created backups
retain the newest 10 by default (`backup_retention`); `backup.sh` keeps the newest
20 of its own.

A backup archive alone therefore cannot rebuild a destroyed device. A complete
disaster-recovery set is three things:

```bash
# 1. the backup archive       /var/lib/beermate-display-manager/backups/*.tar.gz
# 2. the encryption key       /etc/beermate-display-manager/secret.key
# 3. the uploaded media       /var/lib/beermate-display-manager/uploads/
sudo tar -czf /tmp/beermate-uploads.tar.gz \
  -C /var/lib/beermate-display-manager uploads thumbnails
```

Copy all three off the device regularly. Only an `admin` may download backups
through the API.

### Restore

```bash
sudo /opt/BeerMateDisplayManager/scripts/restore.sh /path/to/backup.tar.gz
```

Verifies the archive contents, requires you to type `RESTORE`, snapshots the
current database to `pre-restore-<timestamp>.db`, integrity-checks the extracted
database (if `sqlite3` is present), swaps it in and restarts the service.

### Logs and health

```bash
journalctl -u beermate-display-manager -f
journalctl -u beermate-display-manager --since "1 hour ago" --no-pager
curl -s http://127.0.0.1:8080/health
```

Logs are JSON by default with a hard redaction layer over cookies, Authorization
headers, tokens and form bodies. `/health` reports display state, storage state
and the published revision.

### Uninstall

```bash
sudo /opt/BeerMateDisplayManager/scripts/uninstall-jetson.sh          # keeps all data
sudo /opt/BeerMateDisplayManager/scripts/uninstall-jetson.sh --purge  # deletes data + key
```

`--purge` requires typing `DELETE BEERMATE DATA` exactly, and removes the
encryption key along with everything else. If you have an Xvfb unit from step
3.5, remove it separately.

---

## Reference

### Paths, ports and accounts

| Thing | Location |
|---|---|
| Application dir | `/opt/BeerMateDisplayManager` |
| Binary | `/opt/BeerMateDisplayManager/beermate-display-manager` |
| Scripts | `/opt/BeerMateDisplayManager/scripts/` |
| Data dir (0750, `beermate`) | `/var/lib/beermate-display-manager` |
| Database | `…/database/beermate.db` |
| Backups and snapshots | `…/backups/` |
| Config dir | `/etc/beermate-display-manager` |
| Config file (0640, root:beermate) | `…/config.json` |
| Encryption key (0640, root:beermate) | `…/secret.key` |
| systemd unit | `/etc/systemd/system/beermate-display-manager.service` |
| Player autostart | `/home/beermate/.config/autostart/beermate-player.desktop` |
| Service / user / group | `beermate-display-manager` / `beermate` / `beermate` |

| Port | Bind | Purpose |
|---|---|---|
| 8080 | `0.0.0.0` | admin, player, API, health (reachable only over Tailscale) |
| 9222 | `127.0.0.1` | CDP DevTools for the capture browser — never exposed |
| 9224 | `127.0.0.1` | CDP for the interactive-login browser instance |

| URL | |
|---|---|
| `http://<tailscale-ip>:8080/admin` | dashboard |
| `http://127.0.0.1:8080/player` | player (loopback) |
| `http://127.0.0.1:8080/health` | health |

### Configuration keys and environment overrides

Precedence: built-in defaults → `config.json` → environment. The unit file sets
`BEERMATE_CONFIG_DIR`, `BEERMATE_DATA_DIR`, `DISPLAY` and `XAUTHORITY`.

| Key | Default | Env override |
|---|---|---|
| `listen_addr` | `0.0.0.0:8080` | `BEERMATE_LISTEN_ADDR` |
| `timezone` | `Europe/Amsterdam` | `BEERMATE_TIMEZONE` |
| `log_level` / `log_format` | `info` / `json` | `BEERMATE_LOG_LEVEL` / `BEERMATE_LOG_FORMAT` |
| `trust_proxy_headers` | `false` | `BEERMATE_TRUST_PROXY_HEADERS` |
| `session_idle_timeout` | `12h` | `BEERMATE_SESSION_IDLE_TIMEOUT` |
| `session_max_lifetime` | `168h` | `BEERMATE_SESSION_MAX_LIFETIME` |
| `max_image_bytes` | 25 MiB | `BEERMATE_MAX_IMAGE_BYTES` |
| `max_video_bytes` | 512 MiB | `BEERMATE_MAX_VIDEO_BYTES` |
| `max_pdf_bytes` / `max_pdf_pages` | 64 MiB / 50 | `BEERMATE_MAX_PDF_BYTES` / `…_PAGES` |
| `browser_enabled` | `true` | `BEERMATE_BROWSER_ENABLED` |
| `browser_debug_addr` | `127.0.0.1:9222` | `BEERMATE_BROWSER_DEBUG_ADDR` |
| `browser_xvfb_display` | `:99` | `BEERMATE_BROWSER_XVFB_DISPLAY` |
| `browser_real_display` | `:0` | `BEERMATE_BROWSER_REAL_DISPLAY` |
| `browser_width` / `browser_height` | 1920 / 1080 | `BEERMATE_BROWSER_WIDTH` / `…_HEIGHT` |
| `dpms_enabled` / `dpms_driver` / `dpms_display` | `true` / `xset` / `:0` | `BEERMATE_DPMS_*` |
| `pdftoppm_path` / `ffmpeg_path` | `pdftoppm` / `ffmpeg` | `BEERMATE_PDFTOPPM_PATH` / `BEERMATE_FFMPEG_PATH` |
| `backup_retention` | 10 | `BEERMATE_BACKUP_RETENTION` |
| `revision_retention` | 25 (min 2) | `BEERMATE_REVISION_RETENTION` |
| `social_cache_max_posts` | 200 | `BEERMATE_SOCIAL_CACHE_MAX_POSTS` |
| `low_disk_warn_bytes` | 1 GiB | `BEERMATE_LOW_DISK_WARN_BYTES` |
| `max_concurrent_hash` | 2 | `BEERMATE_MAX_CONCURRENT_HASH` |
| `max_sse_clients` | 16 (min 2) | `BEERMATE_MAX_SSE_CLIENTS` |
| `max_managed_captures` | 2 | `BEERMATE_MAX_MANAGED_CAPTURES` |

### Script index

| Script | Run as | Purpose |
|---|---|---|
| `install-jetson.sh` | root | First install; idempotent |
| `update-jetson.sh` | root | Upgrade with snapshot + `.prev` binary |
| `rollback-jetson.sh` | root | Undo the last update |
| `verify-installation.sh` | root | Post-install checks; exits non-zero on failure |
| `uninstall-jetson.sh` | root | Remove service (`--purge` also removes data) |
| `run-player.sh` | `beermate` | Kiosk Chromium supervisor (called by autostart) |
| `backup.sh` | root | Create a backup archive from the CLI |
| `restore.sh` | root | Restore a backup archive |
| `import-legacy-config.sh` | `beermate` | Import the old kiosk as a draft |
| `start-interactive-login.sh` | any | Open a website for manual login |
| `stop-interactive-login.sh` | any | Finish and validate that login |
| `lib.sh` | — | Shared constants and helpers (sourced) |

### Known gaps this guide works around

1. **Binary location** — `make` writes to `dist/`, the installer expects the
   binary at the bundle root. Stage 2 handles it; a `make package` target would
   remove the manual step.
2. **Xvfb** — the service uses `:99` but nothing starts it, and the installer
   ships no unit. Step 3.5 provides one.
3. **Not yet run on hardware** — as of this writing the scripts are syntax-checked
   but have not been executed on a physical Jetson (see `PROJECT_STATE.md`).
   Expect to iterate on the first real run; capture the output.
4. **Rollback only follows `update-jetson.sh`** — see the note in
   [Day 2](#update-to-a-new-version).
