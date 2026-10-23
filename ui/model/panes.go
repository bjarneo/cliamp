package model

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/bjarneo/cliamp/v2/ui"
	"github.com/charmbracelet/x/ansi"
)

// paneRefreshInterval is how often a plugin pane re-renders. Plugin render runs
// on the UI goroutine, so it stays under the frame budget: since the image work
// moved off the render path, one Lua call per pane per frame is cheap.
const paneRefreshInterval = 50 * time.Millisecond

type paneCacheEntry struct {
	text string
	at   time.Time
}

// slotVisualizers keeps one Visualizer per pane that shows a built-in mode.
// They share the primary analysis through SetBands, so a second pane costs a
// driver render, not a second FFT.
type slotVisualizers struct {
	mu     sync.Mutex
	byName map[string]*ui.Visualizer
}

func newSlotVisualizers() *slotVisualizers {
	return &slotVisualizers{byName: make(map[string]*ui.Visualizer)}
}

// tickAll advances every pane visualizer with the primary's context, so drivers
// that keep their own state (stereo levels, rain, peak holds) animate instead of
// staying at rest.
func (s *slotVisualizers) tickAll(ctx ui.VisTickContext, samples []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.byName {
		// Wave, Scope and Heartbeat draw the raw samples; the primary's analysis
		// produced them, so the pane copies instead of analyzing again.
		v.SetWaveSamples(samples)
		v.Tick(ctx)
	}
}

func (s *slotVisualizers) get(name string, mode ui.VisMode, sampleRate float64) *ui.Visualizer {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byName[name]
	if !ok {
		v = ui.NewVisualizer(sampleRate)
		s.byName[name] = v
	}
	v.Mode = mode
	return v
}

type paneCache struct {
	mu      sync.Mutex
	entries map[string]paneCacheEntry
}

func newPaneCache() *paneCache {
	return &paneCache{entries: make(map[string]paneCacheEntry)}
}

func (c *paneCache) get(key string) (paneCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok
}

func (c *paneCache) set(key, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = paneCacheEntry{text: text, at: time.Now()}
}

// SetPaneSet wires the runtime layout registry.
func (m *Model) SetPaneSet(panes *ui.PaneSet) {
	m.panes = panes
	if m.paneCache == nil {
		m.paneCache = newPaneCache()
	}
	if m.slotVis == nil {
		m.slotVis = newSlotVisualizers()
	}
	if m.artRef == nil {
		m.artRef = newArtRef()
	}
}

// regionPanes returns the panes of one region, builtin only when nothing is
// registered.
func (m Model) regionPanes(region ui.Region) []ui.PaneSpec {
	if m.panes == nil {
		return []ui.PaneSpec{{Region: region, Pane: ui.PaneBuiltin, Weight: 1}}
	}
	return m.panes.RegionPanes(region)
}

func paneWeights(panes []ui.PaneSpec) []int {
	weights := make([]int, len(panes))
	for i, pane := range panes {
		weights[i] = max(1, pane.Weight)
	}
	return weights
}

// renderPaneVis renders a pane that has a visualizer assigned. A built-in name
// uses a shared-analysis Visualizer; anything else is a plugin visualizer.
func (m Model) renderPaneVis(vis string, rows, cols int) string {
	if mode, ok := ui.StringToVisModeExact(vis); ok {
		if m.slotVis == nil {
			return ""
		}
		v := m.slotVis.get(vis, mode, 44100)
		v.Rows = rows
		v.Cols = cols
		v.SetBands(m.vis.SmoothedBands())
		return v.Render()
	}
	return m.renderPluginPane(vis, rows, cols)
}

// renderPluginPane asks a plugin visualizer for a frame sized to its box.
func (m Model) renderPluginPane(name string, rows, cols int) string {
	if m.luaMgr == nil || rows <= 0 || cols <= 0 {
		return ""
	}
	key := fmt.Sprintf("%s|%d|%d", name, rows, cols)
	if m.paneCache != nil {
		if entry, ok := m.paneCache.get(key); ok && time.Since(entry.at) < paneRefreshInterval {
			return entry.text
		}
	}
	bands := m.vis.SmoothedBands()
	var values [10]float64
	copy(values[:], bands)
	text := m.luaMgr.RenderVis(name, values, rows, cols, m.vis.Frame())
	// A block with image placeholders must reach the renderer every frame: the
	// API alternates an invisible reset so the line counts as changed, and the
	// cache would swallow that and leave the image stale.
	if m.paneCache != nil && !strings.Contains(text, "\U0010EEEE") {
		m.paneCache.set(key, text)
	}
	return text
}

// joinPanesRow lays panes side by side, padding each to its width.
func joinPanesRow(blocks []string, widths []int, rows int) string {
	split := make([][]string, len(blocks))
	for i, block := range blocks {
		split[i] = strings.Split(block, "\n")
	}
	lines := make([]string, rows)
	for row := range rows {
		var b strings.Builder
		for i := range blocks {
			text := ""
			if row < len(split[i]) {
				text = split[i][row]
			}
			// A plugin can return a wider line; clip it so it cannot push the
			// next pane out of place.
			text = ansi.Truncate(text, widths[i], "")
			b.WriteString(text)
			if pad := widths[i] - lipglossWidth(text); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		lines[row] = b.String()
	}
	return strings.Join(lines, "\n")
}

// lipglossWidth measures text in terminal cells, ignoring escapes.
func lipglossWidth(text string) int {
	return lipgloss.Width(text)
}

// artRef holds the cover of the current playback state. Plugins read it from
// their own goroutine, so it lives behind an atomic value instead of on the
// model.
type artRef struct {
	value atomic.Value // string
}

func newArtRef() *artRef {
	ref := &artRef{}
	ref.value.Store("")
	return ref
}

func (r *artRef) set(url string) {
	if r == nil {
		return
	}
	r.value.Store(url)
}

func (r *artRef) get() string {
	if r == nil {
		return ""
	}
	url, _ := r.value.Load().(string)
	return url
}

// CurrentArtFunc returns a func a plugin API can call to read the cover of the
// current playback state. It is safe to call from any goroutine.
func (m *Model) CurrentArtFunc() func() string {
	ref := m.artRef
	return ref.get
}
