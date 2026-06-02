# Lua Plugins

cliamp has a Lua 5.1 plugin system. Plugins can hook into playback events (scrobbling, notifications, status bar output) and add custom visualizers. Each plugin runs in an isolated VM. A crash in one plugin cannot affect others or the player.

Plugins live in `~/.config/cliamp/plugins/`. Create the directory:

```
mkdir -p ~/.config/cliamp/plugins
```

Drop a `.lua` file in and restart cliamp. That's it.

## Plugin manager

```sh
cliamp plugins                          # show help
cliamp plugins list                     # list installed plugins
cliamp plugins install <source>         # install a plugin
cliamp plugins remove <name>            # remove a plugin
```

### Install sources

| Format | Example |
|--------|---------|
| GitHub | `user/repo` |
| GitHub with tag | `user/repo@v1.0` |
| GitLab | `gitlab:user/repo` |
| GitLab with tag | `gitlab:user/repo@v1.0` |
| Codeberg | `codeberg:user/repo` |
| Codeberg with tag | `codeberg:user/repo@v1.0` |
| Direct URL | `https://example.com/plugin.lua` |

### Naming convention

Plugin repositories **must** be named `cliamp-plugin-<name>` with the entry point `<name>.lua` at the repo root. The `cliamp-plugin-` prefix is stripped on install, so `cliamp-plugin-soap-bubbles` (containing `soap-bubbles.lua`) installs as `soap-bubbles`.

```sh
cliamp plugins install bjarneo/cliamp-plugin-lastfm
cliamp plugins install bjarneo/cliamp-plugin-lastfm@v1.0
cliamp plugins install gitlab:user/my-visualizer
cliamp plugins install codeberg:user/my-plugin
cliamp plugins install https://example.com/my-plugin.lua
cliamp plugins remove lastfm
```

## Quick start

### Now-playing file (for Waybar, Polybar, etc.)

```lua
-- ~/.config/cliamp/plugins/now-playing.lua
local p = plugin.register({
    name = "now-playing",
    type = "hook",
    description = "Write now-playing to /tmp for status bars",
})

p:on("track.change", function(track)
    cliamp.fs.write("/tmp/cliamp-now-playing", track.artist .. " - " .. track.title)
end)

p:on("playback.state", function(ev)
    if ev.status == "paused" then
        cliamp.fs.write("/tmp/cliamp-now-playing", "paused")
    end
end)

p:on("app.quit", function()
    cliamp.fs.remove("/tmp/cliamp-now-playing")
end)
```

### Desktop notification on track change

```lua
-- ~/.config/cliamp/plugins/notify.lua
local p = plugin.register({
    name = "notify",
    type = "hook",
})

p:on("track.change", function(track)
    local title = track.artist .. " - " .. track.title
    os.execute('notify-send "cliamp" "' .. title .. '"')
end)
```

Note: `os.execute` is removed by the sandbox. For shell commands, use `cliamp.http.post` to a local webhook, or write to a file that a watcher picks up.

### Webhook

```lua
-- ~/.config/cliamp/plugins/webhook.lua
local p = plugin.register({
    name = "webhook",
    type = "hook",
})

local url = p:config("url")

p:on("track.change", function(track)
    if not url then return end
    cliamp.http.post(url, {
        json = { title = track.title, artist = track.artist, album = track.album }
    })
end)
```

```toml
# config.toml
[plugins.webhook]
url = "https://example.com/hook"
```

## Plugin structure

### Single file

```
~/.config/cliamp/plugins/myplugin.lua
```

### Directory with init.lua

```
~/.config/cliamp/plugins/myplugin/
    init.lua
    helpers.lua
```

The directory name becomes the plugin name. Only `init.lua` is loaded automatically.

## Registration

Every plugin must call `plugin.register()` to be recognized. Files that don't call it are silently skipped.

```lua
local p = plugin.register({
    name        = "myplugin",           -- required
    type        = "hook",               -- "hook" or "visualizer"
    version     = "1.0.0",             -- optional
    description = "What it does",       -- optional
})
```

