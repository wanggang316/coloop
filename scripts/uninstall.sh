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

# Quote a string for safe use inside a /bin/sh -c command.
shq() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
# Escape a string for embedding in an AppleScript "do shell script" literal.
asq() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

# Run a /bin/sh command string as root using whatever works here: nothing if the
# target dir is writable, then cached sudo, then a macOS GUI prompt (no TTY), then
# an interactive sudo prompt. Mirrors install.sh so /usr/local installs reverse cleanly.
#   $1 = path being modified (for the writable check)   $2 = command string
run_root() {
  local target="$1" cmd="$2"
  if [ -w "$(nearest_existing "$target")" ]; then sh -c "$cmd"; return; fi
  if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo /bin/sh -c "$cmd"; return
  fi
  if [ "$OS" = "darwin" ] && command -v osascript >/dev/null 2>&1; then
    warn "$target needs admin rights — a macOS authorization dialog will appear"
    osascript -e "do shell script \"$(asq "$cmd")\" with administrator privileges" >/dev/null
    return
  fi
  if command -v sudo >/dev/null 2>&1 && [ -t 0 ]; then
    warn "$target needs elevated permissions; sudo may prompt for your password"
    sudo /bin/sh -c "$cmd"; return
  fi
  die "cannot modify $target without privileges; remove it manually or re-run as root"
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
  run_root "$(dirname "$BIN_PATH")" "/bin/rm -f $(shq "$BIN_PATH")"
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
