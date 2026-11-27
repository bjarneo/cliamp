package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ui"
)

// f inside the full-screen visualizer drops the track line, time, seek bar
// and padding so only the visualizer is drawn, edge to edge.
func TestFullVisFKeyPureMode(t *testing.T) {
	m := fullVisModel(t)
	m.vis = ui.NewVisualizer(44100)
	m.width, m.height = 80, 24
	m.recomputeLayout()
	if m.vis.Rows >= 24 {
		t.Fatalf("normal full vis rows = %d, want fewer than 24", m.vis.Rows)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'f'})

	if !m.pureVis {
		t.Fatal("f did not enable pure mode")
	}
	if m.layout.paddingH != 0 || m.layout.paddingV != 0 {
		t.Errorf("padding = %d,%d, want 0,0", m.layout.paddingH, m.layout.paddingV)
	}
	if m.vis.Rows != 24 || m.vis.Cols != 80 {
		t.Errorf("vis = %dx%d, want 80x24", m.vis.Cols, m.vis.Rows)
	}
	out := stripAnsi(m.renderFullVisualizer())
	if strings.Contains(out, "Netanyahu Knew") || strings.Contains(out, "[Plain]") {
		t.Errorf("pure mode still shows track info:\n%s", out)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'f'})
	if m.pureVis || m.vis.Rows >= 24 {
		t.Errorf("second f did not restore the normal view (pure=%v rows=%d)", m.pureVis, m.vis.Rows)
	}
}
