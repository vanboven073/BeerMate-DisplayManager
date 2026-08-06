#!/usr/bin/env bash
#
# uninstall-jetson.sh — remove the service and application.
#
# By default it preserves ALL data (database, uploads, backups, config, key) so
# an uninstall is never destructive. Pass --purge to also remove the data and
# config directories; that requires typing the confirmation phrase.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_require_root

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

bm_info "stopping and disabling the service"
systemctl stop "${BM_SERVICE}" 2>/dev/null || true
systemctl disable "${BM_SERVICE}" 2>/dev/null || true
rm -f "${BM_SYSTEMD_UNIT}"

bm_info "stopping and disabling the Xvfb virtual display"
systemctl stop "${BM_XVFB_SERVICE}" 2>/dev/null || true
systemctl disable "${BM_XVFB_SERVICE}" 2>/dev/null || true
rm -f "${BM_XVFB_UNIT}"

systemctl daemon-reload

bm_info "removing the player autostart entry"
rm -f "/home/${BM_USER}/.config/autostart/beermate-player.desktop"

bm_info "removing the application directory"
rm -rf "${BM_APP_DIR}"

if [ "${PURGE}" -eq 1 ]; then
  bm_warn "PURGE requested: this will delete ALL data, backups, config and the encryption key."
  echo "Type exactly 'DELETE BEERMATE DATA' to proceed:"
  read -r confirm
  if [ "${confirm}" = "DELETE BEERMATE DATA" ]; then
    rm -rf "${BM_DATA_DIR}" "${BM_CONFIG_DIR}"
    bm_ok "all data and configuration removed"
  else
    bm_warn "confirmation did not match; data and configuration kept"
  fi
else
  bm_ok "service removed. Data kept at ${BM_DATA_DIR} and config at ${BM_CONFIG_DIR}"
  echo "  To remove everything: ${SCRIPT_DIR}/uninstall-jetson.sh --purge"
fi
