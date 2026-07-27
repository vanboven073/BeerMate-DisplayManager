#!/usr/bin/env bash
#
# backup.sh — create a backup archive from the command line.
#
# A local convenience for cron or manual runs. It checkpoints the WAL and copies
# a consistent database plus a manifest into the backups directory, mirroring
# what the dashboard's Create Backup button does. It runs directly on the files
# so it works even if the service is stopped.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_need tar
bm_need sha256sum

DB="${BM_DATA_DIR}/database/beermate.db"
[ -f "${DB}" ] || bm_die "database not found at ${DB}"

ts="$(date -u +%Y%m%d-%H%M%S)"
rand="$(head -c2 /dev/urandom | od -An -tx1 | tr -d ' \n')"
archive="${BM_DATA_DIR}/backups/beermate-backup-manual-${ts}-${rand}.tar.gz"
staging="$(mktemp -d)"
trap 'rm -rf "${staging}"' EXIT

bm_info "checkpointing the database"
# Best-effort WAL checkpoint via sqlite3 if present; otherwise copy as-is (the
# service checkpoints on shutdown, so a stopped service has a clean file).
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "${DB}" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null 2>&1 || true
fi

mkdir -p "${staging}/database"
cp -a "${DB}" "${staging}/database/beermate.db"

schema_ver="$(command -v sqlite3 >/dev/null 2>&1 && sqlite3 "${DB}" 'SELECT MAX(version) FROM schema_migrations;' 2>/dev/null || echo 0)"
cat > "${staging}/manifest.json" <<JSON
{
  "version": "1",
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "created_by": "backup.sh",
  "kind": "manual",
  "schema_version": ${schema_ver:-0},
  "includes": ["database","media_metadata","settings","playlist","scenes","schedule","social_config","website_metadata"],
  "excludes": ["browser_profiles","encryption_key","session_cookies","raw_media_files"]
}
JSON

bm_info "writing archive"
tar -czf "${archive}" -C "${staging}" manifest.json database
chown "${BM_USER}:${BM_GROUP}" "${archive}" 2>/dev/null || true

sum="$(sha256sum "${archive}" | cut -d' ' -f1)"
bm_ok "backup created: ${archive}"
echo "  size:   $(du -h "${archive}" | cut -f1)"
echo "  sha256: ${sum}"

# Enforce a simple retention on command-line backups too, so cron cannot fill
# the disk. Keep the newest 20 manual backups made by this script.
mapfile -t old < <(ls -1t "${BM_DATA_DIR}"/backups/beermate-backup-manual-*.tar.gz 2>/dev/null | tail -n +21 || true)
for f in "${old[@]:-}"; do
  [ -n "${f}" ] && rm -f "${f}" && bm_info "pruned old backup ${f}"
done
