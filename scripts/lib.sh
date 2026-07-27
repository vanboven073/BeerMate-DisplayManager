#!/usr/bin/env bash
#
# lib.sh — shared constants and helpers for the BeerMate deployment scripts.
#
# Sourced by the other scripts. Keeping the complex logic here (rather than in
# long heredocs pasted over QBee) means the deployment commands an operator runs
# stay short.
set -Eeuo pipefail

# ---- identity ------------------------------------------------------------
readonly BM_SERVICE="beermate-display-manager"
readonly BM_USER="beermate"
readonly BM_GROUP="beermate"
readonly BM_APP_DIR="/opt/BeerMateDisplayManager"
readonly BM_DATA_DIR="/var/lib/beermate-display-manager"
readonly BM_CONFIG_DIR="/etc/beermate-display-manager"
readonly BM_BINARY="beermate-display-manager"
readonly BM_SYSTEMD_UNIT="/etc/systemd/system/${BM_SERVICE}.service"
readonly BM_SECRET_FILE="${BM_CONFIG_DIR}/secret.key"
readonly BM_CONFIG_FILE="${BM_CONFIG_DIR}/config.json"

# ---- output --------------------------------------------------------------
if [ -t 1 ]; then
  readonly C_RESET=$'\033[0m'; readonly C_BLUE=$'\033[34m'
  readonly C_GREEN=$'\033[32m'; readonly C_YELLOW=$'\033[33m'; readonly C_RED=$'\033[31m'
else
  readonly C_RESET=""; readonly C_BLUE=""; readonly C_GREEN=""; readonly C_YELLOW=""; readonly C_RED=""
fi

bm_info()  { echo "${C_BLUE}==>${C_RESET} $*"; }
bm_ok()    { echo "${C_GREEN} ok${C_RESET} $*"; }
bm_warn()  { echo "${C_YELLOW}warn${C_RESET} $*" >&2; }
bm_error() { echo "${C_RED}err ${C_RESET} $*" >&2; }
bm_die()   { bm_error "$*"; exit 1; }

# bm_require_root ensures the script runs with the privileges it needs. The
# deployment runs from a QBee root shell, so this is normally satisfied.
bm_require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    bm_die "this script must run as root (it manages system directories and systemd)"
  fi
}

# bm_need checks a command exists.
bm_need() {
  command -v "$1" >/dev/null 2>&1 || bm_die "required command not found: $1"
}

# bm_ensure_user creates the service user and group if missing. The user gets no
# login shell: it exists only to own the service and its data.
bm_ensure_user() {
  if ! getent group "${BM_GROUP}" >/dev/null 2>&1; then
    bm_info "creating group ${BM_GROUP}"
    groupadd --system "${BM_GROUP}"
  fi
  if ! id "${BM_USER}" >/dev/null 2>&1; then
    bm_info "creating user ${BM_USER}"
    useradd --system --gid "${BM_GROUP}" --home-dir "/home/${BM_USER}" \
      --shell /usr/sbin/nologin "${BM_USER}"
  fi
}

# bm_ensure_dirs creates the directory tree with correct ownership and modes.
# Data is 0750 (private to the service user); config is 0755 so it is readable
# for troubleshooting, but the secret key inside it is locked down separately.
bm_ensure_dirs() {
  local d
  install -d -o "${BM_USER}" -g "${BM_GROUP}" -m 0750 "${BM_DATA_DIR}"
  for d in database uploads thumbnails browser-profiles backups cache social-cache runtime; do
    install -d -o "${BM_USER}" -g "${BM_GROUP}" -m 0750 "${BM_DATA_DIR}/${d}"
  done
  install -d -o root -g "${BM_GROUP}" -m 0755 "${BM_CONFIG_DIR}"
  install -d -o root -g root -m 0755 "${BM_APP_DIR}"
  install -d -o root -g root -m 0755 "${BM_APP_DIR}/scripts"
}

# bm_generate_secret writes a fresh 32-byte base64 key with mode 0600 if none
# exists. The key encrypts stored API tokens and must never be regenerated on an
# existing install, or every stored credential becomes undecryptable — hence the
# existence check.
bm_generate_secret() {
  if [ -f "${BM_SECRET_FILE}" ]; then
    bm_ok "encryption key already present; leaving it untouched"
    return 0
  fi
  bm_info "generating encryption key"
  umask 077
  head -c 32 /dev/urandom | base64 > "${BM_SECRET_FILE}"
  chown root:"${BM_GROUP}" "${BM_SECRET_FILE}"
  chmod 0640 "${BM_SECRET_FILE}"
  bm_ok "encryption key written to ${BM_SECRET_FILE} (mode 0640, group ${BM_GROUP})"
}

# bm_service_active reports whether the service is running.
bm_service_active() {
  systemctl is-active --quiet "${BM_SERVICE}"
}

# bm_stop_service stops the service if it is running.
bm_stop_service() {
  if bm_service_active; then
    bm_info "stopping ${BM_SERVICE}"
    systemctl stop "${BM_SERVICE}"
  fi
}

# bm_health_ok probes the local health endpoint.
bm_health_ok() {
  curl -fsS --max-time 3 "http://127.0.0.1:8080/health" >/dev/null 2>&1
}

# bm_wait_health waits up to N seconds for the service to become healthy.
bm_wait_health() {
  local tries="${1:-30}"
  local i
  for i in $(seq 1 "${tries}"); do
    if bm_health_ok; then
      return 0
    fi
    sleep 1
  done
  return 1
}
