-- autoplay.lua — Keep playing related YouTube tracks when the queue runs out.
--
-- When the last track in play order starts (repeat off) and it is a YouTube
-- or YouTube Music video, fetch its auto-generated Mix (list=RD<id>) with
-- yt-dlp, drop entries already in the queue, and append the next few. Each
-- appended track re-seeds the next batch when it becomes the last one. If the
-- queue ends before a batch lands, playback resumes when it does.
--
-- Config:
--   [plugins.autoplay]
--   enabled_on_start = true    # default false; the toggle key persists overrides
--   count            = 5       # tracks appended per refill
--   key              = "ctrl+a" # main-view toggle key (c is reserved by the core)
--
--   cliamp plugins call autoplay status
--   cliamp plugins call autoplay toggle

local p = plugin.register({
    name        = "autoplay",
    type        = "hook",
    version     = "0.3.0",
    description = "Continue with related YouTube tracks when the queue ends",
    permissions = {"control", "exec", "keymap"},
})

local FETCH_ITEMS    = 20 -- Mix entries requested; headroom for dedupe
local REFILL_TIMEOUT = 20 -- seconds to wait for queue.add resolutions (about 2 s each)

local count = math.floor(tonumber(p:config("count")) or 5)
if count < 1 then count = 5 end
local key     = p:config("key") or "ctrl+a"
local enabled = cliamp.store.get("enabled")
if enabled == nil then
    enabled = p:config("enabled_on_start") == "true" -- config values arrive as strings
end

-- A refill is one yt-dlp fetch plus the queue.add resolutions it issues.
-- gen invalidates a fetch started for a track the user has moved away from.
local gen          = 0
local current_path = nil   -- path of the track that is (or was last) playing
local fetch        = nil   -- {gen, handle} while yt-dlp runs
local refill       = nil   -- {gen, base, expected} while adds resolve
local resume       = false -- queue ended: call next() once a track follows again
local failed_seed  = nil   -- seed whose Mix added nothing; blocks refetch loops
local continue_after_end   -- defined below; used by the refill timeout

-- video_id extracts the YouTube video ID from a track path, or nil.
local function video_id(path)
    if not path then return nil end
    local host = path:match("^https?://([^/]+)")
    if not host then return nil end
    host = host:lower():gsub("^www%.", ""):gsub("^m%.", "")
    if host == "youtu.be" then
        return path:match("^https?://[^/]+/([%w_%-]+)")
    end
    if host == "youtube.com" or host == "music.youtube.com" then
        return path:match("[?&]v=([%w_%-]+)")
    end
    return nil
end

local function busy()
    return fetch ~= nil or refill ~= nil
end

-- invalidate cancels an in-flight fetch and any pending resume. A refill
-- whose adds are already issued cannot be recalled; queue.change closes it.
local function invalidate()
    gen = gen + 1
    if fetch and fetch.handle then fetch.handle:cancel() end
    fetch = nil
    resume = false
end

-- eligible_seed returns the current track path when a Mix can be fetched
-- for it: autoplay is on, nothing is in flight, it is a YouTube video that
-- has not failed before, and it is not a live stream.
local function eligible_seed()
    if not enabled or busy() or not current_path then return nil end
    if current_path == failed_seed or not video_id(current_path) then return nil end
    if cliamp.track.is_live() then return nil end
    return current_path
end

