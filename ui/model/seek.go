package model

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/ui"
)

// seekDebounceTicks is how many ticks to wait after the last seek keypress
// before actually executing the yt-dlp seek (restart).
const seekDebounceTicks = 8 // ~800ms at 100ms tick interval

// seekTickMsg fires when an async seek completes.
type seekTickMsg struct {
	err    error
	resume bool
	target time.Duration
	gen    uint64
}

type ytdlUnpauseReconnectMsg struct{ err error }

// doSeek handles a seek keypress. Seeks that restart a decoder accumulate into
// one target and debounce; local files seek immediately.
func (m *Model) doSeek(d time.Duration) tea.Cmd {
	return m.seekRelative(d, seekDebounceTicks)
}

func (m *Model) streamSeekRelative(delta time.Duration) tea.Cmd {
	p := m.player
	gen := m.seek.gen
	return func() tea.Msg {
		err := p.Seek(delta)
		return seekTickMsg{err: err, gen: gen}
	}
}

func (m *Model) streamSeekAbsolute(target time.Duration) tea.Cmd {
	p := m.player
	gen := m.seek.gen
	return func() tea.Msg {
		err := p.Seek(target - p.Position())
		return seekTickMsg{err: err, target: target, gen: gen}
	}
}

func (m *Model) seekRelative(d time.Duration, debounceTicks int) tea.Cmd {
	if m.player.IsStreamSeek() {
		return m.streamSeekRelative(d)
	}
	if !m.needsDebouncedSeek() {
		m.player.Seek(d)
		m.finishSeek()
		return nil
	}

	target := m.player.Position()
	if m.seek.active && debounceTicks > 0 {
		target = m.seek.targetPos
	}
	return m.queueSeekTarget(target+d, debounceTicks)
}

// needsDebouncedSeek reports whether seeking restarts a decoder, making a burst
// of keypresses worth summing into one.
func (m *Model) needsDebouncedSeek() bool {
	if m.player.IsYTDLSeek() {
		return true
	}
	track, _ := m.currentPlaybackTrack()
	return track.Stream && m.player.Seekable()
}

func (m *Model) seekAbsolute(target time.Duration) tea.Cmd {
	if m.player.IsStreamSeek() {
		return m.streamSeekAbsolute(target)
	}
	if !m.needsDebouncedSeek() {
		m.player.Seek(target - m.player.Position())
		m.finishSeek()
		return nil
	}
	return m.queueSeekTarget(target, 0)
}

func (m *Model) queueSeekTarget(target time.Duration, debounceTicks int) tea.Cmd {
	m.seek.active = true
	m.seek.targetPos = m.clampPosition(target)

	if m.player.IsYTDLSeek() {
		m.player.CancelSeekYTDL()
	}

	if debounceTicks > 0 {
		m.seek.timer = debounceTicks
		m.seek.timerFor = 0
		return nil
	}

	m.seek.timer = 0
	m.seek.timerFor = 0
	return m.commitPendingSeek()
}

func (m *Model) finishSeek() {
	m.notifyAll()
	if m.notifier != nil {
		m.notifier.Seeked(m.player.Position())
	}
	m.emitPlugin(luaplugin.EventPlayerSeek, map[string]any{
		"position": m.player.Position().Seconds(),
		"duration": m.player.Duration().Seconds(),
	})
}

// commitPendingSeek starts the seek to the pending target. Only one such seek
// runs at a time: overlapping decoder restarts prepare their replacements
// against the same pipeline state, so the first to commit makes the others
// no-ops and the newest target would be silently lost. A target that arrives
// while a seek runs is committed when that seek reports back.
func (m *Model) commitPendingSeek() tea.Cmd {
	if m.seek.inFlight {
		m.seek.pending = true
		return nil
	}
	m.seek.inFlight = true
	m.seek.pending = false
	return m.seekCmd(m.seek.targetPos, false)
}

// seekCmd performs the decoder-restarting seek to target asynchronously.
// Position and pipeline kind are read inside the command so playback progress
// and track changes between queueing and execution are respected.
func (m *Model) seekCmd(target time.Duration, resume bool) tea.Cmd {
	p := m.player
	gen := m.seek.gen
	return func() tea.Msg {
		var err error
		if p.IsYTDLSeek() {
			err = p.SeekYTDL(target - p.Position())
		} else {
			err = p.Seek(target - p.Position())
		}
		return seekTickMsg{err: err, resume: resume, target: target, gen: gen}
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
	return m.commitPendingSeek()
}
