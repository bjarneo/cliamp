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

func TestClockFallbackIsOptional(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "clock-bare", `
		local p = plugin.register({name = "clock-bare", type = "visualizer"})
		out = cliamp.clock("05:23")
	`)

	if got := m.plugins[0].L.GetGlobal("out").String(); got != clockMarker+"05:23"+clockMarker {
		t.Fatalf("clock() = %q", got)
	}
}