local function on_mix(g, seed, ids)
    if fetch and fetch.gen == g then fetch = nil end
    if g ~= gen or not enabled then return end -- user moved on or turned autoplay off
    local seen = {}
    for _, t in ipairs(cliamp.queue.list()) do
        seen[video_id(t.path) or t.path] = true
    end
    local base, added = cliamp.queue.count(), 0
    for _, id in ipairs(ids) do
        if added >= count then break end
        if not seen[id] then
            seen[id] = true
            cliamp.queue.add("https://www.youtube.com/watch?v=" .. id)
            added = added + 1
        end
    end
    cliamp.log.debug(string.format("mix for %s: %d entries, %d added", seed, #ids, added))
    if added == 0 then
        failed_seed = seed
        cliamp.message("Autoplay: no new related tracks")
        return
    end
    -- Each add resolves asynchronously; queue.change tracks their arrival.
    -- If none arrive in time, latch the seed so the same Mix is not refetched.
    -- If the queue ended meanwhile, continue from the track that finished.
    refill = { gen = g, base = base, expected = base + added }
    cliamp.timer.after(REFILL_TIMEOUT, function()
        if not refill or refill.gen ~= g then return end
        if cliamp.queue.count() <= refill.base then failed_seed = seed end
        refill = nil
        if resume then continue_after_end() end
    end)
    cliamp.message(string.format("Autoplay: adding %d related tracks", added))
end

-- fetch_mix runs yt-dlp on the Mix seeded from the given track.
local function fetch_mix(seed)
    local id = video_id(seed)
    local mix = "https://www.youtube.com/watch?v=" .. id .. "&list=RD" .. id
    local g, ids = gen, {}
    fetch = { gen = g }
    cliamp.log.debug("fetching mix for " .. seed)
    local handle, err = cliamp.exec.run("yt-dlp", {
        "--flat-playlist", "-j", "--no-warnings",
        "--playlist-end", tostring(FETCH_ITEMS),
        "--socket-timeout", "15",
        mix,
    }, {
        timeout = 30,
        on_stdout = function(line)
            local entry = cliamp.json.decode(line)
            -- Skip the seed itself and anything live right now: a live
            -- stream never ends, so it would stall the queue for good.
            if entry and entry.id and entry.id ~= id and entry.live_status ~= "is_live" then
                ids[#ids + 1] = entry.id
            end
        end,
        on_exit = function(code)
            if code ~= 0 and #ids == 0 then
                if fetch and fetch.gen == g then fetch = nil end
                cliamp.log.debug(string.format("yt-dlp exited %d with no mix entries for %s", code, seed))
                if g == gen then
                    failed_seed = seed
                    cliamp.message("Autoplay: yt-dlp failed (" .. tostring(code) .. ")")
                end
                return
            end
            on_mix(g, seed, ids)
        end,
    })
    if err then
        fetch = nil
        cliamp.log.warn("autoplay: " .. err)
        cliamp.message("Autoplay: " .. err)
    elseif fetch and fetch.gen == g then
        fetch.handle = handle
    end
end

-- continue_after_end fetches related tracks for the track that just finished
-- and arms a resume for when they land. It never arms one on its own for a
-- track it cannot continue from; an existing arm is left alone, because adds
-- already issued may still land.
continue_after_end = function()
    local seed = eligible_seed()
    if not seed then return end
    resume = true
    fetch_mix(seed)
end

-- prefill fetches while the last track is still playing so the related
-- tracks are queued before it ends and the transition stays gapless.
local function prefill()
    local seed = eligible_seed()
    if not seed then return end
    if cliamp.player.repeat_mode():lower() ~= "off" then return end
    if cliamp.queue.has_next() then return end
    fetch_mix(seed)
end

p:on("track.change", function(track)
    if track.path == current_path then return end
    invalidate()
    current_path = track.path
    prefill()
end)

-- Playback is running, by the user's hand or on its own, so a pending resume
-- is moot. Re-check eligibility too: whether a yt-dlp track is a live stream
-- is only known once its stream is up, after track.change has fired.
p:on("playback.state", function(ev)
    if ev.status ~= "playing" then return end
    resume = false
    prefill()
end)

-- A track follows again after the queue ended: resume. Otherwise re-check
-- after the user removes upcoming tracks, clears play-next, or switches
-- repeat off, and close a refill once its tracks have arrived.
p:on("queue.change", function(ev)
    if resume and cliamp.queue.has_next() then
        resume = false
        cliamp.player.next()
    end
    if refill and ev.count >= refill.expected then refill = nil end
    prefill()
end)

p:on("player.mode", function() prefill() end)

-- The user stopped playback on purpose: drop the fetch and any planned
-- resume. Tracks already being added still land, but nothing starts them.
p:on("playback.stop", function()
    invalidate()
    refill = nil
end)

-- Playback ran past the last track. Resume when a pending refill lands,
-- otherwise fetch now, seeded by the track that just finished.
p:on("queue.end", function(track)
    if not enabled then return end
    current_path = track.path
    cliamp.log.debug("queue ended after " .. tostring(track.path) .. (busy() and ", refill pending" or ""))
    if busy() then
        resume = true
        return
    end
    continue_after_end()
end)

local function toggle()
    enabled = not enabled
    cliamp.store.set("enabled", enabled)
    if enabled then
        failed_seed = nil
        prefill()
    else
        invalidate()
        refill = nil
    end
    cliamp.message("Autoplay " .. (enabled and "on" or "off"))
    return enabled
end

local ok, reason = p:bind(key, "Toggle autoplay (YouTube Mix)", toggle)
if not ok then
    cliamp.log.warn("could not bind " .. key .. ": " .. tostring(reason))
end

p:command("toggle", function()
    return "autoplay " .. (toggle() and "on" or "off")
end)

p:command("status", function()
    local state = "idle"
    if fetch then state = "fetching" elseif refill then state = "adding" end
    return string.format("autoplay %s (%s), %d per refill, key %s",
        enabled and "on" or "off", state, count, key)
end)
