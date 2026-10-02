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

// ytdlUnpauseReconnectMsg reports a yt-dlp reconnect on unpause. gen holds
// the stream generation and seekGen holds the seek generation at its start.
type ytdlUnpauseReconnectMsg struct {
	err     error
	gen     uint64
	seekGen uint64
}

// doSeek handles a seek keypress. Seeks that restart a decoder accumulate into
// one target and debounce; local files seek immediately.
func (m *Model) doSeek(d time.Duration) tea.Cmd {
	return m.seekRelative(d, seekDebounceTicks)
}

func (m *Model) seekRelative(d time.Duration, debounceTicks int) tea.Cmd {
	if !m.needsDebouncedSeek() {
		m.player.Seek(d)
		m.finishSeek()
		return nil
	}

	// A pending or running seek has not moved Position yet, so build on its
	// target or back-to-back relative seeks overwrite each other.
	target := m.player.Position()
	if m.seek.active {
		target = m.seek.targetPos
	}
	return m.queueSeekTarget(target+d, debounceTicks)
}

// needsDebouncedSeek reports whether seeking restarts a decoder, making a burst
// of keypresses worth summing into one. A provider URI that the player
// resolves at play time is a stream, also when an older favorites, history
// or playlist file reloaded it without the stream flag.
func (m *Model) needsDebouncedSeek() bool {
	if m.player.IsYTDLSeek() {
		return true
	}
	track, _ := m.currentPlaybackTrack()
	return (track.Stream || m.hasSourceResolver(track.Path)) && m.player.Seekable()
}

func (m *Model) seekAbsolute(target time.Duration) tea.Cmd {
	cmd, _ := m.trySeekAbsolute(target)
	return cmd
}

// trySeekAbsolute is seekAbsolute that also returns the error of an in-place
// seek. A seek that restarts a decoder runs in the returned Cmd and reports
// its error through seekTickMsg.
func (m *Model) trySeekAbsolute(target time.Duration) (tea.Cmd, error) {
	if !m.needsDebouncedSeek() {
		if err := m.player.Seek(target - m.player.Position()); err != nil {
			return nil, err
		}
		m.finishSeek()
		return nil, nil
	}
	return m.queueSeekTarget(target, 0), nil
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

// finishSeek tells the media controls and plugins that a seek landed. The
// media controls get the new state before the MPRIS Seeked signal, also for
// a seek within the same second, so a client that reads Position on the
// signal sees the new value. A rewind with previous that lands reports the
// play before it and starts a replay, which can scrobble again.
func (m *Model) finishSeek() {
	if m.seek.rewind {
		m.seek.rewind = false
		m.leaveTrack(m.seek.rewindAt, m.seek.rewindDur)
		m.playingTrackLeft = false
	}
	m.notice.sent = false
	m.notifyPlaybackChange()
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

// resetSeek drops a seek that waits for its debounce or still runs, for a
// new track or a stop. The seek generation moves on, so a seek that lands
// later is ignored.
func (m *Model) resetSeek() {
	m.seek.active = false
	m.seek.inFlight = false
	m.seek.pending = false
	m.seek.gen++
	m.seek.timer = 0
	m.seek.timerFor = 0
	m.seek.grace = 0
	m.seek.graceFor = 0
	m.seek.rewind = false
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