The returned object `p` provides two methods:

| Method | Description |
|--------|-------------|
| `p:on(event, callback)` | Subscribe to a playback event |
| `p:config(key)` | Read a config value from `[plugins.myplugin]` in config.toml |

## Events

Plugins subscribe to events with `p:on(event, callback)`. Callbacks run asynchronously in goroutines and have a 5-second timeout.

### Available events

| Event | Callback argument | When |
|-------|-------------------|------|
| `track.change` | `{title, artist, album, genre, year, path, duration, stream}` | New track starts |
| `track.scrobble` | Same + `{played_secs}` | Track played >= 50% or >= 4 min |
| `playback.state` | `{status, title, artist, album, path, duration, stream, position}` | Any playback state change (play, pause, stop, seek, volume, track transition) |
| `player.seek` | `{position, duration}` (seconds) | A seek completes |
| `player.volume` | `{db}` | Volume changes |
| `player.eq` | `{bands, preset}` | An EQ band or preset changes |
| `player.mode` | `{shuffle, repeat}` | Shuffle toggled or repeat mode cycled |
| `queue.change` | `{count, index, queued}` | Playlist or play-next queue changes |
| `app.start` | `{}` | After all plugins loaded |
| `app.quit` | `{}` | Before shutdown |

The `status` field in `playback.state` is one of: `"playing"`, `"paused"`, `"stopped"`. The `repeat` field in `player.mode` is one of: `"Off"`, `"All"`, `"One"` (matching `cliamp.player.repeat_mode()`). In `player.eq`, `bands` is an array of 10 dB values.

The `player.*` and `queue.change` events are fired by diffing state after each UI update, so they cover every change regardless of source (keypress, IPC, MPRIS, or another plugin).

## Plugin object methods

The object returned by `plugin.register(...)` exposes additional methods beyond `:on()` / `:config()`:

### `p:bind(key, [description,] callback)` — keyboard binding (requires `permissions = {"keymap"}`)

```lua
local p = plugin.register({
    name = "my-plugin",
    type = "hook",
    permissions = {"keymap"},
})

-- Listed in the Ctrl+K overlay under "— plugins —":
p:bind("x", "Extract chapters", function(key) ... end)

-- Not listed (hidden binding):
p:bind("ctrl+e", function(key) ... end)
```

Returns `true` on success, or `false, reason` if the key is already owned by cliamp's core UI or the plugin lacks the `keymap` permission. Pass a description string as the middle argument to surface the binding in the `Ctrl+K` keymap overlay; omit it for an internal-only binding.

Key strings are in Bubbletea's `msg.String()` form: lowercase letters, `ctrl+` / `shift+` / `alt+` prefixes (e.g. `"x"`, `"ctrl+e"`, `"shift+f1"`). Case-insensitive.

Plugin keys only fire in the main view — overlays like the file browser, theme picker, and keymap itself capture their own input. Core reserves all keys documented in `docs/keybindings.md`; trying to bind one of those logs a warning and returns `false`.

Use `p:unbind(key)` to release a binding.

### `p:command(name, callback)` — shell-invokable command

```lua
p:command("run", function(args)
    -- args is an array of strings passed after the command name
    return "done: " .. args[1]
end)
```

The callback can return a string, which is printed by the CLI client. Commands are invoked from the shell via `cliamp plugins call <plugin-name> <command> [args...]` and dispatched to the running cliamp over IPC. Since dispatch runs in the running player, commands don't need a separate permission (they're user-initiated).

List all registered commands with `cliamp plugins commands`. Commands can run for up to 5 minutes before timing out.

## Lua API

All APIs are under the `cliamp` global table.

### cliamp.player (read-only)

```lua
cliamp.player.state()         --> "playing" | "paused" | "stopped"
cliamp.player.position()      --> number (seconds)
cliamp.player.duration()      --> number (seconds)
cliamp.player.volume()        --> number (dB, -30 to +6)
cliamp.player.speed()         --> number (ratio, 1.0 = normal)
cliamp.player.mono()          --> boolean
cliamp.player.repeat_mode()   --> "Off" | "All" | "One"
cliamp.player.shuffle()       --> boolean
cliamp.player.eq_bands()      --> table of 10 dB values
```

