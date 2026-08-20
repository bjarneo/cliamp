package model

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ui"
)

type tickMsg time.Time
type autoPlayMsg struct{}

var teaTick = tea.Tick

func tickCmd() tea.Cmd {
	return tickCmdAt(ui.TickFast)
}

func tickCmdAt(d time.Duration) tea.Cmd {
	return teaTick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) visualizerVisible() bool {
	if m.vis == nil || m.vis.Mode == ui.VisNone || m.vis.Rows <= 0 || m.vis.Cols <= 0 || m.layout.tooSmall() {
		return false
	}
	if m.fullVis {
		return true
	}
	return m.layout.visualizerRows > 0 && !m.usesContentFirstLayout()
}

func (m *Model) visualizerPlaying() bool {
	return m.player != nil && m.visualizerVisible() &&
		!m.isOverlayActive() && m.player.IsPlaying() && !m.player.IsPaused()
}

func (m *Model) visualizerPaused() bool {
	return m.player != nil && m.visualizerVisible() &&
		!m.isOverlayActive() && m.player.IsPlaying() && m.player.IsPaused()
}

// visualizerSettlingPaused reports whether playback is paused and the
// visualizer still has spectrum content easing to zero. While true the tick
// stays above the idle cadence so the bars fall; once settled the model can
// return to the fully-idle cadence.
func (m *Model) visualizerSettlingPaused() bool {
	if m.player == nil || !m.player.IsPaused() || m.isOverlayActive() {
		return false
	}
	if !m.visualizerVisible() {
		return false
	}
	return m.vis.PausedDecayPending(m.visualizerTickContext(time.Time{}))
}

func (m *Model) visualizerTickContext(now time.Time) ui.VisTickContext {
	sampled := false
	samplesRead := 0
	sampledSize := 0
	cache := map[ui.VisAnalysisSpec][]float64{}

	return ui.VisTickContext{
		Now:           now,
		Playing:       m.visualizerPlaying(),
		Paused:        m.visualizerPaused(),
		OverlayActive: m.isOverlayActive(),
		StereoSamplesInto: func(dst [][2]float64) int {
			if m.player == nil || m.vis == nil || m.vis.Mode == ui.VisNone {
				return 0
			}
			n := m.player.StereoSamplesInto(dst)
			gain := 1.0
			if m.visVolumeLinked {
				gain = math.Pow(10, m.player.Volume()/20)
			}
			mono := m.player.Mono()
			for i := range n {
				if mono {
					mixed := (dst[i][0] + dst[i][1]) / 2
					dst[i] = [2]float64{mixed, mixed}
				}
				dst[i][0] *= gain
				dst[i][1] *= gain
			}
			return n
		},
		Analyze: func(spec ui.VisAnalysisSpec) []float64 {
			spec = ui.NormalizeAnalysisSpec(spec)
			if m.player == nil || m.vis == nil || m.vis.Mode == ui.VisNone {
				return nil
			}
			if bands, ok := cache[spec]; ok {
				return bands
			}
			if m.player.IsPaused() {
				// Paused playback yields no new samples; feed silence so
				// spectrum content eases down to rest instead of freezing on
				// the last played frame held in the audio tap.
				bands := m.vis.Analyze(nil, spec)
				cache[spec] = bands
				return bands
			}
			buf := m.vis.EnsureSampleBuf(spec.FFTSize)
			if !sampled || spec.FFTSize > sampledSize {
				samplesRead = m.player.SamplesInto(buf)
				if m.visVolumeLinked {
					gain := math.Pow(10, m.player.Volume()/20)
					for i := range samplesRead {
						buf[i] *= gain
					}
				}
				sampled = true
				sampledSize = spec.FFTSize
			}
			start := max(0, samplesRead-spec.FFTSize)
			bands := m.vis.Analyze(buf[start:samplesRead], spec)
			cache[spec] = bands
			return bands
		},
	}
}

