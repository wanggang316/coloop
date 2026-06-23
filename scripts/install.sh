#!/usr/bin/env bash
#
# install.sh — build and install the-loop, seed its config, and optionally set
# up start-at-boot via launchd (macOS) or a per-user systemd service (Linux).
#
# Usage:
#   ./scripts/install.sh [options]
#
# Options:
#   --prefix DIR    install the binary into DIR/bin            (default: /usr/local)
#   --config PATH   config file to seed / point the service at (default: ~/.the-loop/config.yaml)
#   --service       also install and enable the autostart service
#   --no-build      skip building; use a prebuilt binary from dist/
#   -h, --help      show this help and exit
#
# Idempotent: re-running upgrades the binary and refreshes the service without
# touching an existing config.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ---- defaults ----------------------------------------------------------------
PREFIX="/usr/local"
CONFIG=""
WITH_SERVICE=0
NO_BUILD=0
LABEL="com.theloop.the-loop"
BINARY="the-loop"

# ---- output helpers ----------------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_BLUE='\033[34m'; C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_RED='\033[31m'; C_DIM='\033[2m'; C_OFF='\033[0m'
else
  C_BLUE=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_DIM=''; C_OFF=''
fi
info()  { printf "${C_BLUE}==>${C_OFF} %s\n" "$*"; }
ok()    { printf "${C_GREEN}  ✓${C_OFF} %s\n" "$*"; }
warn()  { printf "${C_YELLOW}  !${C_OFF} %s\n" "$*"; }
die()   { printf "${C_RED}error:${C_OFF} %s\n" "$*" >&2; exit 1; }

usage() { awk 'NR>2 && /^#/ {sub(/^# ?/,""); print; next} NR>2 {exit}' "${BASH_SOURCE[0]}"; }

# ---- arg parsing -------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)   PREFIX="${2:?--prefix needs a directory}"; shift 2 ;;
    --config)   CONFIG="${2:?--config needs a path}"; shift 2 ;;
    --service)  WITH_SERVICE=1; shift ;;
    --no-build) NO_BUILD=1; shift ;;
    -h|--help)  usage; exit 0 ;;
    *)          die "unknown option: $1 (try --help)" ;;
  esac
done

# Default config under the invoking user's home, with ~ expanded.
if [ -z "$CONFIG" ]; then
  CONFIG="$HOME/.the-loop/config.yaml"
