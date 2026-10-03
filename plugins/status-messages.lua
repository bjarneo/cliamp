-- status-messages.lua — Demo of cliamp.message(): surface playback events
-- as transient messages in the status bar at the bottom of the UI.
--
-- Install by copying (or symlinking) this file to ~/.config/cliamp/plugins/
-- and restart cliamp.

local p = plugin.register({
    name = "status-messages",
    type = "hook",
    description = "Show playback events in the status bar",
})

p:on("app.start", function()
    cliamp.message("cliamp ready", 2)
end)

p:on("track.change", function(track)
    local text = track.title or ""
    if track.artist and track.artist ~= "" then
        text = track.artist .. " — " .. text
    end
    cliamp.message("Now playing: " .. text, 3)
end)

-- playback.state fires once per second while a track plays, not just on
-- state transitions. Track the last status locally so the status bar is
-- only updated when it actually changes.
local last_status = nil
p:on("playback.state", function(ev)
    if ev.status == last_status then
        return
    end
    last_status = ev.status
    if ev.status == "paused" then
        cliamp.message("Paused", 1.5)
    elseif ev.status == "stopped" then
        cliamp.message("Stopped", 1.5)
    end
end)

p:on("track.scrobble", function()
    cliamp.message("Scrobble sent", 2)
end)
