# Headless Daemon Mode

Run cliamp without a TUI. The daemon listens on the same Unix socket as the interactive player, so every `cliamp <subcommand>` keeps working — but nothing renders to the terminal. This is useful when you want a music player you only ever talk to over IPC: from a status bar, a script, a hotkey daemon, or a cron job.

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp -d                                    # short form
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp --daemon ~/Music --auto-play          # auto-play a directory
```

Send `SIGINT` or `SIGTERM` to stop. Resume position is saved on graceful shutdown.

## What works

The daemon exposes the same IPC surface as the TUI. See [Remote Control](remote-control.md) for the full list:

- Playback: `play`, `pause`, `toggle`, `stop`, `next`, `prev`
- Position: `seek`, `volume`, `speed`
- Playback modes: `shuffle`, `repeat`, `mono`
- Library: `load "Name"`, `queue /path/to.mp3`
- Audio: `eq <preset>`, `eq --band N <dB>`, `device <name|list>`
- Status: `status`, `status --json`

## What doesn't

UI-only commands return an error in headless mode:

- `theme` — no UI to apply a theme to
- `vis` — no visualizer running

There is also no MPRIS / macOS NowPlaying bridge in this mode. Wire your media keys to `cliamp` subcommands directly (see [Hyprland](#hyprland) below).

## Use cases

### Background music daemon

Start cliamp once at login (e.g. via `~/.config/systemd/user/cliamp.service` or your DE's autostart) and leave it running. Control it from any terminal:

```sh
cliamp toggle      # play/pause from anywhere
cliamp next
cliamp volume -3
```

A minimal systemd user unit:

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

Poll `cliamp status --json` on an interval, render whatever fields you want.

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

A radio station's playlist entry only knows the station name. While a stream is
playing, `.track` instead reports the now-playing metadata the station itself
broadcasts inline — the ICY metadata of the SHOUTcast/Icecast protocol:

| Field | Description |
|-------|-------------|
| `title` | Current song, from the ICY tag (the station name before any arrives) |
| `artist` | Current artist, when the ICY tag uses `"Artist - Title"` |
| `station` | The station's own name, present only once a song replaces `title` |
| `stream_title` | The raw, unsplit ICY value |

So a status bar can show the station and the song together:

```sh
cliamp status --json | jq -r '(.track // {}) | if .station then (if .artist then "\(.station): \(.artist) - \(.title)" else "\(.station): \(.title)" end) else (.title // "") end'
```

### Hotkeys (window manager / sxhkd / Hyprland)

Bind your media keys directly to IPC subcommands.

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

Since the socket lives at `~/.config/cliamp/cliamp.sock` and the CLI talks to it locally, anything that gets you a shell on the host (SSH, tmux session attach) lets you control playback:

```sh
ssh kitchen-pi cliamp toggle
ssh kitchen-pi cliamp status --json
```

### Embedded / kiosk audio

Run on a Pi or small Linux box that has no display. The daemon needs no terminal allocation, just a working ALSA/PipeWire/PulseAudio output.

```sh
cliamp --daemon --auto-play http://radio.cliamp.stream/lofi/stream
```

## Notes

- The daemon and TUI share the same Unix socket, so only one cliamp instance can run at a time per user. Starting a second instance refuses to bind.
- Lua plugins are not loaded in this version of headless mode. They depend on UI hooks that aren't wired up here.
- Auto-advance has no gapless preloading in headless mode — small inter-track gaps are expected.
