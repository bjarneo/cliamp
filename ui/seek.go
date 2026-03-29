package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// seekDebounceTicks is how many ticks to wait after the last seek keypress
// before actually executing the yt-dlp seek (restart).
const seekDebounceTicks = 8 // ~800ms at 100ms tick interval

// seekTickMsg fires when the async seek completes.
type seekTickMsg struct{}

// doSeek handles a seek keypress. For yt-dlp streams, accumulates into a
// single target position and debounces. For local files, seeks immediately.
func (m *Model) doSeek(d time.Duration) tea.Cmd {
	if !m.player.IsYTDLSeek() {
		// Local/HTTP seek: immediate.
		m.player.Seek(d)
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return nil
	}

	// First press in a new seek sequence: snapshot the starting position.
	if !m.seek.active {
		m.seek.active = true
		m.seek.targetPos = m.player.Position()
	}

	// Accumulate into absolute target position.
	m.seek.targetPos += d
	m.seek.targetPos = m.clampPosition(m.seek.targetPos)

	// Reset debounce timer.
	m.seek.timer = seekDebounceTicks
	m.seek.timerFor = 0

	// Cancel any in-flight seek so it won't swap stale audio.
	m.player.CancelSeekYTDL()

	return nil
}

// displayPosition returns the position to show in the UI.
func (m *Model) displayPosition() time.Duration {
	if m.seek.active {
		return m.seek.targetPos
	}
	return m.player.Position()
}

func (m *Model) clampPosition(pos time.Duration) time.Duration {
	if pos < 0 {
		return 0
	}
	dur := m.player.Duration()
	if dur > 0 && pos >= dur {
		return dur - time.Second
	}
	return pos
}

// tickSeek is called from the main tick loop. It advances the debounce timer with elapsed
// time and runs the yt-dlp seek when the countdown reaches zero.
func (m *Model) tickSeek(dt time.Duration) tea.Cmd {
	if !m.seek.active || m.seek.timer <= 0 {
		m.seek.timerFor = 0
		return nil
	}
	if advanceTickUnits(&m.seek.timer, &m.seek.timerFor, dt, tickFast) == 0 || m.seek.timer > 0 {
		return nil
	}

	// Timer expired — fire the seek to the target position.
	// Compute delta from current actual position.
	target := m.seek.targetPos
	curPos := m.player.Position()
	d := target - curPos

	// Cancel any previous in-flight seek.
	p := m.player
	p.CancelSeekYTDL()

	return func() tea.Msg {
		p.SeekYTDL(d)
		return seekTickMsg{}
	}
}
