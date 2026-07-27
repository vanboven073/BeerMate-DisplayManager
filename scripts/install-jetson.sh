#!/usr/bin/env bash
#
# install-jetson.sh — install or upgrade the BeerMate Display Manager.
#
# Idempotent and safe to re-run: it preserves the database, uploads, backups and
# the encryption key, and only replaces the binary, scripts and unit file. Run
# from the QBee root shell in the directory containing the release artifacts.
#
# Expected layout of the release directory:
#   ./beermate-display-manager                     (linux/arm64 binary)
#   ./deploy/systemd/beermate-display-manager.service
#   ./deploy/autostart/beermate-player.desktop
#   ./scripts/                                     (this directory)
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

RELEASE_DIR="${1:-$(cd "${SCRIPT_DIR}/.." && pwd)}"

bm_require_root
bm_need install
bm_need systemctl
bm_need curl

bm_info "installing from ${RELEASE_DIR}"

BINARY_SRC="${RELEASE_DIR}/${BM_BINARY}"
[ -f "${BINARY_SRC}" ] || bm_die "binary not found at ${BINARY_SRC}; build it with 'make build-arm64' first"

# Refuse a wrong-architecture binary early with a clear message, rather than
# failing cryptically at first start.
if command -v file >/dev/null 2>&1; then
  if ! file "${BINARY_SRC}" | grep -qiE 'aarch64|arm64'; then
    bm_warn "the binary does not look like ARM64: $(file -b "${BINARY_SRC}")"
    bm_warn "continuing, but it will not run on the Jetson if this is wrong"
  fi
fi

bm_ensure_user
bm_ensure_dirs
bm_generate_secret

# Back up the existing database before swapping the binary, so an upgrade that
# introduces a bad migration can be rolled back.
if [ -f "${BM_DATA_DIR}/database/beermate.db" ]; then
  bm_info "snapshotting the database before upgrade"
  ts="$(date -u +%Y%m%d-%H%M%S)"
  snap="${BM_DATA_DIR}/backups/pre-install-${ts}.db"
  cp -a "${BM_DATA_DIR}/database/beermate.db" "${snap}"
  chown "${BM_USER}:${BM_GROUP}" "${snap}"
  bm_ok "database snapshot at ${snap}"
fi

bm_stop_service

# Install the binary atomically: write to a temp name, then rename over the old
# one so a partially-copied binary is never left in place.
bm_info "installing binary"
install -o root -g root -m 0755 "${BINARY_SRC}" "${BM_APP_DIR}/${BM_BINARY}.new"
mv -f "${BM_APP_DIR}/${BM_BINARY}.new" "${BM_APP_DIR}/${BM_BINARY}"

bm_info "installing scripts"
install -o root -g root -m 0755 "${SCRIPT_DIR}"/*.sh "${BM_APP_DIR}/scripts/"

bm_info "installing systemd unit"
install -o root -g root -m 0644 \
  "${RELEASE_DIR}/deploy/systemd/${BM_SERVICE}.service" "${BM_SYSTEMD_UNIT}"

# Player autostart for the graphical beermate session.
AUTOSTART_DIR="/home/${BM_USER}/.config/autostart"
install -d -o "${BM_USER}" -g "${BM_GROUP}" -m 0755 "${AUTOSTART_DIR}"
install -o "${BM_USER}" -g "${BM_GROUP}" -m 0644 \
  "${RELEASE_DIR}/deploy/autostart/beermate-player.desktop" \
  "${AUTOSTART_DIR}/beermate-player.desktop"

# Seed a default config only if none exists; never clobber operator edits.
if [ ! -f "${BM_CONFIG_FILE}" ]; then
  bm_info "writing default configuration"
  cat > "${BM_CONFIG_FILE}" <<'JSON'
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
JSON
  chown root:"${BM_GROUP}" "${BM_CONFIG_FILE}"
  chmod 0640 "${BM_CONFIG_FILE}"
fi

bm_info "reloading systemd and enabling the service"
systemctl daemon-reload
systemctl enable "${BM_SERVICE}"
systemctl restart "${BM_SERVICE}"

bm_info "waiting for the service to become healthy"
if bm_wait_health 30; then
  bm_ok "service is healthy"
else
  bm_warn "service did not report healthy within 30s; check: journalctl -u ${BM_SERVICE} -n 50"
fi

echo
bm_ok "installation complete"
echo "  Player:  http://127.0.0.1:8080/player  (opens automatically on the Jetson)"
echo "  Admin:   http://<tailscale-ip>:8080/admin"
echo "  Health:  http://127.0.0.1:8080/health"
echo "  Logs:    journalctl -u ${BM_SERVICE} -f"
echo
echo "  First run: open the admin URL over Tailscale to create the administrator."