### cliamp.track (read-only)

```lua
cliamp.track.title()          --> string
cliamp.track.artist()         --> string
cliamp.track.album()          --> string
cliamp.track.genre()          --> string
cliamp.track.year()           --> number
cliamp.track.track_number()   --> number
cliamp.track.path()           --> string
cliamp.track.is_stream()      --> boolean
cliamp.track.duration_secs()  --> number
```

### cliamp.queue

Read the playlist freely; mutating it requires `permissions = {"control"}`. All
indices are 0-based, matching `cliamp.queue.current()`.

```lua
-- read (no permission)
cliamp.queue.list()        --> array of {title, artist, album, path, index, queued}
cliamp.queue.count()       --> number of tracks
cliamp.queue.current()     --> 0-based index of the current track

-- mutate (requires "control")
cliamp.queue.add(path)         -- resolve a file/dir/URL and append
cliamp.queue.jump(index)       -- make index current and play it
cliamp.queue.remove(index)     -- remove the track at index
cliamp.queue.move(from, to)    -- reorder a track
```

`add` accepts anything the CLI accepts: a local file or directory, an HTTP
stream, an M3U/PLS URL, or a YouTube/yt-dlp URL. Resolution happens off the UI
thread, so a slow URL never blocks playback.

### cliamp.http

```lua
-- GET
local body, status = cliamp.http.get("https://api.example.com/data", {
    headers = { Authorization = "Bearer token" }
})

-- POST with JSON
local body, status = cliamp.http.post("https://api.example.com/scrobble", {
    json = { artist = "Radiohead", track = "Everything In Its Right Place" }
})

-- POST with form body
local body, status = cliamp.http.post(url, {
    headers = { ["Content-Type"] = "application/x-www-form-urlencoded" },
    body = "key=value&foo=bar"
})
```

Restrictions: 5-second timeout, 1 MB response body cap.

### cliamp.fs

```lua
cliamp.fs.write(path, content)    -- overwrite file
cliamp.fs.append(path, content)   -- append to file
cliamp.fs.read(path)              --> string (max 1 MB)
cliamp.fs.remove(path)            -- delete file
cliamp.fs.exists(path)            --> boolean
cliamp.fs.mkdir(path)             -- create directory (recursive)
cliamp.fs.listdir(path)           --> {names}, err
```

Writes are restricted to the system temp directory (`/tmp/` on Unix), `~/.config/cliamp/`, `~/.local/share/cliamp/`, and `~/Music/cliamp/`. Reads are allowed from anywhere. On Windows, if `HOME` is unset, the config directory portion resolves to `%APPDATA%\cliamp`.

### cliamp.json

```lua
local tbl = cliamp.json.decode('{"key": "value"}')
local str = cliamp.json.encode({ key = "value" })
```

### cliamp.store

A persistent per-plugin key/value store. Values (strings, numbers, booleans,
and tables) survive restarts. No permission required: each plugin sees only its
own namespace, so one plugin can never read another's keys.

```lua
cliamp.store.set(key, value)   -- value: string|number|boolean|table
cliamp.store.get(key)          --> value or nil
cliamp.store.delete(key)
cliamp.store.keys()            --> sorted array of keys
cliamp.store.clear()
```

Backed by `~/.local/share/cliamp/plugins/<name>/store.json`, written owner-only
(0600). Use it for play counts, offline scrobble queues, resume positions, or
remembered settings, not for large data.

```lua
local counts = cliamp.store.get("counts") or {}
counts[cliamp.track.path()] = (counts[cliamp.track.path()] or 0) + 1
cliamp.store.set("counts", counts)
```

### cliamp.crypto

```lua
cliamp.crypto.md5("hello")                  --> hex string
cliamp.crypto.sha256("hello")               --> hex string
cliamp.crypto.hmac_sha256("secret", "msg")  --> hex string
```

