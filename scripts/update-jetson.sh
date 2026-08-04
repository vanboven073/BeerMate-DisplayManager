#!/usr/bin/env bash
#
# update-jetson.sh — upgrade the binary in place, keeping the previous one.
#
# Takes a database snapshot and keeps the outgoing binary as .prev so
# rollback-jetson.sh can restore both. Run from the release directory.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

RELEASE_DIR="${1:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
BINARY_SRC="${RELEASE_DIR}/${BM_BINARY}"

bm_require_root
bm_need curl
[ -f "${BINARY_SRC}" ] || bm_die "binary not found at ${BINARY_SRC}"

ts="$(date -u +%Y%m%d-%H%M%S)"

# 1. Snapshot the database so a bad migration can be undone.
if [ -f "${BM_DATA_DIR}/database/beermate.db" ]; then
  snap="${BM_DATA_DIR}/backups/pre-update-${ts}.db"
  bm_info "snapshotting the database to ${snap}"
  cp -a "${BM_DATA_DIR}/database/beermate.db" "${snap}"
  chown "${BM_USER}:${BM_GROUP}" "${snap}"
fi

# 2. Keep the outgoing binary for rollback.
if [ -f "${BM_APP_DIR}/${BM_BINARY}" ]; then
  bm_info "preserving the current binary as ${BM_BINARY}.prev"
  cp -a "${BM_APP_DIR}/${BM_BINARY}" "${BM_APP_DIR}/${BM_BINARY}.prev"
fi

bm_stop_service

# 3. Install the new binary atomically.
bm_info "installing the new binary"
install -o root -g root -m 0755 "${BINARY_SRC}" "${BM_APP_DIR}/${BM_BINARY}.new"
mv -f "${BM_APP_DIR}/${BM_BINARY}.new" "${BM_APP_DIR}/${BM_BINARY}"

# 4. Refresh scripts and unit if provided.
if [ -d "${SCRIPT_DIR}" ]; then
  install -o root -g root -m 0755 "${SCRIPT_DIR}"/*.sh "${BM_APP_DIR}/scripts/" 2>/dev/null || true
fi
if [ -f "${RELEASE_DIR}/deploy/systemd/${BM_SERVICE}.service" ]; then
  install -o root -g root -m 0644 \
    "${RELEASE_DIR}/deploy/systemd/${BM_SERVICE}.service" "${BM_SYSTEMD_UNIT}"
  systemctl daemon-reload
fi

# Refresh the virtual display while the service is still stopped: the service
# joins this unit's /tmp namespace at start, so restarting Xvfb underneath a
# running service would leave it holding a stale namespace.
bm_install_xvfb_unit "${RELEASE_DIR}"

bm_info "starting the service"
systemctl start "${BM_SERVICE}"

if bm_wait_health 30; then
  bm_ok "update complete and healthy"
else
  bm_warn "service did not become healthy; consider: ${BM_APP_DIR}/scripts/rollback-jetson.sh"
  exit 1
fi
