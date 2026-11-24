#!/usr/bin/env bash
# Installs lqg: creates the "lqg" group, installs the
# binary + systemd unit + a sample config (only if one isn't already
# present — never overwrites a real deployment's config). Must run as
# root. /run/lqg itself is NOT created here — the unit's
# RuntimeDirectory=lqg handles that on every start, correctly, since
# /run is tmpfs and anything created by hand here wouldn't survive a
# reboot.
#
# Usage: sudo ./install.sh [path-to-lqg-binary]
set -euo pipefail

BINARY="${1:-./lqg}"
PREFIX="/usr/local/bin"
UNIT_DIR="/etc/systemd/system"
CONFIG_DIR="/etc/lqg"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $EUID -ne 0 ]]; then
  echo "install.sh must run as root (sudo ./install.sh)" >&2
  exit 1
fi

if [[ ! -x "$BINARY" ]]; then
  echo "lqg binary not found or not executable: $BINARY" >&2
  echo "build it first: CGO_ENABLED=0 go build -o lqg ./cmd/lqg" >&2
  exit 1
fi

if ! getent group lqg >/dev/null; then
  groupadd --system lqg
  echo "created group: lqg"
else
  echo "group already exists: lqg"
fi

# Let the admin who ran `sudo ./install.sh` use `lqg status`/`lqg watch`
# without sudo: the control socket is root:lqg 0660 inside a root:lqg 0750
# runtime dir, so only lqg group members can reach it.
ADDED_USER=""
if [[ -n "${SUDO_USER:-}" && "$SUDO_USER" != "root" ]]; then
  if id -nG "$SUDO_USER" | tr ' ' '\n' | grep -qx lqg; then
    echo "user already in group lqg: $SUDO_USER"
  else
    usermod -aG lqg "$SUDO_USER"
    ADDED_USER="$SUDO_USER"
    echo "added user to group lqg: $SUDO_USER"
  fi
fi

install -o root -g root -m 0755 "$BINARY" "$PREFIX/lqg"
echo "installed binary: $PREFIX/lqg"

install -d -o root -g root -m 0755 "$CONFIG_DIR"
if [[ -f "$CONFIG_DIR/config.yaml" ]]; then
  echo "config already exists, leaving it alone: $CONFIG_DIR/config.yaml"
else
  install -o root -g lqg -m 0640 "$HERE/config.sample.yaml" "$CONFIG_DIR/config.yaml"
  echo "installed sample config: $CONFIG_DIR/config.yaml (edit this before starting lqg)"
fi

install -o root -g root -m 0644 "$HERE/lqg.service" "$UNIT_DIR/lqg.service"
echo "installed systemd unit: $UNIT_DIR/lqg.service"

systemctl daemon-reload

cat <<EOF

done. next steps:
  1. edit $CONFIG_DIR/config.yaml for your apps
  2. review the ReadWritePaths comment in $UNIT_DIR/lqg.service — add
     your apps' writable paths (storage/logs, storage/framework/*) via
     'sudo systemctl edit lqg', or the queue workers won't be able to
     write their own logs/cache under ProtectSystem=strict
  3. sudo $PREFIX/lqg validate $CONFIG_DIR/config.yaml
  4. sudo systemctl enable --now lqg
  5. lqg status   # members of the "lqg" group (or root) can reach the socket
EOF

if [[ -n "$ADDED_USER" ]]; then
  cat <<EOF

NOTE: $ADDED_USER was just added to the "lqg" group. That only applies to
new logins — log out and back in (or run: newgrp lqg) before using
lqg status / lqg watch without sudo. Other users who need access:
  sudo usermod -aG lqg <user>
EOF
fi
