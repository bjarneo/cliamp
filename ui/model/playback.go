package model

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	ytdlReconnectPauseThreshold = 45 * time.Second
	resumeSaveInterval          = 2 * time.Second
)

func (m *Model) replacePlaylist(tracks []playlist.Track) {
	if m.resumeSaver != nil {
		tracks = playlist.WithPlaybackContext(tracks)
	}
	m.playlist.Replace(tracks)
	m.normalizeQueueOverlay()
}

func trackIndexByPath(tracks []playlist.Track, path string) int {
	for i, track := range tracks {
		if track.Path == path {
			return i
		}
	}
	return -1
}

func (m *Model) setPlaybackContext(tracks []playlist.Track, index int) {
	m.playbackContext = cloneTracks(tracks)
	m.playbackContextIndex = index
}

func (m *Model) playbackContextFor(track playlist.Track) ([]playlist.Track, int) {
	if context, index := track.PlaybackContext(); index >= 0 {
		return context, index
	}
	context := m.playbackContext
	index := m.playbackContextIndex
	if index >= 0 && index < len(context) && context[index].Path == track.Path {
		return context, index
	}
	if m.playlist != nil {
		context = m.playlist.Tracks()
		index = m.playlist.Index()
		if index >= 0 && index < len(context) && context[index].Path == track.Path {
			return context, index
		}
	}
	// Path lookup is only a fallback when the source entry's index is unknown.
	if index := trackIndexByPath(m.playbackContext, track.Path); index >= 0 {
		return m.playbackContext, index
	}
	return context, trackIndexByPath(context, track.Path)
}

func (m *Model) persistPlaybackContext(track playlist.Track, positionSec int, now time.Time) {
	if m.resumeSaver == nil {
		return
	}
	context, index := m.playbackContextFor(track)
	if index < 0 {
		return
	}
	m.resumeSaver(track, positionSec, cloneTracks(context), index)
	m.lastResumeSave = now
}

func (m *Model) tickResumeSave(now time.Time) {
	if m.resumeSaver == nil || m.player == nil || !m.player.IsPlaying() {
		return
	}
	if m.seek.active || m.seek.inFlight || m.seek.pending {
		return
	}
	if !m.lastResumeSave.IsZero() && now.Sub(m.lastResumeSave) < resumeSaveInterval {
		return
	}
	track, stats, current := m.playbackSnapshot()
	if !current || !stats.Playing {
		return
	}
	// cachedPos can still contain a seek preview rather than decoder progress.
	m.persistPlaybackContext(track, max(0, int(stats.Position.Seconds())), now)
}

