# Lua Plugins

cliamp has a Lua 5.1 plugin system. Plugins can hook into playback events (scrobbling, notifications, status bar output) and add custom visualizers. Each plugin runs in an isolated VM — a crash in one plugin cannot affect others or the player.

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
| `app.start` | `{}` | After all plugins loaded |
| `app.quit` | `{}` | Before shutdown |

The `status` field in `playback.state` is one of: `"playing"`, `"paused"`, `"stopped"`.

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
cliamp.player.eq_preset()     --> string
cliamp.player.visualizer()    --> string
cliamp.player.theme()         --> string
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
```

Writes are restricted to `/tmp/`, `~/.config/cliamp/`, and `~/.local/share/cliamp/`. Reads are allowed from anywhere.

### cliamp.json

```lua
local tbl = cliamp.json.decode('{"key": "value"}')
local str = cliamp.json.encode({ key = "value" })
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
cliamp.player.set_eq_preset("Rock") -- switch EQ preset by name (sets bands + UI label)
cliamp.player.set_eq_band(1, 6)   -- set EQ band 1 to +6 dB (bands 1-10, -12 to +12)
```

Without `permissions = {"control"}`, these functions log a warning and do nothing.

### cliamp.notify

```lua
cliamp.notify("Song Title")                -- notification with title only
cliamp.notify("Song Title", "Artist Name") -- notification with title and body
```

Sends a desktop notification via `notify-send`. Works with mako, dunst, and other notification daemons.

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

For security, plugins run with restricted access:

| Removed | Replacement |
|---------|-------------|
| `os.execute`, `os.remove`, `os.rename`, `os.exit` | Use `cliamp.http` or `cliamp.fs` |
| `io` module (all of it) | Use `cliamp.fs` |
| `dofile`, `loadfile` | Not available |

Safe functions kept: `os.time()`, `os.date()`, `os.clock()`, `os.getenv()`.

Each plugin runs in its own Lua VM. Plugins cannot access each other's state.

## Debugging

Check `~/.config/cliamp/plugins.log` for plugin output and errors:

```
2025-03-29 14:30:01 [now-playing] info: Now playing: Everything In Its Right Place
2025-03-29 14:30:01 [webhook] error: track.change handler error: connection refused
```

Use `cliamp.log.debug()` liberally during development.
