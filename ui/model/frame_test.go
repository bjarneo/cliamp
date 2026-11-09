package model

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// framePlayer plays a moving tone, so the visualizers draw real shapes.
type framePlayer struct {
	playbackFakeEngine
	phase float64
}

func (f *framePlayer) SamplesInto(dst []float64) int {
	for i := range dst {
		dst[i] = 0.6 * math.Sin(f.phase+float64(i)*2*math.Pi*440/44100)
	}
	f.phase += 0.3
	return len(dst)
}

func (f *framePlayer) WaveformSamplesInto(dst []float64) int { return f.SamplesInto(dst) }

func (f *framePlayer) StereoSamplesInto(dst [][2]float64) int {
	mono := make([]float64, len(dst))
	f.SamplesInto(mono)
	for i, v := range mono {
		dst[i] = [2]float64{v, -v}
	}
	return len(dst)
}

func newFrameModel(width, height int, mode ui.VisMode) (Model, *framePlayer) {
	eng := &framePlayer{playbackFakeEngine: playbackFakeEngine{playing: true, duration: 300 * time.Second, seekable: true}}
	pl := playlist.New()
	titles := []string{"Voodoo People", "Cœur de pirate — Ensemble", "東京 Nights", "A rather long title that will not fit in a narrow terminal at all"}
	for i := range 40 {
		pl.Add(playlist.Track{Path: fmt.Sprintf("/music/%02d.flac", i), Title: titles[i%len(titles)], Artist: "The Prodigy", DurationSecs: 200 + i})
	}
	pl.SetIndex(3)
	m := Model{player: eng, playlist: pl, vis: ui.NewVisualizer(44100), width: width, height: height, focus: focusPlaylist}
	m.vis.Mode = mode
	m.recomputeLayout()
	return m, eng
}

// lipglossFrame is how View drew the frame before renderFrame.
func lipglossFrame(m Model, content string) string {
	frame := m.layout.frameStyle().Render(content)
	padLeft := max(0, (m.width-lipgloss.Width(frame))/2)
	padTop := max(0, (m.height-lipgloss.Height(frame))/2)
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", padLeft) + l
	}
	return ui.FitRect(strings.Repeat("\n", padTop)+strings.Join(lines, "\n"), m.layout.frameWidth, max(1, m.height))
}

// renderFrame draws the same bytes as before on every screen, at every size,
// in every visualizer mode, with the help bar shown and hidden.
func TestRenderFrameMatchesLipgloss(t *testing.T) {
	screens := map[string]func(*Model){
		"main":               func(*Model) {},
		"full visualizer":    func(m *Model) { m.fullVis = true },
		"keymap":             func(m *Model) { m.keymap.visible = true },
		"file browser":       func(m *Model) { m.fileBrowser.visible = true },
		"theme picker":       func(m *Model) { m.themePicker.visible = true },
		"visualizer picker":  func(m *Model) { m.visPicker.visible = true },
		"playlist picker":    func(m *Model) { m.plPicker.visible = true },
		"playlist manager":   func(m *Model) { m.plManager = plManagerState{visible: true, screen: plMgrScreenList} },
		"queue":              func(m *Model) { m.queue.visible = true },
		"info":               func(m *Model) { m.info.visible = true },
		"lyrics":             func(m *Model) { m.lyrics.visible = true },
		"device picker":      func(m *Model) { m.devicePicker.visible = true },
		"search results":     func(m *Model) { m.searchOverlay.visible = true },
		"navigation browser": func(m *Model) { m.navBrowser.visible = true },
		"subscriptions":      func(m *Model) { m.subs.visible = true },
		"jump":               func(m *Model) { m.jump.active = true },
		"URL input":          func(m *Model) { m.urlInput.active = true },
		"sign-in code":       func(m *Model) { m.authCode.active = true },
		"playlist search":    func(m *Model) { m.search.active = true },
		"radio search":       func(m *Model) { m.netSearch.active = true },
		"simplified":         func(m *Model) { m.simplified = true },
	}
	sizes := [][2]int{{205, 102}, {200, 52}, {160, 45}, {120, 40}, {100, 30}, {80, 24}, {79, 23}, {72, 22}, {60, 18}, {56, 16}, {50, 14}, {40, 10}}
	for name, open := range screens {
		for mode := ui.VisMode(0); mode < ui.VisCount; mode++ {
			for _, help := range []bool{true, false} {
				for _, sz := range sizes {
					m, _ := newFrameModel(sz[0], sz[1], mode)
					m.hideHelpBar = !help
					open(&m)
					m.recomputeLayout()
					if m.layout.tooSmall() {
						continue
					}
					content := m.renderFullVisualizer()
					if m.activeScreen() != screenFullVisualizer {
						content = strings.Join(m.mainSections(m.renderBodyRegion(), true, m.usesContentFirstLayout()), "\n")
					}
					if m.renderFrame(content) != lipglossFrame(m, content) {
						t.Errorf("%s, mode %d, help %v, %dx%d: frames differ", name, mode, help, sz[0], sz[1])
					}
				}
			}
		}
	}
	m, _ := newFrameModel(80, 24, ui.VisBars)
	if content := "a\tb\r\nc\t\td\nend\r\n"; m.renderFrame(content) != lipglossFrame(m, content) {
		t.Error("tabs and CRLF: frames differ")
	}
}

// A line wider than the panel is cut, so it no longer pushes the rows below
// it down and the last ones off the screen.
func TestRenderFrameCutsWideLines(t *testing.T) {
	m, _ := newFrameModel(80, 24, ui.VisBars)
	var rows []string
	for _, l := range strings.Split(m.renderFrame("first\n"+strings.Repeat("word ", 40)+"\nlast"), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			rows = append(rows, s)
		}
	}
	if len(rows) != 3 || rows[0] != "first" || rows[2] != "last" {
		t.Fatalf("rows = %q", rows)
	}
}

func TestFullVisHelpKeepsWholeHints(t *testing.T) {
	for _, w := range []int{205, 80, 72, 56, 40} {
		m, _ := newFrameModel(w, 24, ui.VisWave)
		m.fullVis = true
		m.recomputeLayout()
		help := m.fullVisHelp()
		if lipgloss.Width(help) > m.layout.panelWidth || !strings.Contains(help, "Exit") {
			t.Errorf("width %d: help %q", w, help)
		}
	}
}

// BenchmarkPlayingFrame is one tick and one View while music plays.
func BenchmarkPlayingFrame(b *testing.B) {
	for _, c := range []struct {
		name string
		mode ui.VisMode
		full bool
		w, h int
	}{
		{"wave/205x102", ui.VisWave, false, 205, 102},
		{"wave/120x40", ui.VisWave, false, 120, 40},
		{"bars/205x102", ui.VisBars, false, 205, 102},
		{"wave-full/205x102", ui.VisWave, true, 205, 102},
	} {
		b.Run(c.name, func(b *testing.B) {
			m, eng := newFrameModel(c.w, c.h, c.mode)
			m.fullVis = c.full
			m.recomputeLayout()
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				eng.position = time.Duration(i) * 16 * time.Millisecond
				next, _ := m.Update(tickMsg(time.Now()))
				m = next.(Model)
				m.View()
			}
		})
	}
}
