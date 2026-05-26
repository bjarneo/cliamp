package model

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/ui"
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

func (m *Model) visualizerPlaying() bool {
	return m.player != nil && m.vis != nil && m.vis.Mode != ui.VisNone &&
		!m.isOverlayActive() && m.player.IsPlaying() && !m.player.IsPaused()
}

func (m *Model) visualizerPaused() bool {
	return m.player != nil && m.vis != nil && m.vis.Mode != ui.VisNone &&
		!m.isOverlayActive() && m.player.IsPlaying() && m.player.IsPaused()
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
		Analyze: func(spec ui.VisAnalysisSpec) []float64 {
			spec = ui.NormalizeAnalysisSpec(spec)
			if m.player == nil || m.vis == nil || m.vis.Mode == ui.VisNone {
				return nil
			}
			if bands, ok := cache[spec]; ok {
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
	if m.vis != nil {
		d = m.vis.TickInterval(m.visualizerTickContext(time.Time{}))
	}
	// Keep the seek bar / time counter smooth while audio is playing, even
	// when the visualizer driver wants a slow cadence (VisNone, classic peak
	// idle, etc.). Overlays, paused, and stopped playback keep the slower
	// cadence to save CPU.
	if !m.isOverlayActive() && !m.buffering && m.player != nil &&
		m.player.IsPlaying() && !m.player.IsPaused() && d > ui.TickFast {
		if m.lowPower {
			return ui.TickLowPowerPlaying
		}
		d = ui.TickFast
	}
	return d
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
	if m.vis == nil || m.vis.Mode == ui.VisNone {
		return
	}
	m.vis.Tick(m.visualizerTickContext(now))
}

func (m Model) refreshVisualizerIfPending() {
	if m.vis == nil || m.vis.Mode == ui.VisNone || m.activeScreen().hidesVisualizer() || !m.vis.ConsumeRefresh() {
		return
	}
	m.tickVisualizer(time.Now())
}

func (m Model) maybeRequestVisualizerRefresh(msg tea.Msg, wasScreen topLevelScreen, wasMode ui.VisMode, wasPlaying, wasPaused bool) {
	if m.vis == nil {
		return
	}
	if _, ok := msg.(tickMsg); ok {
		return
	}
	screen := m.activeScreen()
	if screen.hidesVisualizer() || m.vis.Mode == ui.VisNone {
		return
	}

	playing := false
	paused := false
	if m.player != nil {
		playing = m.player.IsPlaying()
		paused = m.player.IsPaused()
	}

	if wasScreen != screen ||
		wasMode != m.vis.Mode ||
		(!wasPlaying && playing) ||
		(wasPaused && !paused) {
		m.vis.RequestRefresh()
	}
}
