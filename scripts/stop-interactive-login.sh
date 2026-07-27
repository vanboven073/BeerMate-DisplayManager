#!/usr/bin/env bash
#
# stop-interactive-login.sh — finish an interactive login and validate it.
#
# Closes the interactive browser (which flushes the session cookies to the
# persistent profile so they survive a reboot) and validates that the target URL
# now opens without redirecting to a login page.
#
# Usage: BEERMATE_SESSION=<session> stop-interactive-login.sh <website-id>
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_need curl
WEBSITE_ID="${1:-}"
[ -n "${WEBSITE_ID}" ] || bm_die "usage: $0 <website-id>"
[ -n "${BEERMATE_SESSION:-}" ] || bm_die "set BEERMATE_SESSION to an admin session cookie value"

csrf="$(curl -fsS --max-time 5 \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  "http://127.0.0.1:8080/api/v1/auth/me" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')"

bm_info "finishing login for website ${WEBSITE_ID} and validating the session"
resp="$(curl -fsS --max-time 45 -X POST \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  -H "X-BeerMate-CSRF: ${csrf}" \
  "http://127.0.0.1:8080/api/v1/websites/${WEBSITE_ID}/finish-login")"

echo "${resp}"
if echo "${resp}" | grep -q '"session_state":"active"'; then
  bm_ok "the session is active; the player will reuse it"
else
  bm_warn "the session may not be fully signed in; check the dashboard and re-run the login if needed"
fi
