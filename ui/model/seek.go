package model

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/ui"
)

// seekDebounceTicks is how many ticks to wait after the last seek keypress
// before actually executing the yt-dlp seek (restart).
const seekDebounceTicks = 8 // ~800ms at 100ms tick interval

// seekTickMsg fires when the async seek completes.
type seekTickMsg struct{}

// doSeek handles a seek keypress. For yt-dlp streams, accumulates into a
// single target position and debounces. For HTTP seekable streams, dispatches
// the seek asynchronously to avoid blocking the UI. For local files, seeks
// immediately.
func (m *Model) doSeek(d time.Duration) tea.Cmd {
	return m.seekRelative(d, seekDebounceTicks)
}

func (m *Model) streamSeekRelative(delta time.Duration) tea.Cmd {
	p := m.player
	return func() tea.Msg {
		p.Seek(delta)
		return seekTickMsg{}
	}
}

func (m *Model) streamSeekAbsolute(target time.Duration) tea.Cmd {
	p := m.player
	return func() tea.Msg {
		p.Seek(target - p.Position())
		return seekTickMsg{}
	}
}

func (m *Model) seekRelative(d time.Duration, debounceTicks int) tea.Cmd {
	if m.player.IsStreamSeek() {
		return m.streamSeekRelative(d)
	}
	if !m.player.IsYTDLSeek() {
		m.player.Seek(d)
		m.finishSeek()
		return nil
	}

	target := m.player.Position()
	if m.seek.active && debounceTicks > 0 {
		target = m.seek.targetPos
	}
	return m.queueYTDLSeekTarget(target+d, debounceTicks)
}

func (m *Model) seekAbsolute(target time.Duration) tea.Cmd {
	if m.player.IsStreamSeek() {
		return m.streamSeekAbsolute(target)
	}
	if !m.player.IsYTDLSeek() {
		m.player.Seek(target - m.player.Position())
		m.finishSeek()
		return nil
	}
	return m.queueYTDLSeekTarget(target, 0)
}

func (m *Model) queueYTDLSeekTarget(target time.Duration, debounceTicks int) tea.Cmd {
	m.seek.active = true
	m.seek.targetPos = m.clampPosition(target)

	m.player.CancelSeekYTDL()

	if debounceTicks > 0 {
		m.seek.timer = debounceTicks
		m.seek.timerFor = 0
		return nil
	}

	m.seek.timer = 0
	m.seek.timerFor = 0
	return m.commitPendingYTDLSeek()
}

func (m *Model) finishSeek() {
	m.notifyAll()
	if m.notifier != nil {
		m.notifier.Seeked(m.player.Position())
	}
}

func (m *Model) commitPendingYTDLSeek() tea.Cmd {
	target := m.seek.targetPos
	curPos := m.player.Position()
	d := target - curPos

	p := m.player
	return func() tea.Msg {
		p.SeekYTDL(d)
		return seekTickMsg{}
	}
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
	if advanceTickUnits(&m.seek.timer, &m.seek.timerFor, dt, ui.TickFast) == 0 || m.seek.timer > 0 {
		return nil
	}

	// Timer expired — fire the seek to the target position.
	return m.commitPendingYTDLSeek()
}
