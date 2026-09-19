-- pomodoro.lua — Focus sessions that pause the music on breaks, with a big
-- countdown that replaces the visualizer.
--
-- Press H to start or stop a session, ( and ) to take time off or add time to
-- the running phase. Music plays through the work phase and
-- pauses for breaks, so the silence marks the break rather than a timer you
-- have to watch. Press v (or Ctrl+V) to switch the visualizer to "pomodoro"
-- for a full-width countdown and nothing else.
--
-- Also scriptable:  cliamp plugins call pomodoro start
--                   cliamp plugins call pomodoro stop
--                   cliamp plugins call pomodoro status
--
-- Durations are configurable in config.toml:
--
--   [plugins.pomodoro]
--   work_minutes = 25
--   break_minutes = 5
--   long_break_minutes = 15
--   rounds_before_long_break = 4
--   adjust_minutes = 1           -- how much ( and ) change the running phase

local p = plugin.register({
    name        = "pomodoro",
    type        = "visualizer",
    version     = "1.0.0",
    description = "Focus timer that pauses playback on breaks",
    permissions = {"control", "keymap"},
})

local function cfg(key, fallback)
    local raw = p:config(key)
    if raw == nil or raw == "" then return fallback end
    local v = tonumber(raw)
    if not v or v <= 0 then
        cliamp.log.warn(string.format("pomodoro: %s = %q is not a positive number; using %g",
            key, tostring(raw), fallback))
        return fallback
    end
    return v
end

local WORK = cfg("work_minutes", 25)
local BREAK = cfg("break_minutes", 5)
local LONG_BREAK = cfg("long_break_minutes", 15)
local ROUNDS = math.floor(cfg("rounds_before_long_break", 4))
local ADJUST = cfg("adjust_minutes", 1)

local timer = nil
local phase = nil       -- "work" | "break", nil when stopped
local round = 0         -- completed work phases in this session
local paused_by_us = false
local phase_ends_at = nil
local phase_total = nil   -- length of the current phase, for the progress line

local function total_completed()
    return tonumber(cliamp.store.get("completed")) or 0
end

local function stop_timer()
    if timer then
        cliamp.timer.cancel(timer)
        timer = nil
    end
end

-- resume_if_we_paused only restarts playback we ourselves paused. If the
-- listener paused during a break, leave it alone.
local function resume_if_we_paused()
    if paused_by_us and cliamp.player.state() == "paused" then
        cliamp.player.play_pause()
    end
    paused_by_us = false
end

local start_work, start_break

local function finish_work()
    round = round + 1
    cliamp.store.set("completed", total_completed() + 1)
    start_break()
end

-- phase_done is what runs when the current phase's countdown reaches zero.
local function phase_done()
    if phase == "work" then finish_work() else start_work() end
end

start_work = function()
    phase = "work"
    phase_total = WORK * 60
    phase_ends_at = os.time() + phase_total
    resume_if_we_paused()

    cliamp.message(string.format("Focus %g min (round %d)", WORK, round + 1))
    cliamp.notify("Pomodoro", string.format("Focus for %g minutes", WORK))

    stop_timer()
    timer = cliamp.timer.after(WORK * 60, finish_work)
end

start_break = function()
    phase = "break"
    local long = ROUNDS > 0 and round % ROUNDS == 0
    local minutes = long and LONG_BREAK or BREAK
    phase_total = minutes * 60
    phase_ends_at = os.time() + phase_total

    if cliamp.player.state() == "playing" then
        cliamp.player.play_pause()
        paused_by_us = true
    end

    local label = long and "Long break" or "Break"
    cliamp.message(string.format("%s %g min", label, minutes))
    cliamp.notify("Pomodoro", string.format("%s — %g minutes", label, minutes))

    stop_timer()
    timer = cliamp.timer.after(minutes * 60, phase_done)
end

local function stop_session(quiet)
    stop_timer()
    resume_if_we_paused()
    phase, phase_ends_at, phase_total, round = nil, nil, nil, 0
    if not quiet then
        cliamp.message("Pomodoro stopped")
    end
end

local function seconds_left()
    if not phase_ends_at then return nil end
    local left = phase_ends_at - os.time()
    if left < 0 then left = 0 end
    return left
end

local function status_text()
    if not phase then
        return string.format("Pomodoro is off (%d completed all time)", total_completed())
    end
    local left = seconds_left() or 0
    return string.format("%s: %dm %ds left (round %d, %d all time)",
        phase == "work" and "Focus" or "Break",
        math.floor(left / 60), left % 60, round + 1, total_completed())
