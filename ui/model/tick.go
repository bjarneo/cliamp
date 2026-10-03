package model

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type tickMsg time.Time
type autoPlayMsg struct{}

// spinnerTickMsg redraws the view so that a loading spinner advances. It runs
// beside the main tick, which can still wait up to ui.TickIdle when a load
// starts.
type spinnerTickMsg struct{}

func spinnerTickCmd() tea.Cmd {
	return teaTick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

var teaTick = tea.Tick

func tickCmd() tea.Cmd {
	return tickCmdAt(ui.TickFast)
}

// tickCmdAt schedules the next tick. Each tick schedules its successor, so
// exactly one of these may be in flight: a second chain would never end.
func tickCmdAt(d time.Duration) tea.Cmd {
	return teaTick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// visualizerVisible reports whether the visualizer has a place to draw and
// somebody to draw it for.
func (m Model) visualizerVisible() bool {
	if m.detached {
		return false
	}
	if m.simplified || m.vis == nil || m.vis.Mode == ui.VisNone || m.vis.Rows <= 0 || m.vis.Cols <= 0 || m.layout.tooSmall() {
		return false
	}
	if m.fullVis {
		return true
	}
	return m.layout.visualizerRows > 0 && !m.usesContentFirstLayout()
}

func (m *Model) visualizerPlaying() bool {
	return m.player != nil && m.visualizerVisible() &&
		m.player.IsPlaying() && !m.player.IsPaused()
}

func (m *Model) visualizerPaused() bool {
	return m.player != nil && m.visualizerVisible() &&
		m.player.IsPlaying() && m.player.IsPaused()
}

// visualizerSettlingPaused reports whether playback is paused and the
// visualizer still has spectrum content easing to zero. While true the tick
// stays above the idle cadence so the bars fall; once settled the model can
// return to the fully-idle cadence.
func (m *Model) visualizerSettlingPaused() bool {
	if m.player == nil || !m.player.IsPaused() {
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
		Now:     now,
		Playing: m.visualizerPlaying(),
		Paused:  m.visualizerPaused(),
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
				if spec.Tap == ui.VisTapAudible {
					samplesRead = m.player.WaveformSamplesInto(buf)
				} else {
					samplesRead = m.player.SamplesInto(buf)
				}
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

// tickInterval picks the cadence of the next tick from what the model has
// to keep up with: animation, playback bookkeeping, or nothing at all.
func (m *Model) tickInterval() time.Duration {
	// A detached session renders into a stream nobody reads, so no cadence
	// above the one playback bookkeeping needs buys anything.
	if m.detached {
		if m.isFullyIdle() {
			return ui.TickIdle
		}
		return ui.TickDetached
	}
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
	if m.spinnerVisible() {
		d = min(d, spinnerInterval)
	}
	// Keep the seek bar / time counter smooth while audio is playing, even
	// when the visualizer driver wants a slow cadence (VisNone, classic peak
	// idle, etc.). Paused and stopped playback keep the slower cadence to
	// save CPU.
	if !m.buffering && m.player != nil &&
		m.player.IsPlaying() && !m.player.IsPaused() {
		if m.lowPower {
			return ui.TickLowPowerPlaying
		}
		if m.visualizerVisible() && (m.visualizer60FPS || m.vis.UsesRawSamples()) {
			return ui.TickAnim
		}
		if m.visualizerVisible() && m.vis.DriverOwnsCadence() {
			return min(d, ui.TickFast)
		}
		return ui.TickFast
	}
	// Paused visualizer content still easing to rest: run at the fast cadence
	// so the bars fall smoothly instead of in ~5 fps steps, then drop to idle
	// once the content has settled.
	if m.visualizerSettlingPaused() {
		if m.visualizer60FPS {
			return ui.TickAnim
		}
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
	if m.buffering || m.termTitle.introActive || m.spinnerVisible() {
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

// spinnerVisible reports whether a loading spinner is on the screen. The tick
// then redraws at least every spinnerInterval so that the frames advance.
func (m *Model) spinnerVisible() bool {
	return m.provPane.loading || m.provSearch.loading || m.catalogBatch.loading || m.feedLoading ||
		(m.lyrics.visible && m.lyrics.loading) ||
		(m.netSearch.active && m.netSearch.loading) ||
		(m.searchOverlay.visible && (m.searchOverlay.loading || m.searchOverlay.albumLoading)) ||
		(m.navBrowser.visible && (m.navBrowser.loading || m.navBrowser.albumLoading)) ||
		(m.devicePicker.visible && m.devicePicker.loading) ||
		(m.subs.visible && m.subs.loading)
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

// handleTick samples the player, runs the timed jobs, advances past a
// finished track and schedules the next tick.
func (m *Model) handleTick(msg tickMsg) tea.Cmd {
	now := time.Time(msg)
	dt := m.tickDelta(now)

	m.tickSampleClock()
	m.tickVisualizer(now)
	m.tickProgressReport(now)
	var cmds []tea.Cmd
	// Process debounced yt-dlp seek.
	if cmd := m.tickSeek(dt); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.tickExpire(now, dt)
	m.tickStreamHealth(now)
	if cmd := m.tickStreamTitle(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.tickNetwork(dt)
	if playCmd, restarted := m.tickReconnect(now); restarted {
		// Preserve any seek/lyric commands already queued this tick
		// rather than dropping them on the early return.
		return tea.Batch(append(cmds, playCmd, tickCmdAt(ui.TickFast))...)
	}
	// Check gapless transition (audio already playing next track)
	gaplessAdvanced := m.player.GaplessAdvanced()
	if gaplessAdvanced {
		gaplessCmds, playing := m.tickGapless()
		if !playing {
			return tea.Batch(append(cmds, tickCmdAt(m.tickInterval()))...)
		}
		cmds = append(cmds, gaplessCmds...)
	}
	m.tickResumeSave(now)
	if !gaplessAdvanced {
		cmds = append(cmds, m.tickDrain(now))
	}
	m.advanceTitleScroll(now)
	cmds = append(cmds, m.tickPreloadRetry())
	m.advanceTerminalTitle()
	cmds = append(cmds, tickCmdAt(m.tickInterval()))
	return tea.Batch(cmds...)
}

// playbackClock returns the position and duration of the track that plays.
// While a track buffers, the engine still holds the old pipeline, so the
// position is 0 and the duration comes from the track metadata. The TUI
// clock, the media controls and the runtime state all use this rule.
func (m *Model) playbackClock() (time.Duration, time.Duration) {
	if m.buffering {
		track, _ := m.currentPlaybackTrack()
		return 0, time.Duration(track.DurationSecs) * time.Second
	}
	return m.player.PositionAndDuration()
}

// tickSampleClock caches the position and duration once per tick, so that
// the View render functions do not take speaker.Lock() several times.
// PositionAndDuration() batches both reads under one speaker lock.
func (m *Model) tickSampleClock() {
	if m.buffering {
		m.cachedPos, m.cachedDur = m.playbackClock()
		return
	}
	if m.seek.active {
		m.cachedPos = m.seek.targetPos
		m.cachedDur = m.player.Duration()
		return
	}
	m.cachedPos, m.cachedDur = m.player.PositionAndDuration()
	// Piped SSH streams report 0 duration — use metadata fallback.
	if m.cachedDur == 0 {
		if track, _ := m.currentPlaybackTrack(); track.DurationSecs > 0 && strings.HasPrefix(track.Path, "ssh://") {
			m.cachedDur = time.Duration(track.DurationSecs) * time.Second
		}
	}
}

// tickExpire ends the timed states: the status message, old log lines, the
// debounced speed and EQ saves, a pending jump seek and the seek grace.
func (m *Model) tickExpire(now time.Time, dt time.Duration) {
	// Expire temporary status messages.
	wasStatus := m.status.text != ""
	if !m.status.expiresAt.IsZero() && !now.Before(m.status.expiresAt) {
		m.status.Clear()
	}
	// Drain app log buffer and expire old entries.
	wasLogs := len(m.logLines)
	m.tickLogLines(now)
	if (wasStatus && m.status.text == "") || len(m.logLines) != wasLogs {
		m.applyHeightMode()
		m.adjustScroll()
	}
	m.tickPendingSpeedSave(dt)
	m.tickPendingEQSave(dt)
	if m.pendingSeekActive && !m.pendingSeekExpiresAt.IsZero() && !now.Before(m.pendingSeekExpiresAt) {
		m.pendingSeekActive = false
		m.pendingSeekExpiresAt = time.Time{}
	}
	// Decrement seek grace period.
	advanceTickUnits(&m.seek.grace, &m.seek.graceFor, dt, ui.TickFast)
}

// tickStreamHealth surfaces stream errors, such as a dropped connection, and
// schedules a reconnect for streams. It stays quiet during a yt-dlp seek and
// its grace period, because killing the old pipeline triggers a transient
// error that can persist for a few ticks.
func (m *Model) tickStreamHealth(now time.Time) {
	err := m.player.StreamErr()
	if err == nil || m.seek.active || m.seek.grace != 0 {
		return
	}
	track, idx := m.currentPlaybackTrack()
	isStream := idx >= 0 && (track.Stream || playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path))
	if isStream && m.reconnect.attempts < 5 {
		m.scheduleReconnect(now)
	} else {
		m.err = err
		m.reconnect.at = time.Time{}
	}
}

// tickStreamTitle polls the ICY stream title for live radio display. When
// the song changes while the lyrics overlay is open, it returns the command
// that fetches the new lyrics.
func (m *Model) tickStreamTitle() tea.Cmd {
	title := m.player.StreamTitle()
	if title == "" || title == m.streamTitle {
		return nil
	}
	m.streamTitle = title
	m.resetTitleScroll()
	m.applyHeightMode()
	m.adjustScroll()
	if !m.lyrics.visible || m.lyrics.loading {
		return nil
	}
	artist, song, ok := splitStreamTitle(title)
	if !ok {
		return nil
	}
	track, _ := m.currentPlaybackTrack()
	q := lyricsLookupKey(track, artist, song)
	if q == m.lyrics.query {
		return nil
	}
	m.lyrics.query = q
	m.lyrics.loading = true
	m.lyrics.lines = nil
	m.lyrics.err = nil
	m.lyrics.scroll = 0
	return m.fetchLyricsForTrack(track, artist, song)
}

// tickNetwork updates the average download rate once per second.
func (m *Model) tickNetwork(dt time.Duration) {
	m.network.sampleFor += dt
	if m.network.sampleFor < time.Second {
		return
	}
	downloaded, _ := m.player.StreamBytes()
	delta := downloaded - m.network.lastBytes
	if delta > 0 {
		// Exponential moving average for smooth display.
		instant := float64(delta) / m.network.sampleFor.Seconds() // bytes/sec
		if m.network.speed == 0 {
			m.network.speed = instant
		} else {
			m.network.speed = m.network.speed*0.6 + instant*0.4
		}
	} else if downloaded == 0 {
		m.network.speed = 0
	}
	m.network.lastBytes = downloaded
	m.network.sampleFor = 0
}

// tickReconnect restarts the current track when the scheduled reconnect is
// due. It returns true when it restarted a track, and the tick then ends.
func (m *Model) tickReconnect(now time.Time) (tea.Cmd, bool) {
	if m.reconnect.at.IsZero() || !now.After(m.reconnect.at) {
		return nil, false
	}
	m.reconnect.at = time.Time{}
	track, idx := m.currentPlaybackTrack()
	m.player.Stop()
	if idx < 0 {
		return nil, false
	}
	// playTrack resets reconnect state for every new start, so carry
	// the live-drain marker and its attempt count across this restart.
	ytdlLiveDrain, attempts := m.reconnect.ytdlLiveDrain, m.reconnect.attempts
	playCmd := m.playTrack(track)
	if ytdlLiveDrain {
		m.reconnect.ytdlLiveDrain, m.reconnect.attempts = true, attempts
	}
	return playCmd, true
}

// tickGapless follows a gapless switch that the player already made to the
// preloaded track. It reports the finished track, moves the playlist on and
// arms the preload after the new track. playing is false when the queue
// ended instead.
func (m *Model) tickGapless() (cmds []tea.Cmd, playing bool) {
	// Leave the track that just finished before advancing the playlist.
	// For gapless, the track played fully (100% ≥ 50%), so elapsed = duration.
	// The player stashed the finished pipeline's real duration at swap
	// time; metadata is only a fallback for tracks without it.
	finishedTrack, _ := m.currentPlaybackTrack()
	fullDur := m.player.LastPlayedDuration()
	if fullDur <= 0 {
		fullDur = time.Duration(finishedTrack.DurationSecs) * time.Second
	}
	m.leaveTrack(fullDur, fullDur)

	newTrack, ok := m.advanceToNext()
	if !ok {
		return nil, false
	}
	newTrack, lyricCmd := m.beginPlaybackTrack(newTrack)
	// The preload that just fired is consumed — clear the in-flight flag
	// so the next track can be preloaded.
	m.preloading = false
	// A stream decoder error at the track boundary (e.g., server closing
	// the connection when the preload HTTP request opens) is expected and
	// not a user-visible problem. Clear any pending error so the red
	// message doesn't flash at every track transition.
	m.err = nil
	// Gapless advances without calling playTrack(), so emit now-playing here.
	m.nowPlaying(newTrack)
	return []tea.Cmd{lyricCmd, m.preloadNext()}, true
}

// tickDrain handles a track that played to its end with no preloaded next
// track. A live stream reconnects, and any other track advances. It skips
// while a yt-dlp download still buffers, so that the playlist does not
// advance on every tick while the resolve runs.
func (m *Model) tickDrain(now time.Time) tea.Cmd {
	if !m.player.IsPlaying() || m.player.IsPaused() || !m.player.Drained() || m.buffering || !m.reconnect.at.IsZero() {
		return nil
	}
	finishedTrack, idx := m.currentPlaybackTrack()
	if idx >= 0 && m.currentPlaybackIsLive(finishedTrack) {
		// A live stream has no natural end. A clean decoder EOF is a
		// disconnect, so retry this station instead of advancing.
		m.scheduleReconnect(now)
		m.reconnect.ytdlLiveDrain = playlist.IsYTDL(finishedTrack.Path)
		return nil
	}
	// Track drained to end — always ≥ 50%. The player is still on
	// the finished track here, so its live duration is authoritative
	// even when playlist metadata (DurationSecs) is unknown.
	drainDur := m.player.Duration()
	if drainDur <= 0 {
		drainDur = time.Duration(finishedTrack.DurationSecs) * time.Second
	}
	m.leaveTrack(drainDur, drainDur)

	// Stop the player before dispatching the async nextTrack command.
	// This clears the gapless streamer so the finished track cannot
	// replay while waiting for a yt-dlp pipe chain to spin up.
	m.player.Stop()
	return m.nextTrack()
}

// tickPreloadRetry retries a deferred stream preload. preloadNext() returns
// nil (defers) when the current stream has >streamPreloadLeadTime remaining,
// so the tick polls until we're within the window and the preload gets armed.
// It waits while m.preloading is set, so that a second concurrent HTTP
// connection does not start while the first preloadStreamCmd goroutine is
// still running. It also waits while m.tracksPaging is set, because each
// page of a paged load remixes the upcoming order, so anything armed now
// would be stale by the next one.
func (m *Model) tickPreloadRetry() tea.Cmd {
	if !m.player.IsPlaying() || m.player.IsPaused() || m.buffering || m.preloading || m.tracksPaging || m.player.HasPreload() {
		return nil
	}
	return m.preloadNext()
}
