#!/usr/bin/env bash
#
# verify-installation.sh — post-install sanity checks.
#
# Non-destructive. Reports a pass/fail summary and exits non-zero if any critical
# check fails, so it can gate an automated rollout.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

FAIL=0
pass() { bm_ok "$*"; }
fail() { bm_error "$*"; FAIL=1; }

bm_info "verifying BeerMate Display Manager installation"

# 1. Binary present and executable.
if [ -x "${BM_APP_DIR}/${BM_BINARY}" ]; then
  pass "binary present"
  if "${BM_APP_DIR}/${BM_BINARY}" -version >/dev/null 2>&1; then
    pass "binary runs: $("${BM_APP_DIR}/${BM_BINARY}" -version 2>/dev/null | head -n1)"
  else
    fail "binary will not report its version"
  fi
else
  fail "binary missing or not executable at ${BM_APP_DIR}/${BM_BINARY}"
fi

# 2. Service enabled and active.
if systemctl is-enabled --quiet "${BM_SERVICE}"; then pass "service enabled"; else fail "service not enabled"; fi
if bm_service_active; then pass "service active"; else fail "service not active"; fi

# 3. Directories and ownership.
for d in "${BM_DATA_DIR}" "${BM_DATA_DIR}/database" "${BM_DATA_DIR}/uploads" \
         "${BM_DATA_DIR}/browser-profiles" "${BM_DATA_DIR}/backups"; do
  if [ -d "${d}" ]; then pass "directory ${d}"; else fail "missing directory ${d}"; fi
done
owner="$(stat -c '%U' "${BM_DATA_DIR}" 2>/dev/null || echo '?')"
if [ "${owner}" = "${BM_USER}" ]; then pass "data dir owned by ${BM_USER}"; else fail "data dir owned by ${owner}, expected ${BM_USER}"; fi

# 4. Encryption key present and locked down.
if [ -f "${BM_SECRET_FILE}" ]; then
  mode="$(stat -c '%a' "${BM_SECRET_FILE}")"
  if [ "${mode}" = "640" ] || [ "${mode}" = "600" ]; then
    pass "encryption key present (mode ${mode})"
  else
    fail "encryption key has mode ${mode}; expected 600 or 640"
  fi
else
  fail "encryption key missing at ${BM_SECRET_FILE}"
fi

# 5. Health endpoint.
if bm_health_ok; then
  pass "health endpoint responds"
  # 6. The admin server binds 0.0.0.0:8080, the browser debug port stays local.
  if ss -ltn 2>/dev/null | grep -qE '(:8080)\b'; then pass "listening on port 8080"; else bm_warn "could not confirm port 8080 via ss"; fi
  # The DevTools port, if the browser is running, must be loopback only.
  if ss -ltn 2>/dev/null | grep -E ':9222\b' | grep -qvE '127\.0\.0\.1|\[::1\]'; then
    fail "the DevTools port 9222 is bound to a non-loopback address"
  else
    pass "DevTools port is loopback-only or not open"
  fi
else
  fail "health endpoint did not respond"
fi

# 7. Player autostart entry.
if [ -f "/home/${BM_USER}/.config/autostart/beermate-player.desktop" ]; then
  pass "player autostart entry present"
else
  bm_warn "player autostart entry missing (the display will not open Chromium on login)"
fi

echo
if [ "${FAIL}" -eq 0 ]; then
  bm_ok "all critical checks passed"
  exit 0
else
  bm_error "one or more critical checks failed"
  exit 1
fi
