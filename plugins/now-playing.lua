-- now-playing.lua — Write current track to /tmp for status bars (Waybar, Polybar, etc.)
--
-- The file at /tmp/cliamp-now-playing contains the current "Artist - Title"
-- and is updated on every track change. Cleaned up on quit.

local p = plugin.register({
    name = "now-playing",
    type = "hook",
    description = "Write now-playing to /tmp for status bars",
})

local path = p:config("path") or "/tmp/cliamp-now-playing"

p:on("track.change", function(track)
    local title = track.title or ""
    local artist = track.artist or ""
    local text = title
    if artist ~= "" then
        text = artist .. " - " .. title
    end
    cliamp.fs.write(path, text)
    cliamp.log.info("Now playing: " .. text)

    -- Desktop notification via mako/notify-send.
    if artist ~= "" then
        cliamp.notify(title, artist)
    else
        cliamp.notify(title)
    end
end)

p:on("playback.state", function(ev)
    if ev.status == "stopped" then
        cliamp.fs.write(path, "")
    end
end)

p:on("app.quit", function()
    cliamp.fs.remove(path)
end)
