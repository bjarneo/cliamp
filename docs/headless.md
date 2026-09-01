# Headless Daemon Mode

Run cliamp without a TUI. The daemon listens on the same Unix socket as the
interactive player. Playback, library, and V2 remote commands work, but cliamp
does not render a terminal UI. Use this mode to control playback through IPC
from a status bar, script, hotkey daemon, or cron job.

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp -d                                    # short form
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp --daemon ~/Music --auto-play          # auto-play a directory
```

Send `SIGINT` or `SIGTERM` to stop the daemon. cliamp saves the resume position on a graceful shutdown.

## What works

The daemon exposes the same runtime, library, job, and event IPC interface as the TUI. See [Remote Control](remote-control.md) for the list:

- Playback: `play`, `pause`, `toggle`, `stop`, `next`, `prev`
- Position: `seek`, `volume`, `speed`
- Playback modes: `shuffle`, `repeat`, `mono`
- Library: `load "Name"`, `queue /path/to.mp3`
- Audio: `eq <preset>`, `eq --band N <dB>`, `device <name|list>`
- Status: `status`, `status --json`

## What doesn't

UI-only commands return an error in headless mode:

- `theme`: no UI is available for themes
- `vis`: no visualizer is running

The daemon still enables MPRIS on Linux, NowPlaying on macOS, and hardware media key hotkeys on Windows when the platform service is available. You can also bind media keys directly to `cliamp` subcommands. See [Hyprland](#hyprland).

## Use cases

### Background music daemon

Start cliamp once at login, for example with `~/.config/systemd/user/cliamp.service` or desktop-environment autostart. Keep it running. Control it from any terminal:

```sh
cliamp toggle      # play/pause from anywhere
cliamp next
cliamp volume -3
```

Use this minimal systemd user unit:

```ini
[Unit]
Description=cliamp headless music player

[Service]
ExecStart=%h/.local/bin/cliamp --daemon --auto-play --playlist "Lofi"
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now cliamp.service
```

### Waybar / Polybar / i3blocks status modules

Poll `cliamp status --json` at an interval. Render the fields that you need.

**Waybar** (`~/.config/waybar/config`):

```jsonc
"custom/cliamp": {
  "exec": "cliamp status --json | jq -r 'if .state == \"playing\" then \"  \" + (.track.title // \"\") else \"\" end'",
  "interval": 2,
  "on-click": "cliamp toggle",
  "on-click-right": "cliamp next",
  "on-scroll-up": "cliamp volume +3",
  "on-scroll-down": "cliamp volume -3"
}
```

**Polybar**:

```ini
[module/cliamp]
type = custom/script
exec = cliamp status --json | jq -r '.track.title // ""'
interval = 2
click-left = cliamp toggle
click-right = cliamp next
```

#### Radio stream metadata

A radio station playlist entry only has the station name. During playback,
`.track` reports the station now-playing metadata. The station sends this data
inline as SHOUTcast/Icecast ICY metadata:

| Field | Description |
|-------|-------------|
| `title` | Current song from the ICY tag. Before it arrives, this is the station name. |
| `artist` | Current artist when the ICY tag uses `"Artist - Title"` |
| `station` | Station name. Present only after a song replaces `title`. |
| `stream_title` | Raw, unsplit ICY value |

A status bar can show the station and song together:

```sh
cliamp status --json | jq -r '(.track // {}) | if .station then (if .artist then "\(.station): \(.artist) - \(.title)" else "\(.station): \(.title)" end) else (.title // "") end'
```

### Hotkeys (window manager / sxhkd / Hyprland)

Bind media keys directly to IPC subcommands.

**Hyprland** (`~/.config/hypr/hyprland.conf`):

```ini
bind = , XF86AudioPlay,  exec, cliamp toggle
bind = , XF86AudioNext,  exec, cliamp next
bind = , XF86AudioPrev,  exec, cliamp prev
bind = , XF86AudioRaiseVolume, exec, cliamp volume +3
bind = , XF86AudioLowerVolume, exec, cliamp volume -3
```

**sxhkd**:

```
XF86AudioPlay
    cliamp toggle

XF86AudioNext
    cliamp next
```

### Sleep / wake timers via cron

```cron
# Start lofi playback at 8am on weekdays
0 8 * * 1-5  /home/me/.local/bin/cliamp --daemon --auto-play --playlist Lofi >/dev/null 2>&1 &

# Stop at 6pm
0 18 * * *   pkill -TERM -f 'cliamp --daemon'
```

### Scripted playlists

Build a queue from a script:

```sh
cliamp --daemon --auto-play &
sleep 1                                  # let the socket bind
for f in $(find ~/Music/Albums/Daft\ Punk -name '*.flac' | sort); do
  cliamp queue "$f"
done
```

### Remote control over SSH

The socket is at `~/.config/cliamp/cliamp.sock`, and the CLI accesses it locally. Get a shell on the host with SSH or tmux session attach to control playback:

```sh
ssh kitchen-pi cliamp toggle
ssh kitchen-pi cliamp status --json
```

### Embedded / kiosk audio

Run this mode on a Pi or small Linux computer without a display. The daemon needs no terminal allocation. It needs working ALSA, PipeWire, or PulseAudio output.

```sh
cliamp --daemon --auto-play http://radio.cliamp.stream/lofi/stream
```

## Notes

- The daemon and TUI share one Unix socket. Only one cliamp instance can run for a user. A second instance cannot bind to the socket.
- This version of headless mode does not load Lua plugins. They need UI hooks that this mode does not enable.
- Headless mode does not preload the next track for gapless playback. Small gaps between tracks are expected.