### cliamp.log

```lua
cliamp.log.info("loaded successfully")
cliamp.log.warn("missing config key")
cliamp.log.error("request failed: " .. err)
cliamp.log.debug("response: " .. body)
```

Logs are written to `~/.config/cliamp/plugins.log` with timestamps and `[plugin-name]` prefix.

### cliamp.player control (requires permissions)

Plugins that declare `permissions = {"control"}` can send commands to the player:

```lua
local p = plugin.register({
    name = "my-controller",
    type = "hook",
    permissions = {"control"},
})

cliamp.player.next()              -- skip to next track
cliamp.player.prev()              -- go to previous track
cliamp.player.play_pause()        -- toggle play/pause
cliamp.player.stop()              -- stop playback
cliamp.player.set_volume(-5)      -- set volume in dB (-30 to +6)
cliamp.player.set_speed(1.25)     -- set playback speed (0.25 to 2.0)
cliamp.player.seek(30)            -- seek to 30 seconds
cliamp.player.toggle_mono()       -- toggle mono output
cliamp.player.set_eq_preset("Rock") -- switch to built-in preset (sets bands + UI label)
cliamp.player.set_eq_preset("Metal", {6,4,1,-1,-2,2,4,6,6,5}) -- custom preset with bands
cliamp.player.set_eq_band(1, 6)   -- set EQ band 1 to +6 dB (bands 1-10, -12 to +12)
```

Without `permissions = {"control"}`, these functions log a warning and do nothing.

### cliamp.notify

```lua
cliamp.notify("Song Title")                -- notification with title only
cliamp.notify("Song Title", "Artist Name") -- notification with title and body
```

Sends a desktop notification via `notify-send`. Works with mako, dunst, and other notification daemons.

### cliamp.exec (requires permissions)

Plugins that declare `permissions = {"exec"}` can spawn subprocesses from a configurable binary allowlist. Default allowlist: `yt-dlp`, `ffmpeg`. Extend it in `config.toml`:

```toml
[plugins]
allowed_binaries = "ffprobe, curl"   # merged with defaults
```

```lua
local p = plugin.register({
    name = "my-downloader",
    type = "hook",
    permissions = {"exec"},
})

local handle, err = cliamp.exec.run("yt-dlp", {"--dump-json", url}, {
    on_stdout = function(line) ... end,   -- optional, called per line
    on_stderr = function(line) ... end,   -- optional
    on_exit   = function(code) ... end,   -- optional, fires exactly once
    cwd       = "/tmp/work",              -- optional; must be in write allowlist
    timeout   = 300,                       -- optional seconds, hard cap 1800
})

handle:cancel()                           -- terminate the process
handle:alive()                            -- --> boolean
```

**Safety rails:**

- Binary must be in the allowlist. Argv is argv — no shell, no expansion.
- `args` must be a flat array of strings. Nested tables / non-strings are rejected.
- Subprocess env is minimal (`PATH`, `HOME`, `LANG`) — secrets in the parent env are not passed through.
- Output is capped at 4 MiB per process (stdout + stderr combined); further lines are dropped silently.
- Concurrency capped at 4 running processes per plugin.
- All processes owned by a plugin are killed on plugin unload and on cliamp exit.
- Negative `on_exit` codes signal cancellation/timeout (`-1`) or spawn failure (`-2`).

Without `permissions = {"exec"}`, `cliamp.exec.run` returns `nil, "exec permission required"`.

### cliamp.message

```lua
cliamp.message("Scrobble Sent")        -- show for default duration
cliamp.message("Syncing Library", 5)   -- show for 5 seconds
```

Displays a transient message in the status bar at the bottom of the UI. The
duration argument is optional (seconds); omit it to use the default TTL. Durations above 60 seconds are clamped.

### cliamp.sleep

```lua
cliamp.sleep(2.5)  -- block for 2.5 seconds (max 10)
```

Blocks the plugin's Lua VM. Other hooks for the same plugin will queue until the sleep finishes. Prefer `cliamp.timer.after()` for non-blocking delays.

