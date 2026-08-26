# Lua Plugins

cliamp uses Lua 5.1 plugins. Plugins can handle playback events, such as scrobbling, notifications, and status-bar output. They can also add visualizers. Each plugin runs in an isolated VM. A plugin crash does not affect other plugins or the player.

Store plugins in `~/.config/cliamp/plugins/`. Create the directory:

```
mkdir -p ~/.config/cliamp/plugins
```

cliamp runs a plugin only after you approve its exact contents. Existing and manually copied plugins start as untrusted. Approve one with `cliamp plugins trust <name>`.

## Plugin manager

```sh
cliamp plugins                          # show help
cliamp plugins list                     # list installed plugins
cliamp plugins install <source>         # install a plugin
cliamp plugins trust <name>             # approve installed plugin contents
cliamp plugins remove <name>            # remove a plugin
```

The install and trust commands show the source, SHA-256, declared permissions, and implicit file-system and network access before the prompt. In a non-interactive environment, use `--yes` only after you review the same content independently. cliamp stores approvals in `plugins/.trust.json`. Editing a plugin changes its hash and disables it until you approve it again. cliamp rejects unknown permission names.

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

Name plugin repositories `cliamp-plugin-<name>`. Put the `<name>.lua` entry point in the repository root. cliamp removes the `cliamp-plugin-` prefix during installation. For example, `cliamp-plugin-soap-bubbles`, which contains `soap-bubbles.lua`, installs as `soap-bubbles`.

```sh
cliamp plugins install bjarneo/cliamp-plugin-lastfm
cliamp plugins install bjarneo/cliamp-plugin-lastfm@v1.0
cliamp plugins install gitlab:user/my-visualizer
cliamp plugins install codeberg:user/my-plugin
cliamp plugins install https://example.com/my-plugin.lua
cliamp plugins remove lastfm
```

## Quick start

### Now-playing file for Waybar, Polybar, and similar bars

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

`os.execute` is not available in the sandbox. Use `cliamp.http` for public HTTP endpoints. cliamp blocks private, loopback, link-local, multicast, and unspecified addresses. For local automation, write to an allowed file for a watcher to read. You can also declare the permission-gated `exec` capability.

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

The directory name is the plugin name. cliamp loads only `init.lua` automatically.

## Registration

Each plugin must call `plugin.register()`. cliamp silently skips files that do not call it.

```lua
local p = plugin.register({
    name        = "myplugin",           -- required
    type        = "hook",               -- "hook" or "visualizer"
    version     = "1.0.0",             -- optional
    description = "What it does",       -- optional
})
```

The returned `p` object provides these methods:

| Method | Description |
|--------|-------------|
| `p:on(event, callback)` | Subscribe to a playback event |
| `p:config(key)` | Read a config value from `[plugins.myplugin]` in config.toml |
| `p:publish(topic, payload, options)` | Publish a namespaced event to local IPC subscribers |

## Plugin event pub/sub

Plugins can publish JSON-compatible values to external programs that connect to
Cliamp's owner-only IPC socket. cliamp puts each topic below the installed plugin
name. For example, `myplugin.lua` that publishes `"playback"` uses the topic
`plugin.myplugin.playback`. The name provides one topic segment. cliamp replaces
each character other than a letter, digit, `_`, or `-` with `_`. Therefore,
`my.plugin.lua` uses the `plugin.my_plugin.*` prefix. Publishing `"playback"`
from that plugin uses `plugin.my_plugin.playback`. It cannot collide with topics
from a plugin named `my`.

This conversion loses information, so namespaces are unique for a session. If
`my.plugin.lua` and `my_plugin.lua` are both installed, the first loaded plugin
(plugins load in name order) owns the `plugin.my_plugin.*` prefix. In the other
plugin, `p:publish()` returns `nil, err` and names the owner. Rename one plugin
to publish from both. Subscribers must specify complete topics, not prefixes.

```lua
p:publish("playback", {
    status = cliamp.player.state(),
    title = cliamp.track.title(),
}, { retain = true })
```

With `retain = true`, Cliamp keeps the latest value in memory and sends it to
new subscribers immediately. Cliamp discards retained values when it exits. It
does not save event data to disk. Publishing does not block. cliamp disconnects
a subscriber that cannot keep up instead of blocking the player.

Open `cliamp.sock` and send one NDJSON request to subscribe:

```json
{"version":2,"id":"events","method":"subscribe","topics":["plugin.myplugin.playback"]}
```

After `{"version":2,"id":"events","ok":true}`, the connection becomes a
server-to-client event stream:

