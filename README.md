# lgctl

Control an LG webOS TV from Linux — wake it when your PC resumes from sleep,
switch it to the PC's HDMI input, and put it to sleep when the PC suspends
(only if the PC is the active input).

A small, single-binary, no-GUI take on the Windows-only
[LGTV Companion](https://github.com/JPersson77/LGTVCompanion), aimed at HTPCs
running Linux / SteamOS (e.g. Bazzite). Configured with one JSON file and driven
by a systemd sleep hook.

## Features

- **Wake on resume** — Wake-on-LAN magic packets plus a webOS power-on handshake.
- **Sleep on suspend, input-aware** — powers the TV off when the PC suspends,
  but only if the PC's HDMI input is the currently active source (so it won't
  turn the TV off while you're watching something else). Use `--force` to skip
  the check.
- **Switch HDMI input on wake** — optionally select the PC's input after waking.
- **Manual control** — `lgctl on|off|input N|status` for scripts and hotkeys.

No external dependencies: it's pure Go standard library (its own minimal
WebSocket client), so `go build` produces one static binary.

## Install (prebuilt binary — no Go needed)

Each tagged release ships static `amd64`/`arm64` Linux binaries, so you don't
need a Go toolchain on the target (handy on immutable Bazzite/SteamOS):

```sh
# pick amd64 (most PCs / Steam Deck) or arm64
curl -fsSL -o lgctl https://github.com/lmrisdal/lgctl/releases/latest/download/lgctl-linux-amd64
chmod +x lgctl
sudo install -Dm755 lgctl /usr/local/bin/lgctl
```

Then follow [Configure](#configure), [Pair](#pair), and
[Run on sleep/wake](#run-on-sleepwake-systemd) below.

## Build from source

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o lgctl .
```

## Configure

Copy `packaging/config.example.json` to `/etc/lgctl/config.json` and edit:

| Field                  | Meaning                                                        |
|------------------------|---------------------------------------------------------------|
| `ip`                   | TV's IP address (give it a DHCP reservation).                 |
| `mac`                  | TV's MAC for WOL. String, or array for multiple NICs.         |
| `ssl`                  | Use the encrypted port 3001 (default `true`); `false` = 3000. |
| `hdmi_input`           | HDMI port the PC is on (1–4).                                  |
| `check_input_on_off`   | Only power off if the PC is the active input.                 |
| `set_input_on_wake`    | Switch to the PC's input after waking.                        |
| `input_wake_delay_sec` | Delay before switching input (default 1).                     |
| `timeout_sec`          | How long to retry WOL + connect on power-on (default 20).     |
| `subnet`               | Mask for the directed-broadcast WOL target.                   |

The config file is searched in this order when `--config` is omitted:
`$LGCTL_CONFIG`, `/etc/lgctl/config.json`, `~/.config/lgctl/config.json`.

## Pair

The TV shows a one-time prompt the first time you connect:

```sh
sudo lgctl pair      # accept the prompt on the TV with your remote
```

The received `client_key` is written back into your config file. After that,
all commands work non-interactively.

## Run on sleep/wake (systemd)

The included unit hooks `sleep.target`: `ExecStart` runs (and completes) just
before the machine suspends, `ExecStop` runs on resume.

```sh
sudo install -Dm644 packaging/lgctl-sleep.service /etc/systemd/system/lgctl-sleep.service
sudo systemctl daemon-reload
sudo systemctl enable lgctl-sleep.service
```

Or just run `packaging/install.sh`, which builds, installs the binary to
`/usr/local/bin`, drops the example config, and enables the unit.

Test it:

```sh
systemctl suspend
```

> On immutable distros (Bazzite/SteamOS), `/usr/local/bin` (a symlink to
> `/var/usrlocal/bin`), `/etc`, and `/etc/systemd/system` are all writable, so
> this survives OS image updates.

## Manual commands

```sh
lgctl on            # wake (WOL + power on) and optionally switch input
lgctl off           # power off, but only if the PC is the active input
lgctl off --force   # power off regardless
lgctl input 2       # switch to HDMI 2
lgctl status        # show power state and active input
```

`on`/`off` also accept the aliases `resume`/`suspend`.

## Notes

- `lgctl pair` automatically enables Wake-on-LAN on the TV (best-effort, via the
  same luna workaround the Windows app uses). If it reports it couldn't, enable
  it manually in the TV's network settings.
- The TV must also have "Quick Start+" (or LAN/Wi-Fi standby) enabled for WOL to
  work from a fully-off state — set this once in the TV's General settings.
- Wire the PC to the TV over Ethernet for the most reliable WOL.
