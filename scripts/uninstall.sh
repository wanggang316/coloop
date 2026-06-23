#!/usr/bin/env bash
#
# uninstall.sh — stop and remove the-loop: the autostart service and the binary.
# Run records and config under ~/.the-loop are kept unless you pass --purge.
#
# Usage:
#   ./scripts/uninstall.sh [options]
#
# Options:
#   --prefix DIR    where the binary was installed              (default: /usr/local)
#   --config PATH   config/data location (for --purge)          (default: ~/.the-loop/config.yaml)
#   --purge         also delete the config and run records (~/.the-loop)
#   -h, --help      show this help and exit
set -euo pipefail

# ---- defaults ----------------------------------------------------------------
PREFIX="/usr/local"
CONFIG=""
PURGE=0
LABEL="com.theloop.the-loop"
BINARY="the-loop"

# ---- output helpers ----------------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_BLUE='\033[34m'; C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_RED='\033[31m'; C_OFF='\033[0m'
else
  C_BLUE=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_OFF=''
fi
info() { printf "${C_BLUE}==>${C_OFF} %s\n" "$*"; }
ok()   { printf "${C_GREEN}  ✓${C_OFF} %s\n" "$*"; }
warn() { printf "${C_YELLOW}  !${C_OFF} %s\n" "$*"; }
die()  { printf "${C_RED}error:${C_OFF} %s\n" "$*" >&2; exit 1; }

usage() { awk 'NR>2 && /^#/ {sub(/^# ?/,""); print; next} NR>2 {exit}' "${BASH_SOURCE[0]}"; }

# ---- arg parsing -------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="${2:?--prefix needs a directory}"; shift 2 ;;
    --config) CONFIG="${2:?--config needs a path}"; shift 2 ;;
    --purge)  PURGE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)        die "unknown option: $1 (try --help)" ;;
  esac
done

if [ -z "$CONFIG" ]; then
  CONFIG="$HOME/.the-loop/config.yaml"
else
  case "$CONFIG" in "~"/*) CONFIG="$HOME/${CONFIG#\~/}" ;; esac
fi
BIN_PATH="$PREFIX/bin/$BINARY"
DATA_DIR="$(dirname "$CONFIG")"

case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *)      OS="other" ;;
esac

# Walk up to the first path component that actually exists.
nearest_existing() {
  local d="$1"
  while [ ! -e "$d" ] && [ "$d" != "/" ] && [ "$d" != "." ] && [ -n "$d" ]; do
    d="$(dirname "$d")"
  done
  printf '%s' "$d"
}

# Removing a file needs write permission on its directory, not the file itself.
maybe_sudo() {
  local probe; probe="$(nearest_existing "$1")"
  if [ -w "$probe" ]; then SUDO=""; return; fi
  command -v sudo >/dev/null 2>&1 || die "$probe is not writable and sudo is unavailable"
  warn "$probe needs elevated permissions; sudo may prompt for your password"
  SUDO="sudo"
}

# ---- 1. stop & remove the autostart service ----------------------------------
case "$OS" in
  darwin)
    plist="$HOME/Library/LaunchAgents/$LABEL.plist"
    if [ -f "$plist" ]; then
      info "Removing launchd agent"
      launchctl unload "$plist" >/dev/null 2>&1 || true
      rm -f "$plist"
      ok "removed $plist"
    else
      info "No launchd agent installed"
    fi
    ;;
  linux)
    unit="$HOME/.config/systemd/user/the-loop.service"
    if [ -f "$unit" ] && command -v systemctl >/dev/null 2>&1; then
      info "Removing systemd user service"
      systemctl --user disable --now the-loop >/dev/null 2>&1 || true
      rm -f "$unit"
      systemctl --user daemon-reload >/dev/null 2>&1 || true
      ok "removed $unit"
    else
      info "No systemd user service installed"
    fi
    ;;
esac

# ---- 2. remove the binary ----------------------------------------------------
if [ -e "$BIN_PATH" ] || [ -L "$BIN_PATH" ]; then
  info "Removing binary -> $BIN_PATH"
  maybe_sudo "$(dirname "$BIN_PATH")"
  $SUDO rm -f "$BIN_PATH"
  ok "removed"
else
  info "No binary at $BIN_PATH"
fi

# ---- 3. config & run records -------------------------------------------------
if [ "$PURGE" -eq 1 ]; then
  if [ -d "$DATA_DIR" ]; then
    warn "Deleting config and run records: $DATA_DIR"
    rm -rf "$DATA_DIR"
    ok "purged $DATA_DIR"
  fi
else
  info "Kept config and run records: $DATA_DIR  (use --purge to delete)"
fi

echo
ok "the-loop uninstalled."
