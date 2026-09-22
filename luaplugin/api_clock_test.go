package luaplugin

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/ui"
)

// The marker is a wire format between a plugin's frame and the renderer that
// expands it. Two constants, one string: if they drift, plugins get the
// marker printed as text instead of a clock.
func TestClockMarkerMatchesTheRenderer(t *testing.T) {
	if clockMarker != ui.ClockMarker {
		t.Fatalf("luaplugin marker %q != ui marker %q", clockMarker, ui.ClockMarker)
	}
}

func TestClockWrapsTheTimeAndKeepsTheFallback(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "clock", `
		local p = plugin.register({name = "clock", type = "visualizer"})
		out = cliamp.clock("05:23", "block face")
	`)

	got := m.plugins[0].L.GetGlobal("out").String()
	want := clockMarker + "05:23" + clockMarker + "block face"
	if got != want {
		t.Fatalf("clock() = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, "block face") {
		t.Fatal("the fallback rendering was lost")
	}
}

// A clock face with no fallback would render as an empty panel on every
// terminal that cannot show images, and a plugin author working in one that
// can would never see it. Better to fail at the call.
func TestClockRequiresAFallback(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "clock-bare", `
		local p = plugin.register({name = "clock-bare", type = "visualizer"})
		ok = pcall(function() return cliamp.clock("05:23") end)
	`)

	if v := m.plugins[0].L.GetGlobal("ok"); v.String() != "false" {
		t.Fatalf("clock() without a fallback returned %s, want an error", v.String())
	}
}

// The marker delimits the text, so a zero byte inside it would split the
// frame in the wrong place.
func TestClockRejectsAMarkerInTheText(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "clock-nul", `
		local p = plugin.register({name = "clock-nul", type = "visualizer"})
		ok = pcall(function() return cliamp.clock("05:23\0oops", "face") end)
	`)

	if v := m.plugins[0].L.GetGlobal("ok"); v.String() != "false" {
		t.Fatalf("clock() accepted a zero byte in the text (%s)", v.String())
	}
}
