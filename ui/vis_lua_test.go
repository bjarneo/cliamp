package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeLuaHost records the calls that the Visualizer makes to a LuaVisHost.
type fakeLuaHost struct {
	calls []string
	bands [DefaultSpectrumBands]float64
}

func (h *fakeLuaHost) RenderVis(name string, bands [DefaultSpectrumBands]float64, rows, cols int, _ uint64) string {
	h.bands = bands
	return name
}

func (h *fakeLuaHost) InitVis(name string, rows, cols int) {
	h.calls = append(h.calls, fmt.Sprintf("init %s %dx%d", name, rows, cols))
}

func (h *fakeLuaHost) DestroyVis(name string) {
	h.calls = append(h.calls, "destroy "+name)
}

// newLuaTestVisualizer returns a sized Visualizer with the Lua modes a and b.
func newLuaTestVisualizer(t *testing.T, host LuaVisHost) *Visualizer {
	t.Helper()
	v := NewVisualizer(44100)
	v.Rows, v.Cols = 8, 40
	v.RegisterLuaVisualizers([]string{"a", "b"}, host)
	return v
}

// A Lua mode gets its init before the first frame after it is selected, and
// its destroy when the mode is left after an init. A mode that is left
// before it drew a frame gets neither.
func TestLuaModeInitAndDestroy(t *testing.T) {
	const (
		modeA = VisCount
		modeB = VisCount + 1
	)
	tests := []struct {
		name  string
		steps func(v *Visualizer)
		want  []string
	}{
		{
			name: "select, draw twice, leave",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.Render()
				v.SetMode(VisBars)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a"},
		},
		{
			name: "switch from one Lua mode to another",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.SetMode(modeB)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init b 8x40"},
		},
		{
			name: "leave before the first frame",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.TickInterval(VisTickContext{})
				v.SetMode(VisBars)
				v.Render()
			},
			want: nil,
		},
		{
			name: "cycle through a mode and back",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.SetMode(VisBars)
				v.Render()
				v.SetMode(modeA)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init a 8x40"},
		},
		{
			name: "register the same names again while a Lua mode is active",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.RegisterLuaVisualizers([]string{"a", "b"}, v.luaHost)
				v.Render()
				v.SetMode(VisBars)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init a 8x40", "destroy a"},
		},
		{
			name: "register a new plugin at the index of the active mode",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.RegisterLuaVisualizers([]string{"z", "a"}, v.luaHost)
				v.Render()
				v.SetMode(VisBars)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init z 8x40", "destroy z"},
		},
		{
			name: "register again before the first frame",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.TickInterval(VisTickContext{})
				v.RegisterLuaVisualizers([]string{"a", "b"}, v.luaHost)
				v.Render()
			},
			want: []string{"init a 8x40"},
		},
		{
			name: "register again while a built-in mode is active",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.SetMode(VisBars)
				v.Render()
				v.RegisterLuaVisualizers([]string{"a", "b"}, v.luaHost)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeLuaHost{}
			v := newLuaTestVisualizer(t, host)
			tt.steps(v)
			if !reflect.DeepEqual(host.calls, tt.want) {
				t.Fatalf("calls = %q, want %q", host.calls, tt.want)
			}
		})
	}
}

// A Lua mode with no host draws a blank frame and does not panic.
func TestLuaModeWithoutHost(t *testing.T) {
	v := newLuaTestVisualizer(t, nil)
	v.SetMode(VisCount)
	if got := strings.TrimSpace(v.Render()); got != "" {
		t.Fatalf("Render() = %q, want a blank frame", got)
	}
	v.SetMode(VisBars)
	v.Render()
}

// A Lua mode gets the smoothed bands that the built-in spectrum modes draw,
// not the raw analysis.
func TestLuaModeGetsSmoothedBands(t *testing.T) {
	host := &fakeLuaHost{}
	v := newLuaTestVisualizer(t, host)
	v.SetMode(VisCount)
	for i := range v.bands {
		v.bands[i] = 1
	}
	v.smoothedBands = make([]float64, len(v.bands))
	for i := range v.smoothedBands {
		v.smoothedBands[i] = 0.5
	}
	v.Render()
	for i, got := range host.bands {
		if got != 0.5 {
			t.Fatalf("band %d = %v, want the smoothed 0.5", i, got)
		}
	}
}

// A Lua visualizer cannot take the name of a built-in mode or of an earlier
// Lua mode, so a name always selects the same mode. It stays in the cycle
// and in the picker. Each Visualizer knows only its own Lua names, and the
// built-in names never change.
func TestRegisterLuaVisualizersKeepsNames(t *testing.T) {
	v := NewVisualizer(44100)
	v.RegisterLuaVisualizers([]string{"old"}, nil)
	v.RegisterLuaVisualizers([]string{"Bars", "Custom", "custom"}, nil)
	other := NewVisualizer(44100)
	other.RegisterLuaVisualizers([]string{"Other", "Custom"}, nil)
	tests := []struct {
		name   string
		want   VisMode
		wantOK bool
	}{
		{"bars", VisBars, true},
		{"Custom", VisCount + 1, true},
		{"CUSTOM", VisCount + 1, true},
		{"old", 0, false},
		{"other", 0, false},
	}
	for _, tt := range tests {
		got, ok := v.ModeByName(tt.name)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ModeByName(%q) = %v, %v; want %v, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
	if got, ok := other.ModeByName("custom"); got != VisCount+1 || !ok {
		t.Errorf("other ModeByName(custom) = %v, %v; want %v, true", got, ok, VisCount+1)
	}
	if _, ok := StringToVisModeExact("custom"); ok || len(visNameMap) != int(VisCount) {
		t.Errorf("the built-in names changed: %d names, custom found = %v", len(visNameMap), ok)
	}
	if names := v.AllModeNames(); names[VisCount] != "Bars" {
		t.Errorf("AllModeNames()[VisCount] = %q, want the Lua Bars in the cycle", names[VisCount])
	}
}