func (m *Model) tickDelta(now time.Time) time.Duration {
	dt := m.tickInterval()
	if !now.IsZero() && !m.lastTickAt.IsZero() {
		dt = now.Sub(m.lastTickAt)
	}
	if dt <= 0 {
		dt = ui.TickFast
	}
	if !now.IsZero() {
		m.lastTickAt = now
	}
	return dt
}

func advanceTickUnits(counter *int, elapsed *time.Duration, dt, quantum time.Duration) int {
	if *counter <= 0 {
		*elapsed = 0
		return 0
	}
	*elapsed += dt
	if *elapsed < quantum {
		return 0
	}
	steps := min(int(*elapsed/quantum), *counter)
	*counter -= steps
	if *counter == 0 {
		*elapsed = 0
		return steps
	}
	*elapsed -= time.Duration(steps) * quantum
	return steps
}

func (m *Model) tickInterval() time.Duration {
	if m.termTitle.introActive {
		return ui.TickFast
	}
	// Fully idle: stopped or paused with nothing self-animating. Drop to the
	// idle cadence so the CPU can sit in a low P-state between user actions.
	// Bubbletea still wakes immediately on key / IPC / MPRIS / plugin events.
	if m.isFullyIdle() {
		return ui.TickIdle
	}
	d := ui.TickSlow
	if m.visualizerVisible() {
		d = m.vis.TickInterval(m.visualizerTickContext(time.Time{}))
	}
	// Keep the seek bar / time counter smooth while audio is playing, even
	// when the visualizer driver wants a slow cadence (VisNone, classic peak
	// idle, etc.). Overlays, paused, and stopped playback keep the slower
	// cadence to save CPU.
	if !m.isOverlayActive() && !m.buffering && m.player != nil &&
		m.player.IsPlaying() && !m.player.IsPaused() {
		if m.lowPower {
			return ui.TickLowPowerPlaying
		}
		return ui.TickFast
	}
	// Paused visualizer content still easing to rest: run at the fast cadence
	// so the bars fall smoothly instead of in ~5 fps steps, then drop to idle
	// once the content has settled.
	if m.visualizerSettlingPaused() {
		return ui.TickFast
	}
	return max(d, ui.TickFast)
}

// isFullyIdle reports whether the model has nothing changing on its own.
// When true, the tick can run at ui.TickIdle since any state change will
// arrive as an explicit message (key press, IPC, MPRIS, plugin send).
func (m *Model) isFullyIdle() bool {
	if m.player == nil {
		return false
	}
	if m.player.IsPlaying() && !m.player.IsPaused() {
		return false
	}
	if m.visualizerSettlingPaused() {
		return false
	}
	if m.isOverlayActive() || m.buffering || m.termTitle.introActive {
		return false
	}
	if !m.status.expiresAt.IsZero() || len(m.logLines) > 0 {
		return false
	}
	if !m.reconnect.at.IsZero() {
		return false
	}
	return true
}

func (m *Model) tickVisualizer(now time.Time) {
	if m.vis == nil {
		return
	}
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}
	m.vis.Tick(m.visualizerTickContext(now))
}

func (m Model) refreshVisualizerIfPending() {
	if m.vis == nil {
		return
	}
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}
	if !m.vis.ConsumeRefresh() {
		return
	}
	m.tickVisualizer(time.Now())
}

func (m Model) maybeRequestVisualizerRefresh(msg tea.Msg, wasScreen topLevelScreen, wasVisible bool, wasMode ui.VisMode, wasPlaying, wasPaused bool) {
	if m.vis == nil {
		return
	}
	if _, ok := msg.(tickMsg); ok {
		return
	}
	screen := m.activeScreen()
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}

	playing := false
	paused := false
	if m.player != nil {
		playing = m.player.IsPlaying()
		paused = m.player.IsPaused()
	}
	if paused {
		m.vis.Suspend()
		return
	}

	if !wasVisible ||
		wasScreen != screen ||
		wasMode != m.vis.Mode ||
		(!wasPlaying && playing) ||
		(wasPaused && !paused) {
		m.vis.RequestRefresh()
	}
}
