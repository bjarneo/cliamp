-- auto-eq.lua — Automatically switch EQ preset based on track genre.

local p = plugin.register({
    name        = "auto-eq",
    type        = "hook",
    version     = "1.0.0",
    description = "Switch EQ based on track genre",
    permissions = {"control"},
})

-- Map genres to EQ presets. Keys are lowercase for case-insensitive matching.
local genre_map = {
    rock          = "Rock",
    ["alt-rock"]  = "Rock",
    ["hard rock"] = "Rock",
    metal         = "Rock",
    punk          = "Rock",
    grunge        = "Rock",
    pop           = "Pop",
    ["synth-pop"] = "Pop",
    jazz          = "Jazz",
    ["smooth jazz"] = "Jazz",
    bossa         = "Jazz",
    classical     = "Classical",
    orchestra     = "Classical",
    electronic    = "Electronic",
    edm           = "Electronic",
    techno        = "Electronic",
    house         = "Electronic",
    trance        = "Electronic",
    ambient       = "Electronic",
    ["hip-hop"]   = "Hip-Hop",
    ["hip hop"]   = "Hip-Hop",
    rap           = "Hip-Hop",
    trap          = "Hip-Hop",
    ["r&b"]       = "R&B",
    rnb           = "R&B",
    soul          = "R&B",
    folk          = "Acoustic",
    acoustic      = "Acoustic",
    ["singer-songwriter"] = "Acoustic",
    country       = "Acoustic",
    podcast       = "Podcast",
    speech        = "Podcast",
    lofi          = "Late Night",
    ["lo-fi"]     = "Late Night",
    chill         = "Late Night",
}

local last_preset = nil

p:on("track.change", function(track)
    local genre = (track.genre or ""):lower()
    local preset = genre_map[genre]

    if not preset then
        -- Try partial match for compound genres like "Indie Rock".
        for key, val in pairs(genre_map) do
            if genre:find(key, 1, true) then
                preset = val
                break
            end
        end
    end

    preset = preset or "Flat"

    if preset ~= last_preset then
        cliamp.player.set_eq_preset(preset)
        last_preset = preset
        cliamp.log.info("EQ -> " .. preset .. " (genre: " .. (track.genre or "unknown") .. ")")
    end
end)