else
  case "$CONFIG" in "~"/*) CONFIG="$HOME/${CONFIG#\~/}" ;; esac
fi
BIN_DIR="$PREFIX/bin"
BIN_PATH="$BIN_DIR/$BINARY"
CONFIG_DIR="$(dirname "$CONFIG")"

# ---- platform detection ------------------------------------------------------
case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             die "unsupported architecture: $(uname -m)" ;;
esac

# Walk up to the first path component that actually exists.
nearest_existing() {
  local d="$1"
  while [ ! -e "$d" ] && [ "$d" != "/" ] && [ "$d" != "." ] && [ -n "$d" ]; do
    d="$(dirname "$d")"
  done
  printf '%s' "$d"
}

# sudo only when the destination tree is not writable; surface the reason first.
maybe_sudo() {
  local probe; probe="$(nearest_existing "$1")"
  if [ -w "$probe" ]; then SUDO=""; return; fi
  command -v sudo >/dev/null 2>&1 || die "$1 is not writable and sudo is unavailable; re-run with --prefix \$HOME/.local"
  warn "$probe needs elevated permissions; sudo may prompt for your password"
  SUDO="sudo"
}

# ---- 1. obtain the binary ----------------------------------------------------
TMP_BIN=""
cleanup() { [ -n "$TMP_BIN" ] && rm -f "$TMP_BIN" 2>/dev/null || true; }
trap cleanup EXIT

if [ "$NO_BUILD" -eq 0 ] && command -v go >/dev/null 2>&1; then
  info "Building $BINARY from source (go build)"
  TMP_BIN="$(mktemp -t the-loop.XXXXXX)"
  ( cd "$REPO_ROOT" && go build -ldflags "-s -w" -o "$TMP_BIN" ./cmd/the-loop )
  SRC_BIN="$TMP_BIN"
  ok "built $(go version | awk '{print $3}')"
else
  PREBUILT="$REPO_ROOT/dist/$BINARY-$OS-$ARCH"
  [ "$NO_BUILD" -eq 1 ] || warn "go toolchain not found; falling back to a prebuilt binary"
  [ -x "$PREBUILT" ] || die "no prebuilt binary at $PREBUILT — run 'make dist' on a machine with Go, or install Go and re-run"
  info "Using prebuilt binary dist/$BINARY-$OS-$ARCH"
  SRC_BIN="$PREBUILT"
fi

# ---- 2. install the binary ---------------------------------------------------
info "Installing binary -> $BIN_PATH"
maybe_sudo "$BIN_DIR"
$SUDO mkdir -p "$BIN_DIR"
$SUDO install -m 0755 "$SRC_BIN" "$BIN_PATH"
ok "installed"

# ---- 3. seed the config ------------------------------------------------------
if [ -f "$CONFIG" ]; then
  info "Config already exists -> $CONFIG (left untouched)"
else
  info "Seeding config -> $CONFIG"
  mkdir -p "$CONFIG_DIR"
  cp "$REPO_ROOT/config.example.yaml" "$CONFIG"
  ok "copied from config.example.yaml — edit it to add tasks"
fi

# ---- 4. autostart service (optional) ----------------------------------------
install_launchd() {
  local plist="$HOME/Library/LaunchAgents/$LABEL.plist"
  local log="$CONFIG_DIR/the-loop.log"
  info "Installing launchd agent -> $plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  sed -e "s|@BIN@|$BIN_PATH|g" -e "s|@CONFIG@|$CONFIG|g" -e "s|@LOG@|$log|g" \
    "$REPO_ROOT/deploy/com.theloop.the-loop.plist" > "$plist"
  launchctl unload "$plist" >/dev/null 2>&1 || true   # ignore "not loaded"
  launchctl load -w "$plist"
  ok "loaded; logs at $log"
}

install_systemd_user() {
  local unit_dir="$HOME/.config/systemd/user"
  local unit="$unit_dir/the-loop.service"
  command -v systemctl >/dev/null 2>&1 || die "systemctl not found; this Linux has no systemd (set up autostart manually)"
  info "Installing systemd user service -> $unit"
  mkdir -p "$unit_dir"
  sed -e "s|@BIN@|$BIN_PATH|g" -e "s|@CONFIG@|$CONFIG|g" \
    "$REPO_ROOT/deploy/the-loop.service" > "$unit"
  systemctl --user daemon-reload
  systemctl --user enable --now the-loop
  if loginctl enable-linger "$USER" >/dev/null 2>&1; then
    ok "enabled; lingering on so it survives logout/boot"
  else
    warn "could not enable lingering; run 'sudo loginctl enable-linger $USER' for start-at-boot"
  fi
  ok "started; logs via 'journalctl --user -u the-loop -f'"
}

if [ "$WITH_SERVICE" -eq 1 ]; then
  case "$OS" in
    darwin) install_launchd ;;
    linux)  install_systemd_user ;;
  esac

  # Best-effort health probe so the user sees green, not silence.
  listen="$(awk -F'"' '/^listen:/ {print $2}' "$CONFIG" 2>/dev/null)"
  listen="${listen:-127.0.0.1:8765}"
  if command -v curl >/dev/null 2>&1; then
    for _ in 1 2 3 4 5; do
      if curl -fsS -m 2 "http://$listen/healthz" >/dev/null 2>&1; then
        ok "daemon responding on http://$listen"
        break
      fi
      sleep 1
    done
  fi
fi

# ---- 5. summary --------------------------------------------------------------
echo
info "Done."
echo "  binary : $BIN_PATH"
echo "  config : $CONFIG"
[ "$WITH_SERVICE" -eq 1 ] && echo "  service: enabled (start-at-boot)" || echo "  service: not installed (re-run with --service to enable autostart)"
echo
case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *) warn "$BIN_DIR is not on your PATH — add it, e.g.:"
     printf "      ${C_DIM}echo 'export PATH=\"%s:\$PATH\"' >> ~/.zshrc${C_OFF}\n" "$BIN_DIR" ;;
esac
if [ "$WITH_SERVICE" -eq 0 ]; then
  echo "  Start it now:  $BINARY --config $CONFIG serve"
fi
echo "  Open the UI:   http://127.0.0.1:8765"
