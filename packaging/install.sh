#!/usr/bin/env bash
# Install lgctl: binary, config, and systemd sleep hook.
#
# By default it downloads the latest prebuilt release binary (no Go toolchain
# needed). Use --build to compile from a local checkout instead.
#
#   sudo ./packaging/install.sh            # install latest release
#   sudo ./packaging/install.sh --build    # build from source (needs Go)
#
# Safe to re-run: it won't overwrite an existing config, and it never restarts
# active units (so re-running never toggles the TV off).
set -euo pipefail

REPO="lmrisdal/lgctl"
BIN="/usr/local/bin/lgctl"
CONF_DIR="/etc/lgctl"
CONF="$CONF_DIR/config.json"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd || echo "")"

MODE="release"
[[ "${1:-}" == "--build" ]] && MODE="build"

case "$(uname -m)" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

have_gh() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

# fetch_repo_file <path-in-repo> <dest> : prefer a local copy, else pull from
# GitHub (via gh for private repos, else public raw).
fetch_repo_file() {
  local path="$1" dest="$2"
  if [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/$path" ]]; then
    cp "$SCRIPT_DIR/$path" "$dest"
  elif have_gh; then
    gh api -H "Accept: application/vnd.github.raw" "repos/$REPO/contents/$path" > "$dest"
  else
    curl -fsSL -o "$dest" "https://raw.githubusercontent.com/$REPO/main/$path"
  fi
}

echo "==> Obtaining lgctl binary ($MODE, $ARCH)"
if [[ "$MODE" == "build" ]]; then
  command -v go >/dev/null 2>&1 || { echo "Go is not installed; omit --build to use a prebuilt release." >&2; exit 1; }
  [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/go.mod" ]] || { echo "--build requires running from a checkout (clone the repo first)." >&2; exit 1; }
  ( cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$TMP/lgctl" . )
else
  asset="lgctl-linux-$ARCH"
  if have_gh; then
    gh release download --repo "$REPO" --pattern "$asset" --output "$TMP/lgctl" --clobber
  else
    url="https://github.com/$REPO/releases/latest/download/$asset"
    if ! curl -fSL -o "$TMP/lgctl" "$url"; then
      echo "Download failed. If the repo is private, install GitHub CLI and run 'gh auth login'," >&2
      echo "or build from source with: sudo ./packaging/install.sh --build" >&2
      exit 1
    fi
  fi
fi
chmod +x "$TMP/lgctl"

echo "==> Installing binary to $BIN"
# Atomic replace (temp + rename) so re-running while the old binary is in use
# can't hit ETXTBSY and never leaves a half-written binary behind.
sudo install -Dm755 "$TMP/lgctl" "$BIN.new"
sudo mv -f "$BIN.new" "$BIN"

echo "==> Installing config to $CONF"
sudo mkdir -p "$CONF_DIR"
if [[ -f "$CONF" ]]; then
  echo "    $CONF already exists, leaving it untouched"
else
  fetch_repo_file "packaging/config.example.json" "$TMP/config.json"
  sudo install -Dm644 "$TMP/config.json" "$CONF"
  echo "    Edit $CONF with your TV's IP and MAC, then run: sudo lgctl pair"
fi

echo "==> Installing systemd units"
for u in lgctl-sleep.service lgctl-power.service; do
  fetch_repo_file "packaging/$u" "$TMP/$u"
  sudo install -Dm644 "$TMP/$u" "/etc/systemd/system/$u"
done
sudo systemctl daemon-reload
sudo systemctl enable lgctl-sleep.service lgctl-power.service
# Arm off-at-shutdown now: lgctl-power.service is a oneshot whose ExecStop only
# runs at shutdown if the unit was started. `start` is idempotent (no-op if
# already active) and only triggers a fire-and-forget wake; it is NOT a restart,
# so re-running never powers the TV off. Without this, off-at-shutdown would not
# take effect until the next reboot.
sudo systemctl start lgctl-power.service

cat <<'EOF'

Done. Next steps:
  1. sudo nano /etc/lgctl/config.json     # set ip + mac
  2. sudo lgctl pair                       # accept the prompt on the TV
  3. sudo lgctl status                     # verify it works
The TV will now sleep/wake with the PC. Test with: systemctl suspend

Re-run with --build to compile from source instead of downloading a release.
EOF
