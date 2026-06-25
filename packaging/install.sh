#!/usr/bin/env bash
# Build lgctl, install the binary, config, and systemd sleep hook.
# Safe to re-run; it won't overwrite an existing config.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN=/usr/local/bin/lgctl
CONF_DIR=/etc/lgctl
CONF="$CONF_DIR/config.json"
UNIT=/etc/systemd/system/lgctl-sleep.service

echo "==> Building lgctl"
( cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o lgctl . )

echo "==> Installing binary to $BIN"
sudo install -Dm755 "$REPO_DIR/lgctl" "$BIN"

echo "==> Installing config to $CONF"
sudo mkdir -p "$CONF_DIR"
if [[ -f "$CONF" ]]; then
  echo "    $CONF already exists, leaving it untouched"
else
  sudo install -Dm644 "$REPO_DIR/packaging/config.example.json" "$CONF"
  echo "    Edit $CONF with your TV's IP and MAC, then run: sudo lgctl pair"
fi

echo "==> Installing systemd unit to $UNIT"
sudo install -Dm644 "$REPO_DIR/packaging/lgctl-sleep.service" "$UNIT"
sudo systemctl daemon-reload
sudo systemctl enable lgctl-sleep.service

cat <<'EOF'

Done. Next steps:
  1. sudo nano /etc/lgctl/config.json     # set ip + mac
  2. sudo lgctl pair                       # accept the prompt on the TV
  3. sudo lgctl status                     # verify it works
The TV will now sleep/wake with the PC. Test with: systemctl suspend
EOF
