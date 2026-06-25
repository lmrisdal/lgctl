#!/usr/bin/env bash
# Install lgctl: binary, config, and (optionally) the systemd power hooks.
#
# By default it downloads the latest prebuilt release binary (no Go toolchain
# needed) and asks which systemd services to install. When there's no terminal
# to prompt at (e.g. piped in CI), it installs both services.
#
#   curl -fsSL .../install.sh | sh         # install latest release (interactive)
#   sudo ./packaging/install.sh --build    # build from source (needs Go)
#
# Options:
#   --build        build the binary from a local checkout instead of downloading
#   --sleep        install the sleep/resume service without asking
#   --no-sleep     skip the sleep/resume service
#   --power        install the boot/shutdown service without asking
#   --no-power     skip the boot/shutdown service
#   -y, --yes      accept defaults for all prompts (installs both services)
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
ASSUME_YES=0
WANT_SLEEP="ask"   # ask | yes | no
WANT_POWER="ask"   # ask | yes | no

usage() {
  cat <<'USAGE'
Install lgctl: binary, config, and (optionally) the systemd power hooks.

Options:
  --build        build the binary from a local checkout instead of downloading
  --sleep        install the sleep/resume service without asking
  --no-sleep     skip the sleep/resume service
  --power        install the boot/shutdown service without asking
  --no-power     skip the boot/shutdown service
  -y, --yes      accept defaults for all prompts (installs both services)
  -h, --help     show this help
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --build)              MODE="build" ;;
    --sleep)              WANT_SLEEP="yes" ;;
    --no-sleep)           WANT_SLEEP="no" ;;
    --power|--shutdown)   WANT_POWER="yes" ;;
    --no-power|--no-shutdown) WANT_POWER="no" ;;
    -y|--yes)             ASSUME_YES=1 ;;
    -h|--help)            usage; exit 0 ;;
    *) echo "Unknown option: $1 (try --help)" >&2; exit 1 ;;
  esac
  shift
done

# ask_yn <question> : echo "yes"/"no". Reads from the controlling terminal so it
# works under `curl | sh` (where stdin is the script). Defaults to yes when -y is
# given or no terminal is available.
ask_yn() {
  local q="$1" ans
  if [[ "$ASSUME_YES" -eq 1 ]]; then echo "yes"; return; fi
  # Open the controlling terminal on fd 3; the group keeps the open error (when
  # there is no tty) from leaking to stderr without permanently redirecting it.
  if ! { exec 3<>/dev/tty; } 2>/dev/null; then echo "yes"; return; fi
  printf '%s [Y/n] ' "$q" >&3
  IFS= read -r ans <&3 || ans=""
  exec 3>&-
  case "$ans" in [nN]*) echo "no" ;; *) echo "yes" ;; esac
}

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

[[ "$WANT_SLEEP" == "ask" ]] && WANT_SLEEP="$(ask_yn "Install the sleep service (TV off on suspend, on at resume)?")"
[[ "$WANT_POWER" == "ask" ]] && WANT_POWER="$(ask_yn "Install the shutdown service (TV on at boot, off at shutdown)?")"

units=()
[[ "$WANT_SLEEP" == "yes" ]] && units+=("lgctl-sleep.service")
[[ "$WANT_POWER" == "yes" ]] && units+=("lgctl-power.service")

if [[ ${#units[@]} -eq 0 ]]; then
  echo "==> No systemd services selected; installed binary + config only"
else
  echo "==> Installing systemd units: ${units[*]}"
  for u in "${units[@]}"; do
    fetch_repo_file "packaging/$u" "$TMP/$u"
    sudo install -Dm644 "$TMP/$u" "/etc/systemd/system/$u"
  done
  sudo systemctl daemon-reload
  sudo systemctl enable "${units[@]}"
  # Arm off-at-shutdown now: lgctl-power.service is a oneshot whose ExecStop only
  # runs at shutdown if the unit was started. `start` is idempotent (no-op if
  # already active) and only triggers a fire-and-forget wake; it is NOT a restart,
  # so re-running never powers the TV off. Without this, off-at-shutdown would not
  # take effect until the next reboot.
  if [[ "$WANT_POWER" == "yes" ]]; then
    sudo systemctl start lgctl-power.service
  fi
fi

echo
echo "Done. Next steps:"
echo "  1. sudo nano /etc/lgctl/config.json     # set ip + mac"
echo "  2. sudo lgctl pair                       # accept the prompt on the TV"
echo "  3. sudo lgctl status                     # verify it works"
[[ "$WANT_SLEEP" == "yes" ]] && echo "The TV will sleep/wake with the PC. Test with: systemctl suspend"
echo
echo "Re-run with --help to see options (e.g. --no-power, --build)."