end

-- ---------------------------------------------------------------- visualizer
--
-- A clock face: digits rasterized from curves at twice the terminal's vertical
-- resolution, over a thin progress line.
--
-- Two things make this smooth rather than blocky. The digits are strokes and
-- arcs rather than a bitmap font, so they are drawn at whatever size the panel
-- gives them instead of being a small grid scaled up. And each character cell
-- carries two pixels, using ▀ ▄ █, which doubles the vertical resolution.
--
-- Rasterizing costs real work, so finished digits are cached per size: the
-- clock only changes once a second, and usually only one or two digits with it.

-- cellAspect is the terminal's cell height ÷ width, used to keep the digits
-- from looking stretched. A typical cell is 2; a narrow font or extra line
-- spacing pushes it higher.
--
--   [plugins.pomodoro]
--   cell_aspect = 3.0
local cellAspect = tonumber(p:config("cell_aspect")) or 2.0
if cellAspect < 1 or cellAspect > 5 then cellAspect = 2.0 end

-- A half-block pixel is one cell wide and half a cell tall.
local pixelAspect = cellAspect / 2

-- Anti-aliasing shades edge pixels towards the background, which assumes a
-- dark terminal. Turn it off for plain on/off pixels.
local antialias = p:config("antialias") ~= "false"

local sin, cos, floor, sqrt = math.sin, math.cos, math.floor, math.sqrt

