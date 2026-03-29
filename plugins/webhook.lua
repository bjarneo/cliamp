-- webhook.lua — POST track info to a URL on every track change.
--
-- Configure the URL in config.toml:
--   [plugins.webhook]
--   url = "https://example.com/hook"

local p = plugin.register({
    name = "webhook",
    type = "hook",
    description = "POST track info to a webhook URL",
})

local url = p:config("url")

p:on("track.change", function(track)
    if not url then return end
    local body, status = cliamp.http.post(url, {
        json = {
            title  = track.title,
            artist = track.artist,
            album  = track.album,
            genre  = track.genre,
            path   = track.path,
        }
    })
    if status and status >= 400 then
        cliamp.log.warn("webhook returned " .. tostring(status))
    end
end)