### cliamp.timer

```lua
-- Run once after 5 seconds
local id = cliamp.timer.after(5.0, function()
    cliamp.log.info("timer fired")
end)

-- Run every 30 seconds
local id = cliamp.timer.every(30.0, function()
    -- periodic task
end)

-- Cancel
cliamp.timer.cancel(id)
```

## Configuration

Plugin-specific config goes in `config.toml` under `[plugins.<name>]`:

```toml
[plugins.lastfm]
api_key = "abc123"
api_secret = "secret"
session_key = "sk-xxx"

[plugins.webhook]
url = "https://example.com/hook"
```

Access in Lua:

```lua
local api_key = p:config("api_key")   --> "abc123" or nil
```

### Disabling plugins

Disable a specific plugin:

```toml
[plugins.webhook]
enabled = false
```

Or disable multiple at once:

```toml
[plugins]
disabled = webhook, discord-rpc
```

## Visualizer plugins

Plugins with `type = "visualizer"` add custom visualizer modes that appear in the `v` key cycle alongside built-in modes.

```lua
-- ~/.config/cliamp/plugins/simple-bars.lua
local p = plugin.register({
    name = "simple-bars",
    type = "visualizer",
})

-- Called every frame (~20 FPS during playback).
-- bands: table of 10 numbers (0.0-1.0), indices 1-10
-- frame: monotonic counter
-- rows: available terminal rows (changes in fullscreen mode)
-- cols: available terminal columns
-- Must return a multi-line string.
function p:render(bands, frame, rows, cols)
    local lines = {}
    local chars = { " ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█" }

    for row = 5, 1, -1 do
        local line = ""
        for i = 1, 10 do
            local level = bands[i]
            local threshold = (row - 1) / 5
            if level > threshold then
                line = line .. "██████ "
            else
                line = line .. "       "
            end
        end
        table.insert(lines, line)
    end

    return table.concat(lines, "\n")
end
```

### Visualizer callbacks

| Callback | Signature | Required |
|----------|-----------|----------|
| `p:render(bands, frame, rows, cols)` | Returns string | Yes |
| `p:init(rows, cols)` | Setup when selected | No |
| `p:destroy()` | Cleanup when deselected | No |

Render has a 10 ms budget per frame. If it exceeds this, the previous frame is reused to prevent UI jank.

## Sandbox

For security, plugins run with restricted access. The sandbox removes dangerous standard library functions and restricts file system access.

### Removed functions

| Removed | Replacement |
|---------|-------------|
| `os.execute`, `os.remove`, `os.rename`, `os.exit`, `os.setlocale`, `os.tmpname` | Use `cliamp.fs`, `cliamp.http`, or permission-gated `cliamp.exec` |
| `io` module (all of it) | Use `cliamp.fs` |
| `dofile`, `loadfile` | Not available |

### Kept functions

`os.time()`, `os.date()`, `os.clock()`, `os.getenv()` are available.

### File system restrictions

**Reads:** Allowed from any path (max 1 MB per read).

**Writes/removes/mkdir** are restricted to these directories only:

- `/tmp/` (and the system temp directory)
- `~/.config/cliamp/`
- `~/.local/share/cliamp/`
- `~/Music/cliamp/`

Attempts to write outside these directories will raise a Lua error. Directory traversal (`..`) is blocked.

### Isolation

- Each plugin runs in its own Lua VM. Plugins cannot access each other's state or variables.
- A crash in one plugin does not affect other plugins or the player.
- Network access is available via `cliamp.http` (no raw socket access).
- There is no process spawning — `os.execute` is removed. For shell commands, write to a file that a watcher picks up, or use `cliamp.http.post` to a local webhook.

## Debugging

Check `~/.config/cliamp/plugins.log` for plugin output and errors:

```
2025-03-29 14:30:01 [now-playing] info: Now playing: Everything In Its Right Place
2025-03-29 14:30:01 [webhook] error: track.change handler error: connection refused
```

Use `cliamp.log.debug()` liberally during development.
