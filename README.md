# lgctl

Control an LG webOS TV from Linux — wake it when your PC resumes from sleep,
switch it to the PC's HDMI input, and put it to sleep when the PC suspends
(only if the PC is the active input).

A small, single-binary, no-GUI take on the Windows-only
[LGTV Companion](https://github.com/JPersson77/LGTVCompanion), aimed at HTPCs
running Linux / SteamOS (e.g. Bazzite). Configured with one JSON file and driven
by systemd units.

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

## Install

Run the command:

```sh
curl -fsSL https://raw.githubusercontent.com/lmrisdal/lgctl/main/packaging/install.sh | sh
```

Installs it to `/usr/local/bin/lgctl`, writes an example
config to `/etc/lgctl`, and installs + enables the systemd units for sleep/wake/boot/shutdown.

Then [Configure](#configure) and [Pair](#pair) below.

<details>
<summary>Build from source, or install from a clone</summary>

The one-liner downloads a prebuilt release. To build locally instead (needs Go),
clone and pass `--build`:

```sh
git clone https://github.com/lmrisdal/lgctl
cd lgctl
sudo ./packaging/install.sh            # latest release
sudo ./packaging/install.sh --build    # build from source
```

</details>

Each tagged release ships static `amd64`/`arm64` Linux binaries.

## Configure

`install.sh` placed an example config at `/etc/lgctl/config.json` (or copy
`packaging/config.example.json` there yourself). Edit it:

| Field                  | Meaning                                                       |
| ---------------------- | ------------------------------------------------------------- |
| `ip`                   | TV's IP address (give it a DHCP reservation).                 |
| `mac`                  | TV's MAC for WOL. String, or array for multiple NICs.         |
| `ssl`                  | Use the encrypted port 3001 (default `true`); `false` = 3000. |
| `hdmi_input`           | HDMI port the PC is on (1–4).                                 |
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

## Power events (systemd)

`install.sh` sets up two units that cover all four power events:

- **`lgctl-sleep.service`** — powers the TV off just before suspend, back on at
  resume.
- **`lgctl-power.service`** — wakes the TV at boot (fire-and-forget, so an
  unreachable TV never delays boot) and powers it off at shutdown/reboot (ordered
  after the network so the TV is still reachable).

Both power-off paths are input-aware (`check_input_on_off`), so they leave the
TV alone if you're watching another source. To skip the boot power-on, remove
the `ExecStart=` line from `lgctl-power.service`.

Test it:

```sh
systemctl suspend
```

<details>
<summary>Installing the units by hand (if you're not using install.sh)</summary>

```sh
sudo install -Dm644 packaging/lgctl-sleep.service /etc/systemd/system/lgctl-sleep.service
sudo install -Dm644 packaging/lgctl-power.service /etc/systemd/system/lgctl-power.service
sudo systemctl daemon-reload
sudo systemctl enable lgctl-sleep.service lgctl-power.service
# Arm off-at-shutdown now (don't 'start' the sleep unit — that would power the
# TV off immediately).
sudo systemctl start lgctl-power.service
```

</details>

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
