#!/usr/bin/env bash
#
# rollback-jetson.sh — restore the previous binary and the most recent
# pre-update database snapshot.
#
# Undoes the last update-jetson.sh. It restores .prev over the current binary
# and, unless --keep-db is given, the newest pre-update snapshot over the
# database.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_require_root

KEEP_DB=0
[ "${1:-}" = "--keep-db" ] && KEEP_DB=1

PREV="${BM_APP_DIR}/${BM_BINARY}.prev"
[ -f "${PREV}" ] || bm_die "no previous binary at ${PREV}; nothing to roll back to"

bm_stop_service

# Restore the previous binary, keeping the current one as .failed for inspection.
if [ -f "${BM_APP_DIR}/${BM_BINARY}" ]; then
  mv -f "${BM_APP_DIR}/${BM_BINARY}" "${BM_APP_DIR}/${BM_BINARY}.failed"
fi
cp -a "${PREV}" "${BM_APP_DIR}/${BM_BINARY}"
bm_ok "restored the previous binary"

if [ "${KEEP_DB}" -eq 0 ]; then
  # Restore the newest pre-update snapshot.
  snap="$(ls -1t "${BM_DATA_DIR}"/backups/pre-update-*.db 2>/dev/null | head -n1 || true)"
  if [ -n "${snap}" ]; then
    bm_info "restoring database snapshot ${snap}"
    # Move the current db aside first so it is not lost.
    if [ -f "${BM_DATA_DIR}/database/beermate.db" ]; then
      mv -f "${BM_DATA_DIR}/database/beermate.db" \
        "${BM_DATA_DIR}/database/beermate.db.rolledback-$(date -u +%Y%m%d-%H%M%S)"
    fi
    # Remove any stale WAL so it cannot replay over the restored file.
    rm -f "${BM_DATA_DIR}/database/beermate.db-wal" "${BM_DATA_DIR}/database/beermate.db-shm"
    cp -a "${snap}" "${BM_DATA_DIR}/database/beermate.db"
    chown "${BM_USER}:${BM_GROUP}" "${BM_DATA_DIR}/database/beermate.db"
    bm_ok "database restored"
  else
    bm_warn "no pre-update database snapshot found; keeping the current database"
  fi
else
  bm_info "keeping the current database (--keep-db)"
fi

systemctl start "${BM_SERVICE}"
if bm_wait_health 30; then
  bm_ok "rollback complete and healthy"
else
  bm_warn "service did not become healthy after rollback; check the logs"
  exit 1
fi