```json
{"event":"plugin.myplugin.playback","seq":42,"time":1786685741,"retained":true,"data":{"status":"playing","title":"Track"}}
```

Subscriptions use exact topic matches and accept at most 32 topics. They are
for streaming only. Use another IPC connection for normal commands. Sending
more bytes on a subscription closes it. Payloads can be at most 64 KiB. cliamp
converts them with the nesting and cycle rules of
[`cliamp.json`](#cliampjson). Topic segments can contain letters, digits, `.`,
`_`, and `-`. `p:publish()` needs no permission. IPC is local to the same user,
and the `status` command already exposes playback metadata.

## Events

Use `p:on(event, callback)` to subscribe to events. Callbacks run in goroutines and time out after 5 seconds.

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

In `playback.state`, `status` is `"playing"`, `"paused"`, or `"stopped"`. In `player.mode`, `repeat` is `"Off"`, `"All"`, or `"One"`, matching `cliamp.player.repeat_mode()`. In `player.eq`, `bands` is an array of 10 dB values.

cliamp sends `player.*` and `queue.change` events by comparing state after each UI update. They cover every source, including a keypress, IPC, MPRIS, or another plugin.

## Plugin object methods

The object from `plugin.register(...)` provides these methods in addition to `:on()` and `:config()`:

### `p:bind(key, [description,] callback)` - keyboard binding (requires `permissions = {"keymap"}`)

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

Returns `true` on success. Returns `false, reason` when cliamp's core UI owns the key or the plugin lacks the `keymap` permission. Pass a description as the middle argument to show the binding in the `Ctrl+K` keymap overlay. Omit it for an internal-only binding.

Use Bubbletea's `msg.String()` form for key strings: lowercase letters and the `ctrl+`, `shift+`, or `alt+` prefixes. For example: `"x"`, `"ctrl+e"`, and `"shift+f1"`. Key strings are case-insensitive.

Plugin keys work only in the main view. Overlays such as the file browser, theme picker, and keymap capture their own input. The core reserves every key in `docs/keybindings.md`. Trying to bind one logs a warning and returns `false`.

Use `p:unbind(key)` to release a binding.

### `p:command(name, callback)` - shell-invokable command

```lua
p:command("run", function(args)
    -- args is an array of strings passed after the command name
    return "done: " .. args[1]
end)
```

The callback can return a string. The CLI client prints it. Invoke commands from the shell with `cliamp plugins call <plugin-name> <command> [args...]`. cliamp sends the command to the running player over IPC. Commands need no separate permission because the user starts them.

List registered commands with `cliamp plugins commands`. A command can run for up to 5 minutes before it times out.

## Lua API

All APIs are in the global `cliamp` table.

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

You can read the playlist without permission. To change it, declare `permissions = {"control"}`. All indices are 0-based, as in `cliamp.queue.current()`.

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

`add` accepts every input that the CLI accepts: a local file or directory, an
HTTP stream, an M3U/PLS URL, or a YouTube/yt-dlp URL. cliamp resolves it off the
UI thread. A slow URL does not block playback.

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

The timeout is 5 seconds. The response body limit is 1 MB.

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

You can write only to the system temp directory (`/tmp/` on Unix), `~/.config/cliamp/`, `~/.local/share/cliamp/`, and `~/Music/cliamp/`. You can read from any path. On Windows, when `HOME` is unset, the config directory resolves to `%APPDATA%\cliamp`.

### cliamp.json

```lua
local tbl = cliamp.json.decode('{"key": "value"}')
local str = cliamp.json.encode({ key = "value" })
```

cliamp encodes tables to a depth of 64 levels. A deeper table or a cyclic
reference becomes `null`; it does not fail. `p:publish()` and `cliamp.store`
use the same conversion.

### cliamp.store

This is a persistent key/value store for each plugin. Strings, numbers,
booleans, and tables survive restarts. No permission is required. Each plugin
can access only its own namespace, so it cannot read another plugin's keys.

```lua
cliamp.store.set(key, value)   -- value: string|number|boolean|table
cliamp.store.get(key)          --> value or nil
cliamp.store.delete(key)
cliamp.store.keys()            --> sorted array of keys
cliamp.store.clear()
```

cliamp stores this data in `~/.local/share/cliamp/plugins/<name>/store.json`
with owner-only mode (0600). Use it for play counts, offline scrobble queues,
resume positions, and saved settings. Do not use it for large data.

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

cliamp writes logs to `~/.config/cliamp/plugins.log`. Each line has a timestamp and the `[plugin-name]` prefix.

### cliamp.player control (requires permissions)

Plugins that declare `permissions = {"control"}` can control the player:

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

If a plugin does not declare `permissions = {"control"}`, these functions log a warning and do nothing.

### cliamp.notify

```lua
cliamp.notify("Song Title")                -- notification with title only
cliamp.notify("Song Title", "Artist Name") -- notification with title and body
```

This sends a desktop notification through `notify-send`. It works with mako, dunst, and other notification daemons.

### cliamp.exec (requires permissions)

Plugins that declare `permissions = {"exec"}` can start subprocesses from a configurable binary allowlist. The default allowlist is `yt-dlp`, `ffmpeg`. Add binaries in `config.toml`:

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

**Safety rules:**

- The binary must be in the allowlist. Argv is argv. No shell or expansion is used.
- `args` must be a flat array of strings. cliamp rejects nested tables and non-strings.
- The subprocess environment contains only `PATH`, `HOME`, and `LANG`. cliamp does not pass parent-environment secrets.
- Output is limited to 4 MiB per process, for stdout and stderr together. cliamp silently drops later lines.
- Each plugin can run up to 4 processes at one time.
- cliamp kills every plugin-owned process when the plugin unloads and when cliamp exits.
- Negative `on_exit` codes indicate cancellation or timeout (`-1`), or a start failure (`-2`).

Without `permissions = {"exec"}`, `cliamp.exec.run` returns `nil, "exec permission required"`.

### cliamp.message

```lua
cliamp.message("Scrobble Sent")        -- show for default duration
cliamp.message("Syncing Library", 5)   -- show for 5 seconds
```

This shows a temporary message in the status bar at the bottom of the UI. The
duration is optional and uses seconds. Omit it to use the default TTL. cliamp
limits durations above 60 seconds.

### cliamp.sleep

```lua
cliamp.sleep(2.5)  -- block for 2.5 seconds (max 10)
```

This blocks the plugin's Lua VM. Other hooks for the same plugin wait until the sleep ends. Use `cliamp.timer.after()` for a non-blocking delay.

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

Put plugin-specific configuration in `config.toml` under `[plugins.<name>]`:

```toml
[plugins.lastfm]
api_key = "abc123"
api_secret = "secret"
session_key = "sk-xxx"

[plugins.webhook]
url = "https://example.com/hook"
```

Read it in Lua:

```lua
local api_key = p:config("api_key")   --> "abc123" or nil
```

### Disabling plugins

Disable one plugin:

```toml
[plugins.webhook]
enabled = false
```

To disable several plugins:

```toml
[plugins]
disabled = webhook, discord-rpc
```

## Visualizer plugins

Plugins with `type = "visualizer"` add visualizer modes to the `v` key cycle with the built-in modes.

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

`render` has a 10 ms limit for each frame. If it exceeds the limit, cliamp reuses the previous frame to prevent UI delay.

## Sandbox

For security, plugins have restricted access. The sandbox removes unsafe standard-library functions and limits file-system access.

### Removed functions

| Removed | Replacement |
|---------|-------------|
| `os.execute`, `os.remove`, `os.rename`, `os.exit`, `os.setlocale`, `os.tmpname` | Use `cliamp.fs`, `cliamp.http`, or permission-gated `cliamp.exec` |
| `io` module (all of it) | Use `cliamp.fs` |
| `dofile`, `loadfile`, `load`, `loadstring`, `require`, `module`, `package`, `debug` | Not available |

### Kept functions

You can use `os.time()`, `os.date()`, `os.clock()`, and `os.getenv()`.

### File system restrictions

**Reads:** You can read from any path, up to 1 MB for each read.

**Writes/removes/mkdir** work only in these directories:

- `/tmp/` (and the system temp directory)
- `~/.config/cliamp/`
- `~/.local/share/cliamp/`
- `~/Music/cliamp/`

Writing outside these directories raises a Lua error. cliamp blocks directory traversal (`..`).

### Isolation

- Each plugin has its own Lua VM. A plugin cannot access another plugin's state or variables.
- A plugin crash does not affect other plugins or the player.
- Use `cliamp.http` for public network access. Raw socket access is not available. cliamp blocks private, loopback, link-local, multicast, and unspecified destinations after DNS resolution and through redirects.
- `os.execute` is not available. Permission-gated `cliamp.exec` can start only configured allowed binaries.

## Debugging

Check `~/.config/cliamp/plugins.log` for plugin output and errors:

```
2025-03-29 14:30:01 [now-playing] info: Now playing: Everything In Its Right Place
2025-03-29 14:30:01 [webhook] error: track.change handler error: connection refused
```

Use `cliamp.log.debug()` during development.
