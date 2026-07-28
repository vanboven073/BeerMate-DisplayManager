# Jetson deployment

Short, copy-pastable commands for the QBee root shell. Avoid pasting large text
blocks; everything complex lives in `scripts/`.

> Deploying for the first time? Use the full runbook in
> [deployment-guide.md](deployment-guide.md) — it covers building the release on
> the workstation, assembling the bundle the installer expects, the Xvfb virtual
> display, first-run bootstrap and an acceptance checklist. This page is the
> cheat sheet for when you already know the flow.

## Target

Original NVIDIA Jetson Nano · Ubuntu 18.04 · ARM64 · Chromium ~97 · ethernet to a
Teltonika RUT · graphical auto-login as `beermate` · Tailscale for remote access.
The Jetson stays powered on continuously; only the display sleeps on schedule.

## 1. Install Tailscale

### On the Jetson (Ubuntu 18.04)

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

Follow the printed URL to authenticate the device into your tailnet. Then note
its address:

```bash
tailscale ip -4
```

### On the Windows laptop

Install from <https://tailscale.com/download/windows>, sign into the **same**
tailnet, and confirm you can reach the Jetson:

```powershell
tailscale ip -4
ping <jetson-tailscale-ip>
```

Do **not** configure any public port forwarding. The admin UI is reachable only
inside the tailnet.

## 2. Install the display manager

Copy the release directory to the Jetson (via `scp`, QBee file transfer, or a USB
stick), then:

```bash
cd /path/to/release
sudo ./scripts/install-jetson.sh
sudo ./scripts/verify-installation.sh
```

`install-jetson.sh` creates the `beermate` service user, the data/config
directories, the encryption key (0640), installs the binary, the systemd unit and
the player autostart entry, seeds a default config, and starts the service. It is
safe to re-run for upgrades — it snapshots the database first and never
regenerates the key.

Required device packages (install once):

```bash
sudo apt-get update
sudo apt-get install -y chromium-browser xvfb poppler-utils curl
```

- `chromium-browser` — the player and the managed browser.
- `xvfb` — virtual display for managed-website capture.
- `poppler-utils` — `pdftoppm` for PDF import.

## 3. First run

From the laptop, over Tailscale:

```
http://<jetson-tailscale-ip>:8080/admin
```

Create the administrator (first-run setup appears automatically). The player
opens on the Jetson's HDMI display on the next graphical login, or immediately:

```bash
sudo -u beermate DISPLAY=:0 /opt/BeerMateDisplayManager/scripts/run-player.sh &
```

## 4. Migrate the old kiosk

Non-destructive: it backs up the old kiosk and creates a **draft** playlist you
review and publish.

```bash
sudo -u beermate /opt/BeerMateDisplayManager/scripts/import-legacy-config.sh
```

If it asks for a session, sign in at the admin URL, copy the `beermate_session`
cookie value, and re-run with `BEERMATE_SESSION=<value> ...`. Then open the
dashboard, review the imported roadmap / dashboard / countdown scenes, and
publish. The old kiosk autostart is preserved for rollback.

## 5. Updates and rollback

```bash
# from a new release directory
sudo ./scripts/update-jetson.sh          # snapshots db, keeps old binary as .prev
sudo ./scripts/rollback-jetson.sh        # restores previous binary + db snapshot
sudo ./scripts/rollback-jetson.sh --keep-db   # binary only
```

## 6. Health and logs

```bash
curl -s http://127.0.0.1:8080/health | head
journalctl -u beermate-display-manager -f
systemctl status beermate-display-manager
```

## 7. Uninstall

```bash
sudo ./scripts/uninstall-jetson.sh          # keeps all data
sudo ./scripts/uninstall-jetson.sh --purge  # removes data (typed confirmation)
```

## Notes on DISPLAY and permissions

The service runs as `beermate` with `DISPLAY=:0` and
`XAUTHORITY=/home/beermate/.Xauthority` (set in the unit) so the DPMS controller
can drive the panel that the auto-logged-in session owns. If your display manager
stores the X cookie elsewhere, adjust `XAUTHORITY` in
`/etc/systemd/system/beermate-display-manager.service` and
`systemctl daemon-reload`.

The managed browser's DevTools port stays on `127.0.0.1` and is never exposed
over Tailscale; `verify-installation.sh` checks this.
