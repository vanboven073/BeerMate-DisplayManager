#!/usr/bin/env bash
#
# run-player.sh — launch and supervise the fullscreen Chromium player.
#
# Started by the graphical autostart entry as the beermate user. It waits for the
# backend and the X session, then keeps Chromium in kiosk mode pointed at the
# local player, relaunching it if it exits so a crash never leaves a blank wall.
set -Eeuo pipefail

PLAYER_URL="http://127.0.0.1:8080/player"
HEALTH_URL="http://127.0.0.1:8080/health"
PROFILE_DIR="${HOME}/.config/beermate-player-chrome"

log() { echo "[beermate-player] $*"; }

# Find a Chromium binary. Chromium 97 on Ubuntu 18.04 is usually chromium-browser.
find_chromium() {
  local c
  for c in chromium-browser chromium google-chrome google-chrome-stable; do
    if command -v "$c" >/dev/null 2>&1; then
      command -v "$c"
      return 0
    fi
  done
  return 1
}

CHROMIUM="$(find_chromium || true)"
if [ -z "${CHROMIUM}" ]; then
  log "no Chromium binary found; install chromium-browser"
  exit 1
fi
log "using ${CHROMIUM}"

# Wait for the backend to answer /health before opening the player, so the first
# frame is content and not a connection error.
log "waiting for the backend at ${HEALTH_URL} ..."
for _ in $(seq 1 60); do
  if curl -fsS --max-time 2 "${HEALTH_URL}" >/dev/null 2>&1; then
    log "backend is ready"
    break
  fi
  sleep 2
done

# Blank the screensaver and power management so the panel never sleeps on its own;
# the display schedule is what controls standby. Best effort.
if command -v xset >/dev/null 2>&1; then
  xset s off || true
  xset -dpms || true
  xset s noblank || true
fi
# Hide the cursor if unclutter is present.
if command -v unclutter >/dev/null 2>&1; then
  unclutter -idle 1 &
fi

mkdir -p "${PROFILE_DIR}"

# Flags limited to those Chromium 97 supports. --no-sandbox is NOT used: the
# player runs as the unprivileged beermate user, so the sandbox stays on.
CHROME_FLAGS=(
  --kiosk
  --app="${PLAYER_URL}"
  --user-data-dir="${PROFILE_DIR}"
  --password-store=basic
  --no-first-run
  --no-default-browser-check
  --disable-translate
  --disable-infobars
  --disable-session-crashed-bubble
  --disable-features=TranslateUI
  --noerrdialogs
  --check-for-update-interval=31536000
  --autoplay-policy=no-user-gesture-required
  --overscroll-history-navigation=0
  --disable-pinch
)

# Supervise: relaunch on exit with a small delay, so a Chromium crash recovers
# without a reboot. The loop exits only when this script is terminated.
while true; do
  log "starting Chromium kiosk"
  "${CHROMIUM}" "${CHROME_FLAGS[@]}" >/dev/null 2>&1 || true
  log "Chromium exited; relaunching in 3s"
  sleep 3
done
