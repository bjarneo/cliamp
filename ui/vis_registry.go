package ui

import "strings"

// VisMode selects the visualizer rendering style.
type VisMode int

const (
	VisBars        VisMode = iota // smooth fractional blocks
	VisBarsDot                    // bars with braille dot stipple
	VisRain                       // falling rain droplets within bar shapes
	VisBarsOutline                // top-edge outline of bars
	VisBricks                     // solid bricks with gaps
	VisColumns                    // many thin columns
	VisClassicPeak                // classic falling peak caps over thin columns
	VisWave                       // braille waveform oscilloscope
	VisScatter                    // braille particle sparkle
	VisFlame                      // braille rising flame tendrils
	VisRetro                      // 80s synthwave perspective grid with wave
	VisPulse                      // braille pulsating circle
	VisMatrix                     // falling matrix rain characters
	VisBinary                     // streaming binary 0s and 1s
	VisSakura                     // falling cherry blossom petals
	VisFirework                   // exploding firework bursts
	VisBubbles                    // rising hollow ring bubbles
	VisLogo                       // CLIAMP pixel text
	VisTerrain                    // scrolling side-view mountain range
	VisScope                      // Lissajous XY oscilloscope
	VisHeartbeat                  // ECG pulse monitor trace
	VisButterfly                  // mirrored Rorschach spectrum
	VisAscii                      // dense shade-block columns (website style)
	VisFirefly                    // firefly meadow at dusk
	VisMosaic                     // static heatmap of flickering tiles
	VisSand                       // falling-sand cellular automaton
	VisGeyser                     // bass-driven particle fountain
	VisClassicLED                 // Winamp 2.9 LED matrix with falling peak caps
	VisStereo                     // stereo L/R horizontal LED peak meters
	VisMirror                     // Braille spectrum bars mirrored about a horizontal axis
	VisOmarchy                    // dithered pixel field with the Omarchy mark (omarchy.org style)
	VisRedSector                  // tumbling wireframe equalizer over a drifting starfield
	VisYinYang                    // two koi chasing round a lily pad, flicking their tails to the drums
	VisNone                       // hidden — no visualizer
	VisCount                      // sentinel for cycling
)

// visEntry pairs a display name with a factory for that mode's visModeDriver.
type visEntry struct {
	name      string
	newDriver func() visModeDriver
}

// visModes is the single source of truth for all visualizer modes.
// To add a new mode: add a const, add one line here, create a vis_*.go file.
var visModes = [VisCount]visEntry{
	VisBars:        {"Bars", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBars)},
	VisBarsDot:     {"BarsDot", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBarsDot)},
	VisRain:        {"Rain", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderRain)},
	VisBarsOutline: {"BarsOutline", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBarsOutline)},
	VisBricks:      {"Bricks", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBricks)},
	VisColumns:     {"Columns", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderColumns)},
	VisClassicPeak: {"ClassicPeak", newClassicPeakDriver},
	VisWave:        {"Wave", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderWave() })},
	VisScatter:     {"Scatter", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderScatter)},
	VisFlame:       {"Flame", newFlameDriver},
	VisRetro:       {"Retro", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderRetro)},
	VisPulse:       {"Pulse", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderPulse)},
	VisMatrix:      {"Matrix", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderMatrix)},
	VisBinary:      {"Binary", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderBinary)},
	VisSakura:      {"Sakura", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderSakura)},
	VisFirework:    {"Firework", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderFirework)},
	VisBubbles:     {"Bubbles", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderBubbles)},
	VisLogo:        {"Logo", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderLogo)},
	VisTerrain:     {"Terrain", newTerrainDriver},
	VisScope:       {"Scope", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderScope() })},
	VisHeartbeat:   {"Heartbeat", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderHeartbeat() })},
	VisButterfly:   {"Butterfly", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderButterfly)},
	VisAscii:       {"Ascii", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderAscii)},
	VisFirefly:     {"Firefly", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderFirefly)},
	VisMosaic:      {"Mosaic", newMosaicDriver},
	VisSand:        {"Sand", newSandDriver},
	VisGeyser:      {"Geyser", newGeyserDriver},
	VisClassicLED:  {"ClassicLED", newClassicLEDDriver},
	VisStereo:      {"Stereo", newStereoDriver},
	VisMirror:      {"Mirror", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderMirror)},
	VisOmarchy:     {"Omarchy", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderOmarchy)},
	VisRedSector:   {"RedSector", newRedSectorDriver},
	VisYinYang:     {"YinYang", newYinYangDriver},
	VisNone:        {"None", newNoOpDriver},
}

// visNameMap maps the lowercase name of each built-in mode to the mode. The
// init function builds it, and nothing changes it after that.
var visNameMap map[string]VisMode

func init() {
	visNameMap = make(map[string]VisMode, VisCount)
	for i := range VisCount {
		visNameMap[strings.ToLower(visModes[i].name)] = VisMode(i)
	}
}

// ModeName returns the display name of the current mode.
func (v *Visualizer) ModeName() string {
	if v.Mode < VisCount {
		return visModes[v.Mode].name
	}
	luaIdx := int(v.Mode - VisCount)
	if luaIdx < len(v.luaVisNames) {
		return v.luaVisNames[luaIdx]
	}
	return "Unknown"
}

// StringToVisModeExact converts a built-in mode name to VisMode, returning
// false if not found.
func StringToVisModeExact(name string) (VisMode, bool) {
	mode, ok := visNameMap[strings.ToLower(name)]
	return mode, ok
}

// ModeByName converts a name to VisMode, ignoring case. A built-in name wins
// over a Lua name, and an earlier Lua name wins over a later one, so a
// plugin named Bars cannot hide the built-in Bars. It returns false if no
// mode of v has the name.
func (v *Visualizer) ModeByName(name string) (VisMode, bool) {
	if mode, ok := StringToVisModeExact(name); ok {
		return mode, true
	}
	for i, luaName := range v.luaVisNames {
		if strings.EqualFold(luaName, name) {
			return VisCount + VisMode(i), true
		}
	}
	return 0, false
}

// VisModeNames returns the display names of all built-in visualizer modes.
func VisModeNames() []string {
	names := make([]string, VisCount)
	for i := range VisCount {
		names[i] = visModes[i].name
	}
	return names
}

// AllModeNames returns the display names of every selectable visualizer mode in
// cycle order: built-in modes followed by any registered Lua visualizers. The
// index of each name equals its VisMode value, so a picker can map a list row
// directly to a mode.
func (v *Visualizer) AllModeNames() []string {
	return append(VisModeNames(), v.luaVisNames...)
}

// SetMode switches to mode if it is within range (built-in or Lua) and requests
// a refresh. Out-of-range values are ignored, matching the SetVisualizer guard.
func (v *Visualizer) SetMode(mode VisMode) {
	if mode < 0 || mode >= VisCount+VisMode(len(v.luaVisNames)) {
		return
	}
	v.Mode = mode
	v.RequestRefresh()
}

// CycleMode advances to the next visualizer mode, including Lua visualizers.
func (v *Visualizer) CycleMode() {
	total := VisCount + VisMode(len(v.luaVisNames))
	v.Mode = (v.Mode + 1) % total
}