-- arc samples an ellipse arc into a polyline. Angles are radians with y down,
-- so -pi/2 is up and pi/2 is down.
local function arc(cx, cy, rx, ry, a0, a1, steps)
    steps = steps or 26
    local pts = {}
    for i = 0, steps do
        local a = a0 + (a1 - a0) * i / steps
        pts[#pts + 1] = {cx + rx * cos(a), cy + ry * sin(a)}
    end
    return pts
end

local function line(x0, y0, x1, y1)
    return {{x0, y0}, {x1, y1}}
end

local TAU = math.pi * 2
local PI = math.pi

-- Digits as stroke paths in a normalized box, x and y both 0..1, y downwards.
-- These are centre lines; thickness is applied when rasterizing.
local GLYPHS = {
    ["0"] = {arc(0.50, 0.50, 0.30, 0.40, 0, TAU, 22)},
    ["1"] = {line(0.30, 0.26, 0.52, 0.08), line(0.52, 0.08, 0.52, 0.92),
             line(0.26, 0.92, 0.78, 0.92)},
    ["2"] = {arc(0.50, 0.32, 0.30, 0.22, PI, TAU + 0.45, 24),
             line(0.77, 0.40, 0.18, 0.92), line(0.18, 0.92, 0.84, 0.92)},
    ["3"] = {arc(0.48, 0.30, 0.28, 0.21, -2.4, 1.0, 24),
             arc(0.48, 0.70, 0.30, 0.22, -1.0, 2.4, 16)},
    ["4"] = {line(0.66, 0.08, 0.18, 0.62), line(0.18, 0.62, 0.86, 0.62),
             line(0.66, 0.08, 0.66, 0.92)},
    -- The bowl starts where the vertical ends, or the join shows as a notch.
    ["5"] = {line(0.78, 0.10, 0.28, 0.10), line(0.28, 0.10, 0.26, 0.48),
             arc(0.50, 0.66, 0.30, 0.26, -2.5, 2.0, 18)},
    ["6"] = {arc(0.52, 0.42, 0.30, 0.34, -0.9, PI, 26),
             arc(0.50, 0.68, 0.28, 0.24, 0, TAU, 20)},
    ["7"] = {line(0.18, 0.10, 0.84, 0.10), line(0.84, 0.10, 0.38, 0.92)},
    ["8"] = {arc(0.50, 0.30, 0.26, 0.20, 0, TAU, 32),
             arc(0.50, 0.71, 0.30, 0.21, 0, TAU, 20)},
    ["9"] = {arc(0.50, 0.32, 0.28, 0.22, 0, TAU, 32),
             arc(0.48, 0.55, 0.30, 0.37, -0.6, 1.9, 16)},
    ["-"] = {line(0.20, 0.50, 0.80, 0.50)},
    [":"] = {line(0.50, 0.33, 0.50, 0.36), line(0.50, 0.66, 0.50, 0.69)},
}

-- Relative advance width per glyph. The colon needs far less room than a digit.
local ADVANCE = {[":"] = 0.42}

-- distToSegment measures in display units. Both coordinates are normalized
-- 0..1, but the glyph box is w cells wide and h*pixelAspect cells tall, so y
-- has to be scaled by their ratio or a horizontal stroke comes out a different
-- thickness from a vertical one.
local function distToSegment(px, py, x0, y0, x1, y1, yscale)
    local dx, dy = (x1 - x0), (y1 - y0) * yscale
    local wx, wy = (px - x0), (py - y0) * yscale
    local len2 = dx * dx + dy * dy
    local t = 0
    if len2 > 0 then
        t = (wx * dx + wy * dy) / len2
        if t < 0 then t = 0 elseif t > 1 then t = 1 end
    end
    local ex, ey = wx - t * dx, wy - t * dy
    return sqrt(ex * ex + ey * ey)
end

local glyphCache = {}

-- rasterGlyph renders one character into a w x h grid of coverage values in
-- 0..1. Coverage rather than on/off is what removes the staircase edges: a
-- pixel the stroke only half covers is drawn at half brightness, and the eye
-- reads the blend as a smooth curve.
local function rasterGlyph(ch, w, h, thick)
    local yscale = (h * pixelAspect) / w
    local key = ch .. ":" .. w .. "x" .. h .. ":" .. thick
    local hit = glyphCache[key]
    if hit then return hit end

    local paths = GLYPHS[ch] or GLYPHS["-"]
    local half = thick / 2
    -- Feather over well under a pixel. A wider blend reads as blur and leaves
    -- a grey halo around every stroke; this keeps the transition tight.
    local feather = 0.55 / w
    local inner, outer = half - feather * 0.5, half + feather * 0.5

    -- Path bounds let most pixels skip a path entirely; without this every
    -- pixel would measure every segment.
    local bounds = {}
    for pi, path in ipairs(paths) do
        local x0, y0, x1, y1 = 1e9, 1e9, -1e9, -1e9
        for _, pt in ipairs(path) do
            if pt[1] < x0 then x0 = pt[1] end
            if pt[1] > x1 then x1 = pt[1] end
            if pt[2] < y0 then y0 = pt[2] end
            if pt[2] > y1 then y1 = pt[2] end
        end
        local mx = outer + 1 / w
        local my = (outer + 1 / w) / yscale
        bounds[pi] = {x0 - mx, y0 - my, x1 + mx, y1 + my}
    end

    local grid = {}
    for y = 1, h do
        local row = {}
        local py = (y - 0.5) / h
        for x = 1, w do
            local px = (x - 0.5) / w
            local best = 1e9
            for pi, path in ipairs(paths) do
                local b = bounds[pi]
                if px >= b[1] and px <= b[3] and py >= b[2] and py <= b[4] then
                    for i = 1, #path - 1 do
                        local a, c = path[i], path[i + 1]
                        local d = distToSegment(px, py, a[1], a[2], c[1], c[2], yscale)
                        if d < best then
                            best = d
                            if best <= inner then break end
                        end
                    end
                end
                if best <= inner then break end
            end

            local cov
            if best <= inner then
                cov = 1
            elseif best >= outer then
                cov = 0
            else
                cov = (outer - best) / (outer - inner)
                -- Push partial coverage towards solid or empty. A linear ramp
                -- spreads mid greys along the whole edge, which is what looks
                -- like a shadow; this keeps only the pixels genuinely astride
                -- the boundary grey.
                cov = cov * cov * (3 - 2 * cov)
            end
            row[x] = cov
        end
        grid[y] = row
    end

    glyphCache[key] = grid
    return grid
end

-- packHalfBlocks turns the coverage canvas into terminal rows. Each cell holds
-- two pixels: the upper is the foreground of ▀, the lower its background, and
-- each is shaded by its own coverage. Colour codes are emitted only when they
-- change, so a mostly-flat row stays cheap.
--
-- The shading blends towards black, which suits a dark terminal. Set
-- antialias = false in config for plain on/off pixels instead.
local function packHalfBlocks(canvas, w, h)
    local rows = {}
    for y = 1, h, 2 do
        local top, bottom = canvas[y] or {}, canvas[y + 1] or {}
        local parts, n = {}, 0
        local curFG, curBG = nil, nil   -- curBG == "d" means terminal default

        local function shade(c)
            local v = math.floor(c * 255 + 0.5)
            return "\27[38;2;" .. v .. ";" .. v .. ";" .. v .. "m", v
        end

        for x = 1, w do
            local ct, cb = top[x] or 0, bottom[x] or 0
            if not antialias then
                ct = ct >= 0.5 and 1 or 0
                cb = cb >= 0.5 and 1 or 0
            else
                -- Anything this faint is invisible but would still paint a
                -- cell, so drop it rather than leave a smudge.
                if ct < 0.06 then ct = 0 end
                if cb < 0.06 then cb = 0 end
            end

            if ct == 0 and cb == 0 then
                if curFG or curBG then
                    n = n + 1; parts[n] = "\27[0m"
                    curFG, curBG = nil, nil
                end
                n = n + 1; parts[n] = " "
            else
                -- Only paint a background when the lower half actually has
                -- ink. Painting black behind a half-empty cell is what leaves
                -- a dark halo on a theme whose background is not pure black.
                local glyph, fgCov
                if cb == 0 then
                    glyph, fgCov = "▀", ct
                elseif ct == 0 then
                    glyph, fgCov = "▄", cb
                else
                    glyph, fgCov = "▀", ct
                end

                if ct > 0 and cb > 0 then
                    local bgv = math.floor(cb * 255 + 0.5)
                    if curBG ~= bgv then
                        n = n + 1
                        parts[n] = "\27[48;2;" .. bgv .. ";" .. bgv .. ";" .. bgv .. "m"
                        curBG = bgv
                    end
                elseif curBG ~= "d" then
                    n = n + 1; parts[n] = "\27[49m"
                    curBG = "d"
                end

                local code, fgv = shade(fgCov)
                if curFG ~= fgv then
                    n = n + 1; parts[n] = code
                    curFG = fgv
                end
                n = n + 1; parts[n] = glyph
            end
        end
        if curFG or curBG then n = n + 1; parts[n] = "\27[0m" end
        rows[#rows + 1] = table.concat(parts)
    end
    return rows
end

local function padTo(line, lineCells, width)
    local pad = floor((width - lineCells) / 2)
    if pad < 0 then pad = 0 end
    return string.rep(" ", pad) .. line
end

local function progressLine(fraction, cells, width)
    if cells < 4 then return "" end
    local filled = floor(fraction * cells + 0.5)
    if filled < 0 then filled = 0 end
    if filled > cells then filled = cells end
    return padTo(string.rep("━", filled) ..
        "\27[2m" .. string.rep("─", cells - filled) .. "\27[22m", cells, width)
end

-- faceWidth returns the exact cell width renderFace will produce for text at
-- digitW, including its per-glyph and per-gap minimums.
local function faceWidth(text, digitW)
    local gap = math.max(1, floor(digitW * 0.14))
    local total = 0
    for i = 1, #text do
        local ch = text:sub(i, i)
        total = total + math.max(2, floor(digitW * (ADVANCE[ch] or 1)))
        if i > 1 then total = total + gap end
    end
    return total
end

-- renderFace lays the glyphs onto one canvas and returns the packed rows.
local function renderFace(text, digitW, h)
    local gap = math.max(1, floor(digitW * 0.14))
    local widths, total = {}, 0
    for i = 1, #text do
        local ch = text:sub(i, i)
        local w = math.max(2, floor(digitW * (ADVANCE[ch] or 1)))
        widths[i] = w
        total = total + w + (i > 1 and gap or 0)
    end

    local canvas = {}
    for y = 1, h do canvas[y] = {} end

    local thick = 0.13
    local x0 = 0
    for i = 1, #text do
        local w = widths[i]
        if i > 1 then x0 = x0 + gap end
        -- Thickness is a fraction of each glyph's own width, so a narrow
        -- glyph would get a proportionally thinner stroke. Scale it back up
        -- so the colon's dots carry the same weight as the digits' strokes.
        local ch = text:sub(i, i)
        local grid = rasterGlyph(ch, w, h, thick / (ADVANCE[ch] or 1))
        for y = 1, h do
            local src, dst = grid[y], canvas[y]
            for x = 1, w do
                local v = src[x]
                if v > 0 and v > (dst[x0 + x] or 0) then dst[x0 + x] = v end
            end
        end
        x0 = x0 + w
    end

    return packHalfBlocks(canvas, total, h), total
end

local faceCache = {}

function p:render(bands, frame, rows, cols)
    if rows <= 0 or cols <= 0 then return "" end

    local left = seconds_left()
    local text = left and string.format("%02d:%02d", floor(left / 60), left % 60) or "--:--"

    -- The progress line costs a row, and a blank spacer above it costs another.
    -- In a short band those rows are a large fraction of the clock, so the
    -- spacer is only spent when there is real height to give.
    local wantBar = rows >= 6
    local wantSpacer = rows >= 12
    local avail = rows - (wantBar and 1 or 0) - (wantSpacer and 1 or 0)

    -- Size from the height available, then shrink until the face really fits
    -- the width. faceWidth counts the same per-glyph minimums renderFace
    -- applies, which an estimate from digitW alone undershoots.
    local h = avail * 2
    local digitW = floor(0.72 * h * pixelAspect)
    local estimate = floor(digitW * 4.42 + digitW * 0.14 * 4)
    if estimate > cols and estimate > 0 then
        local factor = cols / estimate
        digitW = math.max(2, floor(digitW * factor))
        h = math.max(2, floor(h * factor))
        if h % 2 == 1 then h = h - 1 end
    end
    while digitW > 2 and faceWidth(text, digitW) > cols do
        h = math.max(2, floor(h * (digitW - 1) / digitW))
        if h % 2 == 1 then h = h - 1 end
        digitW = digitW - 1
    end

    -- Too short or too narrow for even the smallest face: plain text, clipped.
    if avail < 2 or faceWidth(text, digitW) > cols then
        local shown = text:sub(1, cols)
        local out = {}
        local mid = floor(rows / 2) + 1
        for i = 1, rows do
            out[i] = (i == mid) and padTo(shown, #shown, cols) or ""
        end
        return table.concat(out, "\n")
    end

    local key = text .. "|" .. rows .. "x" .. cols
    local face, faceW = faceCache.rows, faceCache.width
    if faceCache.key ~= key then
        face, faceW = renderFace(text, digitW, h)
        faceCache = {key = key, rows = face, width = faceW}
    end

    local out = {}
    for _ = 1, math.max(0, floor((avail - #face) / 2)) do out[#out + 1] = "" end
    for _, l in ipairs(face) do
        if #out >= avail then break end
        out[#out + 1] = padTo(l, faceW, cols)
    end
    while #out < avail do out[#out + 1] = "" end

    if wantBar then
        local fraction = 0
        if phase_total and phase_total > 0 and left then
            fraction = 1 - (left / phase_total)
        end
        if wantSpacer then out[#out + 1] = "" end
        out[#out + 1] = progressLine(fraction, faceW, cols)
    end

    return table.concat(out, "\n")
end

-- --------------------------------------------------------------------- keys

-- adjust moves the end of the running phase by delta_min minutes. Only the
-- current phase changes; the next one still uses the configured length. The
-- phase never drops below one minute, so shortening cannot skip it outright.
local function adjust(delta_min)
    if not phase then
        cliamp.message("Pomodoro is off (H starts it)")
        return
    end
    local left = seconds_left() or 0
    local new_left = math.max(60, left + delta_min * 60)
    local change = new_left - left
    phase_ends_at = os.time() + new_left
    -- Move the total by the same amount, so the progress line keeps what has
    -- already elapsed rather than jumping.
    phase_total = phase_total + change

    stop_timer()
    timer = cliamp.timer.after(new_left, phase_done)
    cliamp.message(string.format("%s: %d min left", phase == "work" and "Focus" or "Break",
        math.ceil(new_left / 60)))
end

local function bind(key, label, fn)
    local ok, why = p:bind(key, label, fn)
    if not ok then
        cliamp.log.warn("could not bind " .. key .. ": " .. tostring(why))
    end
end

bind("H", "Pomodoro", function()
    if phase then stop_session(false) else start_work() end
end)
bind(")", "Pomodoro +" .. ADJUST .. "m", function() adjust(ADJUST) end)
bind("(", "Pomodoro -" .. ADJUST .. "m", function() adjust(-ADJUST) end)

p:command("start", function()
    if phase then return status_text() end
    start_work()
    return status_text()
end)

p:command("stop", function()
    stop_session(true)
    return "Pomodoro stopped"
end)

p:command("status", function()
    return status_text()
end)

p:on("app.quit", function()
    stop_session(true)
end)

cliamp.log.info(string.format("pomodoro loaded (work=%g break=%g long=%g rounds=%d)",
    WORK, BREAK, LONG_BREAK, ROUNDS))
