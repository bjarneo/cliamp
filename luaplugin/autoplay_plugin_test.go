package luaplugin

import (
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// autoplayStub replaces the plugin and cliamp tables with an in-memory model
// of the player so plugins/autoplay.lua can be driven event by event. Every
// externally visible action lands in the global calls table.
const autoplayStub = `
calls = {}
H = {
    queue = {}, pending = {}, timers = {}, hooks = {}, cmds = {}, store = {},
    has_next = false, is_live = false, repeat_mode = "Off",
    cfg = { enabled_on_start = "true", count = "5" },
}
function H.rec(s) calls[#calls + 1] = s end
function yt(id) return "https://www.youtube.com/watch?v=" .. id end

plugin = { register = function(spec)
    return {
        on      = function(_, ev, fn) H.hooks[ev] = fn end,
        config  = function(_, k) return H.cfg[k] end,
        bind    = function(_, key, desc, fn) return true end,
        command = function(_, name, fn) H.cmds[name] = fn end,
    }
end }

cliamp = {
    store  = { get = function(k) return H.store[k] end, set = function(k, v) H.store[k] = v end },
    player = { repeat_mode = function() return H.repeat_mode end, next = function() H.rec("next") end },
    queue  = {
        current  = function() return #H.queue - 1 end,
        count    = function() return #H.queue end,
        has_next = function() return H.has_next end,
        list     = function()
            local out = {}
            for i, path in ipairs(H.queue) do out[i] = { path = path, index = i - 1, queued = false } end
            return out
        end,
        add = function(path)
            H.rec("add " .. path:match("v=(.*)"))
            H.pending[#H.pending + 1] = path
        end,
    },
    track = { is_live = function() return H.is_live end },
    exec  = { run = function(bin, args, opts)
        H.rec("fetch " .. args[#args]:match("list=RD(.*)"))
        H.exec = opts
        return { cancel = function() H.rec("cancel") end }, nil
    end },
    json    = { decode = function(s)
        local id = s:match('"id":"([^"]+)"')
        return id and { id = id, live_status = s:match('"live_status":"([^"]+)"') } or nil
    end },
    timer   = { after = function(sec, fn) H.timers[#H.timers + 1] = fn end },
    message = function(s) H.rec("msg " .. s) end,
    log     = { warn = function(s) H.rec("warn " .. s) end, info = function() end, debug = function() end },
}

-- Event helpers mirror what the core emits.
function H.track_change(path) H.hooks["track.change"]({ path = path }) end
function H.playing(path) H.hooks["playback.state"]({ status = "playing", path = path }) end
function H.stopped() H.hooks["playback.state"]({ status = "stopped", path = "" }) end
function H.queue_change() H.hooks["queue.change"]({ count = #H.queue, index = #H.queue - 1 }) end
function H.mode() H.hooks["player.mode"]({}) end
function H.queue_end(path) H.hooks["queue.end"]({ path = path }) end
function H.stop_by_user() H.hooks["playback.stop"]({}) end
-- yt-dlp output and exit for the most recent fetch.
function H.feed(ids) for _, id in ipairs(ids) do H.exec.on_stdout('{"id":"' .. id .. '"}') end end
function H.feed_live(id) H.exec.on_stdout('{"id":"' .. id .. '","live_status":"is_live"}') end
function H.exit(code) local e = H.exec; H.exec = nil; e.on_exit(code) end
-- land resolves the next n queue.add calls, one queue.change each.
function H.land(n)
    for i = 1, n do
        local path = table.remove(H.pending, 1)
        if path then
            H.queue[#H.queue + 1] = path
            H.has_next = true
            H.queue_change()
        end
    end
end
function H.fire_timers() local ts = H.timers; H.timers = {}; for _, fn in ipairs(ts) do fn() end end
`

func TestAutoplayPluginLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		setup  string // runs before the plugin loads
		script string
		want   []string
	}{
		{
			name: "prefill on the last track dedupes the seed and caps at count",
			script: `H.queue = {yt("s")}; H.track_change(yt("s"))
				H.feed({"s", "a", "b", "c", "d", "e", "f"}); H.exit(0)`,
			want: []string{"fetch s", "add a", "add b", "add c", "add d", "add e", "msg Autoplay: adding 5 related tracks"},
		},
		{
			name:   "count of zero falls back to five",
			setup:  `H.cfg.count = "0"`,
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a","b","c","d","e","f"}); H.exit(0)`,
			want:   []string{"fetch s", "add a", "add b", "add c", "add d", "add e", "msg Autoplay: adding 5 related tracks"},
		},
		{
			name:   "fractional count is truncated",
			setup:  `H.cfg.count = "2.5"`,
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a","b","c"}); H.exit(0)`,
			want:   []string{"fetch s", "add a", "add b", "msg Autoplay: adding 2 related tracks"},
		},
		{
			name: "a stale fetch finishing late neither clears nor duplicates the live one",
			script: `H.queue = {yt("s")}; H.track_change(yt("s"))
				local stale = H.exec
				H.queue = {yt("s"), yt("t")}; H.track_change(yt("t"))
				stale.on_stdout('{"id":"x"}'); stale.on_exit(0)
				H.queue_change()
				H.rec("status " .. H.cmds.status())
				H.feed({"y"}); H.exit(0)`,
			want: []string{"fetch s", "cancel", "fetch t", "status autoplay on (fetching), 5 per refill, key ctrl+a", "add y", "msg Autoplay: adding 1 related tracks"},
		},
		{
			name: "queue ends while adds resolve: resume once the first lands",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a", "b"}); H.exit(0)
				H.queue_end(yt("s"))
				H.land(1)
				H.land(1)`,
			want: []string{"fetch s", "add a", "add b", "msg Autoplay: adding 2 related tracks", "next"},
		},
		{
			name: "partial resolution then queue end: timeout refetches from the finished track",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a", "b"}); H.exit(0)
				H.land(1)
				H.track_change(yt("a"))
				H.has_next = false; H.queue_end(yt("a"))
				H.fire_timers()
				H.feed({"c"}); H.exit(0); H.land(1)`,
			want: []string{"fetch s", "add a", "add b", "msg Autoplay: adding 2 related tracks", "fetch a", "add c", "msg Autoplay: adding 1 related tracks", "next"},
		},
		{
			name: "user resumes playback while a resume is armed: no auto-next",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a"}); H.exit(0)
				H.queue_end(yt("s"))
				H.playing(yt("s"))
				H.land(1)`,
			want: []string{"fetch s", "add a", "msg Autoplay: adding 1 related tracks"},
		},
		{
			name: "toggle off mid-fetch cancels it and drops the result",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.cmds.toggle()
				H.feed({"a"}); H.exit(0)`,
			want: []string{"fetch s", "cancel", "msg Autoplay off"},
		},
		{
			name: "live at track start but not once playing: fetch when the stream is up",
			script: `H.queue = {yt("s")}; H.is_live = true; H.track_change(yt("s"))
				H.is_live = false; H.playing(yt("s"))`,
			want: []string{"fetch s"},
		},
		{
			name: "no fetch with a next track, with repeat on, or on a manual stop; repeat off refetches",
			script: `H.queue = {yt("s"), yt("t")}; H.has_next = true; H.track_change(yt("s"))
				H.has_next = false; H.repeat_mode = "All"; H.track_change(yt("t"))
				H.repeat_mode = "Off"; H.stopped()
				H.mode()`,
			want: []string{"fetch t"},
		},
		{
			name: "queue end with nothing pending fetches now and resumes when tracks land",
			script: `H.queue = {yt("s")}; H.queue_end(yt("s"))
				H.feed({"a"}); H.exit(0); H.land(1)`,
			want: []string{"fetch s", "add a", "msg Autoplay: adding 1 related tracks", "next"},
		},
		{
			name: "late arrival after a latched refill timeout still resumes",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a"}); H.exit(0)
				H.queue_end(yt("s"))
				H.fire_timers()
				H.land(1)`,
			want: []string{"fetch s", "add a", "msg Autoplay: adding 1 related tracks", "next"},
		},
		{
			name: "an explicit stop cancels a pending resume",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"a"}); H.exit(0)
				H.queue_end(yt("s"))
				H.stop_by_user()
				H.land(1)`,
			want: []string{"fetch s", "add a", "msg Autoplay: adding 1 related tracks"},
		},
		{
			name: "queue end on a non-YouTube track arms nothing",
			script: `H.queue = {"/music/a.flac"}; H.track_change("/music/a.flac")
				H.queue_end("/music/a.flac")
				H.queue[#H.queue + 1] = "/music/b.flac"; H.has_next = true; H.queue_change()`,
			want: nil,
		},
		{
			name: "live mix entries are never queued",
			script: `H.queue = {yt("s")}; H.track_change(yt("s"))
				H.feed({"a"}); H.feed_live("radio"); H.feed({"b"}); H.exit(0)`,
			want: []string{"fetch s", "add a", "add b", "msg Autoplay: adding 2 related tracks"},
		},
		{
			name: "an empty mix latches the seed so it is not refetched",
			script: `H.queue = {yt("s")}; H.track_change(yt("s")); H.feed({"s"}); H.exit(0)
				H.mode()`,
			want: []string{"fetch s", "msg Autoplay: no new related tracks"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := lua.NewState()
			defer L.Close()
			if err := L.DoString(autoplayStub); err != nil {
				t.Fatal(err)
			}
			if tt.setup != "" {
				if err := L.DoString(tt.setup); err != nil {
					t.Fatal(err)
				}
			}
			if err := L.DoFile(filepath.Join("..", "plugins", "autoplay.lua")); err != nil {
				t.Fatal(err)
			}
			if err := L.DoString(tt.script); err != nil {
				t.Fatal(err)
			}
			var got []string
			L.GetGlobal("calls").(*lua.LTable).ForEach(func(_, v lua.LValue) {
				got = append(got, v.String())
			})
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}
