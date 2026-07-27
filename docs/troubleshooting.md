# Troubleshooting

Commands assume the Jetson. Start with the logs and health:

```bash
systemctl status beermate-display-manager
journalctl -u beermate-display-manager -n 100 --no-pager
curl -s http://127.0.0.1:8080/health
```

## The service will not start

- **Config error**: run `/opt/BeerMateDisplayManager/beermate-display-manager
  -check` to validate config and migrations without starting. It prints the
  first problem.
- **Non-loopback DevTools address**: the service refuses to start if
  `browser_debug_addr` is not a loopback host. Fix it in
  `/etc/beermate-display-manager/config.json`.
- **Bad timezone**: an unknown `timezone` fails startup. Use an IANA name like
  `Europe/Amsterdam`.
- **Permissions**: the `beermate` user must own `/var/lib/beermate-display-manager`.
  Re-run `install-jetson.sh`, which fixes ownership.

## The admin page is unreachable over Tailscale

```bash
tailscale status              # both devices in the tailnet?
tailscale ip -4               # the Jetson's address
ss -ltn | grep 8080           # is the service listening on 0.0.0.0:8080?
curl -s http://127.0.0.1:8080/health   # is it healthy locally?
```

If it is healthy locally but unreachable remotely, the problem is Tailscale (ACLs
or the device being logged out), not the service.

## The display is blank

- **Outside scheduled hours** — expected. `GET /health` shows `display.on`. The
  standby screen shows a dim BeerMate card with the time; a truly blank screen
  means something else.
- **Player not running**: it starts on graphical login. Start it manually:
  ```bash
  sudo -u beermate DISPLAY=:0 /opt/BeerMateDisplayManager/scripts/run-player.sh &
  ```
- **Nothing published**: `overview` shows `playlist.published_revision: null`.
  Publish a playlist from the dashboard.
- **DPMS**: check `xset q` as the `beermate` user with `DISPLAY=:0`. If the panel
  will not wake, the `XAUTHORITY` in the unit may be wrong — see
  jetson-deployment.md.

## The display will not sleep / wake on schedule

- The engine uses **server time** and the configured timezone; check the Jetson's
  clock (`timedatectl`). The original Nano has no RTC battery, so a cold-booted
  clock can be wrong until NTP settles.
- Confirm the weekly rule for the day is enabled and its times are right in the
  Schedule view. Remember an off-time **earlier** than the on-time means an
  overnight window (e.g. 18:00–02:00).
- A stuck manual override blocks the schedule until it expires or is cleared —
  clear it in the Schedule view.

## A website slide shows a fallback / login page

- **iframe site refuses framing**: switch it to a managed website (mark it as
  requiring authentication or set render mode to managed) so it is captured
  instead of framed.
- **Session expired**: the dashboard shows `Reauth needed`. Run Prepare login →
  log in on the Jetson → Finish login. See authenticated-websites.md.
- **Managed capture fails**: ensure `chromium-browser` and `xvfb` are installed
  and `browser_enabled: true`.

## A video will not play

- Only **H.264 MP4** decodes reliably on the Nano's Chromium 97. WebM/other MP4
  brands carry a warning on upload; re-encode to H.264 baseline/main if playback
  fails.

## PDF import produced no pages

- Install poppler: `sudo apt-get install -y poppler-utils`. The PDF itself is
  still stored; re-upload after installing, or the pages appear once the tool is
  present.

## Uploads are rejected

- **SVG**: rejected by design (it can carry scripts). Export as PNG.
- **Wrong type**: the file's actual signature is used, not its name. The error
  states what the contents are.
- **Too large**: caps are in the config (`max_*_bytes`).

## Low disk / storage warnings

`/health` reports `storage.state: low` under the `low_disk_warn_bytes` threshold.
Delete unused media and old backups from the dashboard; orphaned files are swept
automatically every few hours.

## Recovering from a bad update

```bash
sudo /opt/BeerMateDisplayManager/scripts/rollback-jetson.sh
```

Restores the previous binary and the newest pre-update database snapshot. Add
`--keep-db` to roll back the binary only.

## Restoring from a backup

Download and verify from the dashboard, or on the device:

```bash
sudo /opt/BeerMateDisplayManager/scripts/restore.sh /path/to/backup.tar.gz
```

It integrity-checks the archive, snapshots the current database first, and
restarts the service.

## Resetting a locked-out admin

Login throttling clears on its own after the lockout window. If the only admin
password is lost, restore a backup from before the change, or (last resort) stop
the service and inspect `/var/lib/beermate-display-manager/database` with
`sqlite3` — there is no plaintext password to recover, so a password reset means
setting a new Argon2id hash, most easily via a restored backup.
