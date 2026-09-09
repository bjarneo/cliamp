# Detached Mode

Run cliamp without a terminal of its own. `--daemon` starts the whole player --
the same playback, providers, plugins, keybindings, and visualizer as the
interactive TUI -- and renders it into a virtual terminal instead of yours.
Playback survives every terminal, IPC serves scripts and status bars, and
`cliamp attach` lends a real terminal to the running player whenever you want
to browse or search.

```sh
cliamp --daemon                              # detached, IPC only until you attach
cliamp -d                                    # short form
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp --daemon ~/Music --auto-play          # auto-play a directory

cliamp attach                                # borrow this terminal to the session
```

Press `ctrl+\` in an attached terminal to detach: the session keeps playing.
Every other key belongs to the player, so `q` still quits cliamp -- session and
playback included.

Send `SIGINT` or `SIGTERM` to stop the session. cliamp saves the resume position on a graceful shutdown.

## What works

Everything the interactive player does, because it is the interactive player.
Detached, that includes playback, gapless preload, the playlist and provider
browsers, saved playlists, Lua plugins, MPRIS on Linux, NowPlaying on macOS,
hardware media keys on Windows, and the full runtime, library, job, and event
IPC interface. See [Remote Control](remote-control.md) for the command list:

- Playback: `play`, `pause`, `toggle`, `stop`, `next`, `prev`
- Position: `seek`, `volume`, `speed`
- Playback modes: `shuffle`, `repeat`, `mono`
- Library: `load "Name"`, `queue /path/to.mp3`
- Audio: `eq <preset>`, `eq --band N <dB>`, `device <name|list>`
- Appearance: `theme <name>`, `vis <mode>`
- Status: `status`, `status --json`, `vis --stream`

`cliamp vis --stream` analyzes the spectrum on demand while detached, so a
status bar gets live bands without a terminal attached.

## Attaching

`cliamp attach` needs a terminal on stdin and stdout, and takes the size from
it -- resize the window and the session follows.

- **One terminal at a time.** One player has one renderer, so attaching from a
  second terminal takes the session over and the first is told why.
- **Color depth is fixed for the session.** A session outlives its clients, so
  it renders truecolor and each client downsamples for its own terminal.
- **The client owns its terminal.** It enters the alternate screen on attach and
  restores the screen, cursor, and title on detach, so a session that dies
  never leaves your terminal in a strange state.
- **Attach only works against `--daemon`.** A cliamp that owns a real terminal
  has no virtual one to lend and reports that.

Over SSH, attach the session running on the other host:

```sh
ssh -t kitchen-pi cliamp attach
```

## Use cases

### Background music session

Start cliamp once at login, for example with `~/.config/systemd/user/cliamp.service` or desktop-environment autostart. Keep it running. Control it from any terminal, or attach to it:

```sh
cliamp toggle      # play/pause from anywhere
cliamp next
cliamp volume -3
cliamp attach      # full UI in this terminal, ctrl+\ to leave it playing
```

Use this minimal systemd user unit:

```ini
[Unit]
Description=cliamp music player session

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

The socket is at `~/.config/cliamp/cliamp.sock`, and the CLI accesses it locally. Get a shell on the host with SSH to control playback, or attach to its UI:

```sh
ssh kitchen-pi cliamp toggle
ssh kitchen-pi cliamp status --json
ssh -t kitchen-pi cliamp attach
```

### Embedded / kiosk audio

Run this mode on a Pi or small Linux computer without a display. A detached session needs no terminal allocation. It needs working ALSA, PipeWire, or PulseAudio output.

```sh
cliamp --daemon --auto-play http://radio.cliamp.stream/lofi/stream
```

## Notes

- One cliamp instance runs per user: it owns the Unix socket. Start it detached
  and attach to it rather than starting a second one.
- A detached session renders no frame and runs no visualizer until a client
  attaches. It ticks only as often as playback bookkeeping needs, so an
  idle-but-playing session costs about what the old headless mode did.