// nextTrack advances to the next playlist track and starts playing it.
// Unplayable tracks are skipped automatically.
func (m *Model) nextTrack() tea.Cmd {
	m.clearPreload()
	if m.playbackDetached {
		m.playbackDetached = false
		if m.playlist.Len() == 0 {
			m.stopPlayback()
			return nil
		}
		return m.playCurrentTrack()
	}
	track, ok := m.playlist.Next()
	m.normalizeQueueOverlay()
	if !ok {
		m.stopPlayback()
		return nil
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return m.playTrack(track)
}

// prevTrack goes to the previous track, or restarts if >3s into the current one.
// Unplayable tracks are skipped automatically.
func (m *Model) prevTrack() tea.Cmd {
	m.clearPreload()
	if m.player.Position() > 3*time.Second {
		if m.player.Seekable() {
			// Seekable media rewinds in place; non-seekable streams must be restarted.
			m.player.Seek(m.playbackTicket(), -m.player.Position())
			return nil
		}
		// The position belongs to the audible source, so restart that one rather
		// than a replacement that is still loading.
		track, idx := m.activePlaybackTrack()
		if idx >= 0 {
			return m.playTrack(track)
		}
		return nil
	}
	track, ok := m.playlist.Prev()
	if !ok {
		return nil
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return m.playTrack(track)
}

// playCurrentLogicalTrack starts playback from the playlist's active logical
// track, preserving queued playback state.
func (m *Model) playCurrentLogicalTrack() tea.Cmd {
	m.clearPreload()
	track, idx := m.playlist.Current()
	if idx < 0 {
		return nil
	}
	m.resetTitleScroll()
	m.plCursor = idx
	m.adjustScroll()
	return m.playTrack(track)
}

// playCurrentTrack starts playing the selected track, skipping forward in
// playlist order if the selection is unplayable.
func (m *Model) playCurrentTrack() tea.Cmd {
	m.clearPreload()
	m.resetTitleScroll()
	if m.playlist.Len() == 0 {
		return nil
	}
	activation, ok := m.playlist.ActivateSelected()
	if !ok {
		m.stopPlayback()
		m.status.Warning("No available tracks", statusTTLDefault)
		return nil
	}
	if activation.Skipped {
		m.status.Warning("Track unavailable, skipping...", statusTTLDefault)
	}
	m.plCursor = activation.Index
	m.adjustScroll()
	return m.playTrack(activation.Track)
}

// playTrackImmediate appends a track to the playlist and starts playing it now,
// stopping any current playback. Used by search-result "Play now" actions.
func (m *Model) playTrackImmediate(track playlist.Track) tea.Cmd {
	m.clearPreload()
	m.playlist.Add(track)
	m.loadedPlaylist = ""
	m.addToHeaderState([]playlist.Track{track})
	idx := m.playlist.Len() - 1
	m.selectPlaybackIndex(idx)
	m.plCursor = idx
	m.adjustScroll()
	m.status.Showf(statusTTLMedium, "Playing: %s", track.DisplayName())
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// appendTrack appends a track to the playlist; auto-plays if nothing is playing.
func (m *Model) appendTrack(track playlist.Track) tea.Cmd {
	wasEmpty := m.playlist.Len() == 0
	m.playlist.Add(track)
	m.loadedPlaylist = ""
	m.addToHeaderState([]playlist.Track{track})
	idx := m.playlist.Len() - 1
	m.status.Showf(statusTTLMedium, "Added: %s", track.DisplayName())
	if wasEmpty || !m.player.IsPlaying() {
		m.selectPlaybackIndex(idx)
		m.plCursor = idx
		m.adjustScroll()
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	return nil
}

// playAlbumImmediate appends an expanded album to the queue and starts it at
// its first track. Like playTrackImmediate it adds rather than replaces, so a
// queue built up over an evening survives picking an album from search.
func (m *Model) playAlbumImmediate(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	m.clearPreload()
	idx := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(tracks)
	m.selectPlaybackIndex(idx)
	m.plCursor = idx
	m.adjustScroll()
	m.status.Showf(statusTTLMedium, "Playing album: %s (%d tracks)", album.Title, len(tracks))
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// appendAlbum appends an expanded album to the queue; auto-plays from its first
// track if nothing is playing.
func (m *Model) appendAlbum(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	wasEmpty := m.playlist.Len() == 0
	idx := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(tracks)
	m.status.Showf(statusTTLMedium, "Added album: %s (%d tracks)", album.Title, len(tracks))
	if wasEmpty || !m.player.IsPlaying() {
		m.selectPlaybackIndex(idx)
		m.plCursor = idx
		m.adjustScroll()
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	return nil
}

// queueAlbumNext queues a whole album to play after the current track, keeping
// its running order.
func (m *Model) queueAlbumNext(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	idx := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(tracks)
	for i := range tracks {
		m.playlist.Queue(idx + i)
	}
	m.status.Showf(statusTTLMedium, "Queued album: %s (%d tracks)", album.Title, len(tracks))
	if !m.player.IsPlaying() {
		cmd := m.nextTrack()
		m.notifyPlayback()
		return cmd
	}
	return m.rearmPreload()
}

// closeNetSearch fully resets the net search overlay and restores focus,
// dropping any cached results so they don't linger between sessions.
func (m *Model) closeNetSearch() {
	nextRequest(&m.requests.netSearch)
	m.netSearch = netSearchState{}
	m.focus = m.prevFocus
}

// closeSpotSearch fully resets the Spotify search overlay, dropping cached
// results, playlists, and the selected track.
func (m *Model) closeSpotSearch() {
	m.cancelSpotRequest()
	nextRequest(&m.requests.spotSearch)
	m.invalidateSpotAlbumRequest()
	nextRequest(&m.requests.spotLists)
	nextRequest(&m.requests.spotMutation)
	m.spotSearch = spotSearchState{}
}

func (m *Model) invalidateSpotAlbumRequest() {
	m.cancelSpotRequest()
	nextRequest(&m.requests.spotAlbum)
	m.spotSearch.albumLoading = false
}

func (m *Model) newSpotRequestContext(timeout time.Duration) context.Context {
	m.cancelSpotRequest()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.spotSearch.cancel = cancel
	return ctx
}

func (m *Model) cancelSpotRequest() {
	if m.spotSearch.cancel != nil {
		m.spotSearch.cancel()
		m.spotSearch.cancel = nil
	}
}

// queueTrackNext adds a track to the playlist and queues it to play next.
func (m *Model) queueTrackNext(track playlist.Track) tea.Cmd {
	m.playlist.Add(track)
	m.loadedPlaylist = ""
	m.addToHeaderState([]playlist.Track{track})
	idx := m.playlist.Len() - 1
	m.playlist.Queue(idx)
	m.normalizeQueueOverlay()
	m.status.Showf(statusTTLMedium, "Queued: %s", track.DisplayName())
	if !m.player.IsPlaying() {
		cmd := m.nextTrack()
		m.notifyPlayback()
		return cmd
	}
	return m.rearmPreload()
}

// removeSelectedFromPlaylist removes the track at the current playlist cursor.
// If the active track is removed, playback is stopped; the cursor is clamped
// to the new playlist length.
func (m *Model) removeSelectedFromPlaylist() {
	idx := m.plCursor
	if idx < 0 || idx >= m.playlist.Len() {
		return
	}
	snapshot := m.playlist.Snapshot()
	track, ok := m.playlist.Track(idx)
	if !ok {
		return
	}
	if track.DirSourced {
		m.status.Warningf(statusTTLDefault, "Can't remove %q: it's supplied by the playlist's directory source", track.DisplayName())
		return
	}
	loaded := m.loadedPlaylist
	var saved []playlist.Track
	persisted := false
	if loaded != "" {
		if saver, ok := m.localProvider.(provider.PlaylistSaver); ok {
			var err error
			saved, err = m.localProvider.Tracks(loaded)
			if err != nil {
				m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
				return
			}
			// saved rescans directory sources, so a new file could have shifted
			// indexes since the queue was loaded. Match the persisted explicit
			// track by path so the wrong track is never removed.
			savedIdx := -1
			for i, candidate := range saved {
				if !candidate.DirSourced && candidate.Path == track.Path {
					savedIdx = i
					break
				}
			}
			if savedIdx < 0 {
				m.status.Errorf(statusTTLDefault, "Remove failed: selected track is no longer in %q", loaded)
				return
			}
			original := cloneTracks(saved)
			saved = append(saved[:savedIdx:savedIdx], saved[savedIdx+1:]...)
			if err := saver.SavePlaylist(loaded, saved); err != nil {
				m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
				return
			}
			saved = original
			persisted = true
		}
	}
	wasActive := idx == m.playlist.Index()
	if !m.playlist.Remove(idx) {
		return
	}
	m.normalizeQueueOverlay()
	m.playlistUndo = playlistUndo{active: true, snapshot: snapshot, loaded: loaded, saved: saved, persisted: persisted}
	if wasActive {
		m.stopPlayback()
		m.clearPreload()
	}
	if newLen := m.playlist.Len(); newLen == 0 {
		m.plCursor = 0
	} else if m.plCursor >= newLen {
		m.plCursor = newLen - 1
	}
	m.adjustScroll()
	if loaded != "" {
		m.status.Showf(statusTTLDefault, "Removed from %q: %s (Ctrl+Z to undo)", loaded, track.DisplayName())
	} else {
		m.status.Showf(statusTTLDefault, "Removed from queue: %s (Ctrl+Z to undo)", track.DisplayName())
	}
	m.notifyPlayback()
}

func (m *Model) undoPlaylistMutation() tea.Cmd {
	undo := m.playlistUndo
	if !undo.active {
		m.status.Warning("Nothing to undo", statusTTLShort)
		return nil
	}
	if undo.persisted {
		saver := m.localSaver()
		if saver == nil {
			m.status.Warning("Undo unavailable", statusTTLDefault)
			return nil
		}
		if err := saver.SavePlaylist(undo.loaded, cloneTracks(undo.saved)); err != nil {
			m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
			return nil
		}
	}
	m.playlist.Restore(undo.snapshot)
	m.normalizeQueueOverlay()
	m.playlistUndo = playlistUndo{}
	if m.plCursor >= m.playlist.Len() {
		m.plCursor = max(0, m.playlist.Len()-1)
	}
	m.adjustScroll()
	m.status.Show("Restored previous playlist state", statusTTLDefault)
	return m.rearmPreload()
}

// playTrack plays a track, using async HTTP for streams and sync I/O for local files.
// yt-dlp URLs are streamed via a piped yt-dlp | ffmpeg chain for instant playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	m.pausedAt = time.Time{}
	m.player.CancelSeekYTDL()
	m.clearPreload()
	ticket, ctx := m.player.BeginStart()
	m.pending = m.capturePlaybackTrack(track, ticket)
	m.playbackDetached = false
	m.err = nil
	if track.Feed || playlist.IsFeed(track.Path) {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.status.Activity("Loading feed...", statusTTLLong)
		return resolveFeedTrackCmd(ticket, track.Path)
	}
	if m.provider != nil {
		m.playingProvider = m.provider.Name()
	}
	startAt := m.startPosition(ctx, track)
	if needsAsyncPrepare(track) {
		m.buffering = true
		m.bufferingAt = time.Now()
		return prepareSourceCmd(m.player, ticket, track, startAt)
	}
	// A local file opens without network work, so prepare it here and commit
	// through the same path the asynchronous message takes.
	return m.applyPreparedSource(prepareSource(m.player, ticket, track, startAt))
}

// needsAsyncPrepare reports whether opening track may block on the network or
// a subprocess. Such sources prepare on a command goroutine and show as
// buffering. The Stream flag is the primary signal; the path checks cover
// remote tracks constructed without it.
func needsAsyncPrepare(track playlist.Track) bool {
	return track.Stream || playlist.IsYTDL(track.Path) || playlist.IsURL(track.Path) ||
		strings.Contains(track.Path, "://") || strings.HasPrefix(track.Path, "spotify:")
}

func (m *Model) backfillLoadedPlaylistDuration(track playlist.Track) {
	if m.loadedPlaylist == "" || track.DurationSecs > 0 || track.Stream || playlist.IsURL(track.Path) || strings.HasPrefix(track.Path, "ssh://") {
		return
	}
	dur := int(m.player.Duration().Seconds())
	if dur <= 0 {
		return
	}
	saver, ok := m.localProvider.(provider.PlaylistSaver)
	if !ok {
		return
	}
	tracks, err := m.localProvider.Tracks(m.loadedPlaylist)
	if err != nil {
		return
	}
	changed := false
	for i := range tracks {
		if tracks[i].Path == track.Path && tracks[i].DurationSecs == 0 {
			tracks[i].DurationSecs = dur
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	if err := saver.SavePlaylist(m.loadedPlaylist, tracks); err == nil {
		if idx := m.playlist.Index(); idx >= 0 {
			track.DurationSecs = dur
			m.playlist.SetTrack(idx, track)
		}
	}
}

// activatePlaybackTrack applies effects only once the source reached the engine.
func (m *Model) activatePlaybackTrack(source *playbackTrack, stats player.PlaybackStats) {
	track := source.track
	m.playing = source
	m.playbackDetached = source.detached
	m.setPlaybackContext(source.context, source.index)
	m.resetTitleScroll()
	nextRequest(&m.requests.lyrics)
	m.persistPlaybackContext(track, max(0, int(stats.Position.Seconds())), time.Now())
	m.lastProgressReport = time.Time{}
	m.queueEffect(m.recordListenedTrack(track))
	m.reconnect.attempts = 0
	m.reconnect.at = time.Time{}
	m.streamTitle = ""
	m.lyrics.lines = nil
	m.lyrics.err = nil
	m.lyrics.query = ""
	m.lyrics.scroll = 0
	m.lyrics.loading = false
	m.seek = seekState{gen: m.seek.gen + 1}
	if m.lyrics.visible {
		q := lyricsLookupKey(track, track.Artist, track.Title)
		if q == "" {
			return
		}
		m.lyrics.loading = true
		m.lyrics.query = q
		m.queueEffect(m.fetchLyricsForTrack(track, track.Artist, track.Title))
		return
	}
}

// queueEffect defers a command produced while settling a transition. Helpers
// such as clearPreload adopt a gapless promotion from call sites that have no
// command return path, so Update drains the queue after every message instead
// of each caller threading commands through.
func (m *Model) queueEffect(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	m.playbackEffects = tea.Batch(m.playbackEffects, cmd)
}

func (m *Model) fetchLyricsForTrack(track playlist.Track, artist, title string) tea.Cmd {
	return fetchTrackLyricsCmd(track, artist, title, m.lyrics.query, nextRequest(&m.requests.lyrics), m.spotifyLyricFetcher())
}

// togglePlayPause starts playback if stopped, or toggles pause if playing.
// For live streams and long-paused yt-dlp streams, unpausing reconnects instead
// of playing stale data sitting in OS/decoder buffers from before the pause.
func (m *Model) togglePlayPause() tea.Cmd {
	if m.buffering {
		return nil
	}
	if !m.player.IsPlaying() {
		if m.playlist.CurrentIsQueued() {
			return m.playCurrentLogicalTrack()
		}
		return m.playCurrentTrack()
	}
	if m.player.IsPaused() {
		track, idx := m.displayedPlaybackTrack()
		pausedFor := time.Duration(0)
		if !m.pausedAt.IsZero() {
			pausedFor = time.Since(m.pausedAt)
		}
		if m.currentPlaybackIsLive(track) || shouldReconnectOnUnpause(track, idx, pausedFor) {
			if playlist.IsYTDL(track.Path) && m.player.IsYTDLSeek() {
				return m.reconnectYTDLOnUnpause()
			}
			m.pausedAt = time.Time{}
			return m.playTrack(track)
		}
	}
	m.togglePlayerPause()
	return nil
}

func (m *Model) togglePlayerPause() {
	m.player.TogglePause()
	if m.player.IsPaused() {
		m.pausedAt = time.Now()
		return
	}
	m.pausedAt = time.Time{}
}

func (m *Model) reconnectYTDLOnUnpause() tea.Cmd {
	m.seek.active = true
	m.seek.targetPos = m.player.Position()
	m.seek.timer = 0
	m.seek.timerFor = 0
	m.seek.grace = 0
	m.seek.graceFor = 0
	m.player.CancelSeekYTDL()
	m.status.Activity("Reconnecting stream...", statusTTLMedium)

	p := m.player
	ticket := m.playbackTicket()
	return func() tea.Msg {
		err := p.SeekYTDL(ticket, 0)
		return ytdlUnpauseReconnectMsg{ticket: ticket, err: err}
	}
}

// shouldReconnectOnUnpause reports whether unpausing should reconnect and
// restart instead of resuming buffered audio.
func shouldReconnectOnUnpause(track playlist.Track, idx int, pausedFor time.Duration) bool {
	if idx < 0 {
		return false
	}
	if track.IsLive() {
		return true
	}
	return pausedFor >= ytdlReconnectPauseThreshold && playlist.IsYTDL(track.Path)
}

// startPosition returns where track should begin. The returned func may make a
// provider HTTP call, so callers run it on their own goroutine.
func (m *Model) startPosition(ctx context.Context, track playlist.Track) func() time.Duration {
	// Only remote tracks have a server-side position, so a local file never
	// reaches the provider and the synchronous caller cannot block on HTTP.
	var positioner provider.TrackPosition
	if track.Stream || playlist.IsURL(track.Path) {
		positioner = m.findTrackPosition(track)
	}
	hint := time.Duration(0)
	if m.resume.path == track.Path && m.resume.secs > 0 {
		hint = time.Duration(m.resume.secs) * time.Second
	}
	if positioner == nil {
		return func() time.Duration { return hint }
	}
	return func() time.Duration { return positioner.TrackPosition(ctx, track) }
}

// clearResume drops the startup hint for track.
func (m *Model) clearResume(track playlist.Track) {
	if m.resume.path == track.Path {
		m.resume.path = ""
		m.resume.secs = 0
	}
}

// applyResume seeks to the saved resume position if the current track matches.
// yt-dlp resume is asynchronous because it rebuilds the playback pipeline.
func (m *Model) applyResume() tea.Cmd {
	// secs == 0 is indistinguishable from "never played"; skip resume.
	if m.resume.path == "" || m.resume.secs <= 0 {
		return nil
	}
	track, _ := m.displayedPlaybackTrack()
	if track.Path != m.resume.path {
		return nil
	}
	// PlayAt already started at the provider's position, so spend the hint
	// without seeking rather than overriding that with a stale value.
	if m.findTrackPosition(track) != nil {
		m.clearResume(track)
		return nil
	}
	// Only seek if the player reports the stream is seekable; otherwise the
	// seek is a no-op that returns nil, which we must not mistake for success.
	if !m.player.Seekable() {
		return nil
	}
	target := m.clampPosition(time.Duration(m.resume.secs) * time.Second)
	if playlist.IsMixcloudURL(track.Path) && m.player.IsYTDLSeek() {
		m.seek.active = true
		m.seek.inFlight = true
		m.seek.pending = false
		m.seek.targetPos = target
		m.seek.timer = 0
		m.seek.timerFor = 0
		m.player.CancelSeekYTDL()
		m.status.Activityf(statusTTLLong, "Resuming at %s…", formatJumpClock(target))
		return m.seekCmd(target, true)
	}
	if err := m.player.Seek(m.playbackTicket(), target-m.player.Position()); err == nil {
		m.resume.path = ""
		m.resume.secs = 0
	}
	return nil
}
