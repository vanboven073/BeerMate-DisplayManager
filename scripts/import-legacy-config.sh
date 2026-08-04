#!/usr/bin/env bash
#
# import-legacy-config.sh — migrate the old Bash kiosk content.
#
# Detects the legacy files, takes a timestamped backup of the old setup, and
# calls the running service's migration endpoint to recreate the roadmap image,
# the dashboard website and the 14 August 2026 countdown as a DRAFT playlist.
# It never deletes the old kiosk, so rollback stays possible; nothing is
# published — an operator reviews the draft and publishes it from the dashboard.
#
# This talks to the API, so an administrator must already exist and you must pass
# a session cookie, OR run it interactively after signing in. To keep the
# operator's manual steps short, it accepts a session token via BEERMATE_SESSION.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_need curl

LEGACY_KIOSK_DIR="/home/${BM_USER}/beermate-kiosk"
LEGACY_ROADMAP="/home/${BM_USER}/Pictures/Roadmap_2026.png"
LEGACY_SCRIPT="/home/${BM_USER}/bin/beermate-kiosk.sh"
LEGACY_AUTOSTART="/home/${BM_USER}/.config/autostart/beermate-kiosk.desktop"
DASHBOARD_URL="https://dashboard.beermatecloud.eu/dashboard/44-beermate-lifetime-dashboard"

bm_info "detecting legacy kiosk files"
found=0
[ -f "${LEGACY_ROADMAP}" ] && { bm_ok "found roadmap image: ${LEGACY_ROADMAP}"; found=1; } || bm_warn "roadmap image not found at ${LEGACY_ROADMAP}"
[ -f "${LEGACY_SCRIPT}" ] && { bm_ok "found kiosk script: ${LEGACY_SCRIPT}"; found=1; } || bm_warn "kiosk script not found"
[ -d "${LEGACY_KIOSK_DIR}" ] && bm_ok "found kiosk directory: ${LEGACY_KIOSK_DIR}"

# Timestamped backup of the entire old setup, so nothing is ever lost.
ts="$(date -u +%Y%m%d-%H%M%S)"
backup_root="${BM_DATA_DIR}/backups/legacy-kiosk-${ts}"
bm_info "backing up the old kiosk to ${backup_root}"
mkdir -p "${backup_root}"
for src in "${LEGACY_KIOSK_DIR}" "${LEGACY_ROADMAP}" "${LEGACY_SCRIPT}" "${LEGACY_AUTOSTART}"; do
  if [ -e "${src}" ]; then
    cp -a "${src}" "${backup_root}/" 2>/dev/null || bm_warn "could not copy ${src}"
  fi
done
bm_ok "old kiosk preserved at ${backup_root} (nothing was deleted)"

if [ "${found}" -eq 0 ]; then
  bm_warn "no legacy files detected; the import will still create the dashboard and countdown"
fi

# Call the migration endpoint. Requires an authenticated session.
if [ -z "${BEERMATE_SESSION:-}" ]; then
  cat <<EOF

${C_YELLOW}Manual step required.${C_RESET}
The importer needs an authenticated admin session token. Get one either way:

A) On this device, no browser needed (replace admin with your username):

     read -rp  'Admin username: ' BM_ADMIN
     read -rsp 'Admin password: ' BM_PW; echo
     BEERMATE_SESSION="\$(printf '{"username":"%s","password":"%s"}' "\${BM_ADMIN}" "\${BM_PW}" \\
       | curl -fsS -i -X POST -H 'Content-Type: application/json' -d @- \\
         http://127.0.0.1:8080/api/v1/auth/login \\
       | tr -d '\\r' | sed -n 's/^[Ss]et-[Cc]ookie: beermate_session=\\([^;]*\\).*/\\1/p')"
     unset BM_PW

B) In the browser, signed in at http://<tailscale-ip>:8080/admin:
   DevTools (F12) -> Application -> Storage -> Cookies -> pick the origin ->
   copy the beermate_session Value.

   Note: 'document.cookie' does NOT work. The session cookie is HttpOnly by
   design so JavaScript can never read it; only the DevTools cookie panel and
   the network layer can see it.

Then re-run:

  BEERMATE_SESSION=<value> ${SCRIPT_DIR}/import-legacy-config.sh

Alternatively, trigger the import from the dashboard once that button is added.
The old kiosk backup is already saved at:
  ${backup_root}
EOF
  exit 0
fi

bm_info "requesting legacy import via the API"
csrf="$(curl -fsS --max-time 5 \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  "http://127.0.0.1:8080/api/v1/auth/me" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')"

resp="$(curl -fsS --max-time 30 -X POST \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  -H "X-BeerMate-CSRF: ${csrf}" \
  -H "Content-Type: application/json" \
  -d "{\"roadmap_image\":\"${LEGACY_ROADMAP}\",\"dashboard_url\":\"${DASHBOARD_URL}\"}" \
  "http://127.0.0.1:8080/api/v1/migrate/legacy")"

echo "${resp}"
bm_ok "import requested. Review the draft playlist in the dashboard and publish it."
echo "  To switch the display to the new player, the autostart entry is already"
echo "  installed. To restore the old kiosk, re-enable ${LEGACY_AUTOSTART} from ${backup_root}."
