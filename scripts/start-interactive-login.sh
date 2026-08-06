#!/usr/bin/env bash
#
# start-interactive-login.sh — open an authenticated website for manual login.
#
# A thin convenience wrapper: it asks the running service to open the website on
# the Jetson's real display so an administrator can complete the login by hand
# (MFA, SSO, cookie consent, captcha all work because a human drives a real
# browser). The heavy lifting is in the service; this exists so the login flow is
# also reachable from a shell.
#
# Usage: start-interactive-login.sh <website-id>
#        BEERMATE_SESSION=<session> start-interactive-login.sh <website-id>
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

bm_need curl
WEBSITE_ID="${1:-}"
[ -n "${WEBSITE_ID}" ] || bm_die "usage: $0 <website-id>"

if [ -z "${BEERMATE_SESSION:-}" ]; then
  bm_die "set BEERMATE_SESSION to an admin session cookie value (see import-legacy-config.sh for how)"
fi

csrf="$(curl -fsS --max-time 5 \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  "http://127.0.0.1:8080/api/v1/auth/me" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')"

bm_info "opening website ${WEBSITE_ID} for interactive login on the Jetson display"
curl -fsS --max-time 30 -X POST \
  -H "Cookie: beermate_session=${BEERMATE_SESSION}" \
  -H "X-BeerMate-CSRF: ${csrf}" \
  "http://127.0.0.1:8080/api/v1/websites/${WEBSITE_ID}/prepare-login"

echo
bm_ok "the site should now be open on the Jetson screen"
echo "  Log in there with a keyboard and mouse, or over an independently secured"
echo "  remote desktop reached via Tailscale. When done, run:"
echo "    ${SCRIPT_DIR}/stop-interactive-login.sh ${WEBSITE_ID}"
