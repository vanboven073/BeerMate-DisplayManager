#!/usr/bin/env bash
#
# restore.sh — restore the database from a backup archive.
#
# Verifies the archive, takes a safety snapshot of the current database, then
# swaps in the restored one and restarts the service. Destructive by nature, so
# it requires an explicit confirmation.
#
# Usage: restore.sh /path/to/beermate-backup-...tar.gz
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_require_root
bm_need tar

ARCHIVE="${1:-}"
[ -n "${ARCHIVE}" ] || bm_die "usage: $0 <backup-archive.tar.gz>"
[ -f "${ARCHIVE}" ] || bm_die "archive not found: ${ARCHIVE}"

bm_info "inspecting archive ${ARCHIVE}"
# Verify it contains the expected members before touching anything.
if ! tar -tzf "${ARCHIVE}" | grep -q '^database/beermate.db$'; then
  bm_die "archive does not contain database/beermate.db; refusing to restore"
fi
if ! tar -tzf "${ARCHIVE}" | grep -q '^manifest.json$'; then
  bm_warn "archive has no manifest.json; continuing but this may not be a BeerMate backup"
fi

echo "${C_YELLOW}This will replace the current database with the one in the archive.${C_RESET}"
echo "A safety snapshot of the current database is taken first."
echo "Type 'RESTORE' to proceed:"
read -r confirm
[ "${confirm}" = "RESTORE" ] || bm_die "confirmation did not match; aborting"

bm_stop_service

DB="${BM_DATA_DIR}/database/beermate.db"
ts="$(date -u +%Y%m%d-%H%M%S)"
if [ -f "${DB}" ]; then
  snap="${BM_DATA_DIR}/backups/pre-restore-${ts}.db"
  bm_info "snapshotting the current database to ${snap}"
  cp -a "${DB}" "${snap}"
  chown "${BM_USER}:${BM_GROUP}" "${snap}"
fi

staging="$(mktemp -d)"
trap 'rm -rf "${staging}"' EXIT
tar -xzf "${ARCHIVE}" -C "${staging}" database/beermate.db

# Validate the extracted database opens and passes an integrity check if sqlite3
# is available, before it goes live.
if command -v sqlite3 >/dev/null 2>&1; then
  bm_info "integrity-checking the extracted database"
  result="$(sqlite3 "${staging}/database/beermate.db" 'PRAGMA integrity_check;' 2>&1 || echo 'error')"
  [ "${result}" = "ok" ] || bm_die "extracted database failed integrity check: ${result}"
fi

bm_info "swapping in the restored database"
# Remove any stale WAL so it cannot replay over the restored file.
rm -f "${DB}-wal" "${DB}-shm"
cp -a "${staging}/database/beermate.db" "${DB}"
chown "${BM_USER}:${BM_GROUP}" "${DB}"

systemctl start "${BM_SERVICE}"
if bm_wait_health 30; then
  bm_ok "restore complete and healthy"
else
  bm_warn "service did not become healthy; the pre-restore snapshot is at ${snap:-<none>}"
  exit 1
fi
