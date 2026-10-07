package model

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	ytdlReconnectPauseThreshold = 45 * time.Second
	resumeSaveInterval          = 2 * time.Second
)

// replacePlaylist replaces the queue. It advances the queue generation, so a
// feed or file browser replace that is still resolving is dropped. It ends
// the batch load of a YouTube radio playlist, so no batch appends to the new
// queue. The undo of the last queue edit goes, because it restores the old
// queue.
func (m *Model) replacePlaylist(tracks []playlist.Track) {
	nextRequest(&m.requests.queue)
	m.resetYTDLBatch()
	m.playlistUndo = playlistUndo{}
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
	if m.buffering || m.seek.active || m.seek.inFlight || m.seek.pending {
		return
	}
	if !m.lastResumeSave.IsZero() && now.Sub(m.lastResumeSave) < resumeSaveInterval {
		return
	}
	track, index := m.currentPlaybackTrack()
	if index < 0 {
		return
	}
	// cachedPos can still contain a seek preview rather than decoder progress.
	m.persistPlaybackContext(track, max(0, int(m.player.Position().Seconds())), now)
}

// nextTrack advances to the next playlist track and starts playing it.
// Unplayable tracks are skipped automatically.
func (m *Model) nextTrack() tea.Cmd {
	track, ok := m.advanceToNext()
	if !ok {
		return nil
	}
	return m.playTrack(track)
}

// advanceToNext moves the playlist to the track that plays after the current
// one and returns it. After a replace detached the playing track, that is the
// selected row of the new list, or the first playable row after it. When
// nothing playable follows, it ends the queue and returns false. nextTrack
// then starts the track, and a gapless switch already plays it.
func (m *Model) advanceToNext() (playlist.Track, bool) {
	var track playlist.Track
	var ok bool
	if m.playbackDetached {
		m.playbackDetached = false
		var activation playlist.SelectionActivation
		activation, ok = m.playlist.ActivateSelected()
		track = activation.Track
		switch {
		case !ok && m.playlist.Len() > 0:
			m.status.Warning("No available tracks", statusTTLDefault)
		case activation.Skipped:
			m.status.Warning("Track unavailable, skipping...", statusTTLDefault)
		}
	} else {
		track, ok = m.playlist.Next()
	}
	m.normalizeQueueOverlay()
	if !ok {
		m.endQueue()
		return playlist.Track{}, false
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return track, true
}

// prevTrack goes to the previous track, or restarts if >3s into the current one.
// Unplayable tracks are skipped automatically.
func (m *Model) prevTrack() tea.Cmd {
	// A pending or running seek has not moved Position yet, so a rewind that
	// is still on its way counts as done.
	pos := m.player.Position()
	if m.seek.active {
		pos = m.seek.targetPos
	}
	if pos > 3*time.Second {
		if m.player.Seekable() {
			// Seekable media rewinds in place; non-seekable streams must be restarted.
			// The rewind ends the play so far. finishSeek reports that play
			// and starts the replay, which can scrobble again, when the
			// rewind lands. A failed rewind plays on as the same play.
			m.seek.rewindAt, m.seek.rewindDur = m.player.PositionAndDuration()
			m.seek.rewind = true
			cmd, err := m.trySeekAbsolute(0)
			if err != nil {
				m.seek.rewind = false
			}
			return cmd
		}
		track, idx := m.currentPlaybackTrack()
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
	m.stopPlayback()
	m.player.ClearPreload()
	idx := m.appendTracks(track)
	m.playlist.SetIndex(idx)
	m.plCursor = idx
	m.adjustScroll()
	m.status.Showf(statusTTLMedium, "Playing: %s", track.DisplayName())
	return m.playCurrentTrack()
}

// appendTrack appends a track to the playlist; auto-plays if nothing is playing.
func (m *Model) appendTrack(track playlist.Track) tea.Cmd {
	wasEmpty := m.playlist.Len() == 0
	idx := m.appendTracks(track)
	m.status.Showf(statusTTLMedium, "Added: %s", track.DisplayName())
	if wasEmpty || !m.player.IsPlaying() {
		m.playlist.SetIndex(idx)
		m.plCursor = idx
		m.adjustScroll()
		return m.playCurrentTrack()
	}
	return nil
}

// playAlbumImmediate appends an expanded album to the queue and starts it at
// its first track. Like playTrackImmediate it adds rather than replaces, so a
// queue built up over an evening survives picking an album from search.
func (m *Model) playAlbumImmediate(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	m.stopPlayback()
	m.player.ClearPreload()
	idx := m.appendTracks(tracks...)
	m.playlist.SetIndex(idx)
	m.plCursor = idx
	m.adjustScroll()
	m.status.Showf(statusTTLMedium, "Playing album: %s (%d tracks)", album.Title, len(tracks))
	return m.playCurrentTrack()
}

// appendAlbum appends an expanded album to the queue; auto-plays from its first
// track if nothing is playing.
func (m *Model) appendAlbum(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	wasEmpty := m.playlist.Len() == 0
	idx := m.appendTracks(tracks...)
	m.status.Showf(statusTTLMedium, "Added album: %s (%d tracks)", album.Title, len(tracks))
	if wasEmpty || !m.player.IsPlaying() {
		m.playlist.SetIndex(idx)
		m.plCursor = idx
		m.adjustScroll()
		return m.playCurrentTrack()
	}
	return nil
}

// queueAlbumNext queues a whole album to play after the current track, keeping
// its running order.
func (m *Model) queueAlbumNext(album playlist.Track, tracks []playlist.Track) tea.Cmd {
	idx := m.appendTracks(tracks...)
	for i := range tracks {
		m.playlist.Queue(idx + i)
	}
	m.status.Showf(statusTTLMedium, "Queued album: %s (%d tracks)", album.Title, len(tracks))
	if !m.player.IsPlaying() {
		return m.nextTrack()
	}
	return m.rearmStalePreload()
}

// closeNetSearch fully resets the net search overlay and restores focus,
// dropping any cached results so they don't linger between sessions.
func (m *Model) closeNetSearch() {
	nextRequest(&m.requests.netSearch)
	m.netSearch = netSearchState{}
	m.focus = m.prevFocus
}

// closeSearchOverlay fully resets the provider search overlay, dropping cached
// results, playlists, and the selected track.
func (m *Model) closeSearchOverlay() {
	m.cancelSearchOverlayRequest()
	nextRequest(&m.requests.searchOverlay)
	m.invalidateSearchOverlayAlbumRequest()
	nextRequest(&m.requests.searchOverlayLists)
	nextRequest(&m.requests.searchOverlayMutation)
	m.searchOverlay = searchOverlayState{}
}

func (m *Model) invalidateSearchOverlayAlbumRequest() {
	m.cancelSearchOverlayRequest()
	nextRequest(&m.requests.searchOverlayAlbum)
	m.searchOverlay.albumLoading = false
}

func (m *Model) newSearchOverlayRequestContext(timeout time.Duration) context.Context {
	m.cancelSearchOverlayRequest()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.searchOverlay.cancel = cancel
	return ctx
}

func (m *Model) cancelSearchOverlayRequest() {
	if m.searchOverlay.cancel != nil {
		m.searchOverlay.cancel()
		m.searchOverlay.cancel = nil
	}
}

// queueTrackNext adds a track to the playlist and queues it to play next.
func (m *Model) queueTrackNext(track playlist.Track) tea.Cmd {
	idx := m.appendTracks(track)
	m.playlist.Queue(idx)
	m.normalizeQueueOverlay()
	m.status.Showf(statusTTLMedium, "Queued: %s", track.DisplayName())
	if !m.player.IsPlaying() {
		return m.nextTrack()
	}
	return m.rearmStalePreload()
}

// recordPlaylistUndo lets Ctrl+Z undo the queue edit that just ran. The undo
// holds only while the queue and the loaded playlist stay as the edit left
// them.
func (m *Model) recordPlaylistUndo(undo playlistUndo) {
	undo.active = true
	undo.revision = m.playlist.Revision()
	undo.loaded = m.loadedPlaylist
	m.playlistUndo = undo
}

func (m *Model) undoPlaylistMutation() tea.Cmd {
	undo := m.playlistUndo
	if !undo.active {
		m.status.Warning("Nothing to undo", statusTTLShort)
		return nil
	}
	if undo.revision != m.playlist.Revision() || undo.loaded != m.loadedPlaylist {
		// Restoring the snapshot would drop every change since the edit.
		m.playlistUndo = playlistUndo{}
		m.status.Warning("Can't undo: the playlist changed since the edit", statusTTLDefault)
		return nil
	}
	if undo.persisted {
		// Put back only the removed track in one locked update, so a track
		// that another writer added since the edit is kept.
		updater, ok := m.localProvider.(playlistUpdater)
		if !ok {
			m.status.Warning("Undo unavailable", statusTTLDefault)
			return nil
		}
		err := updater.UpdatePlaylist(undo.loaded, func(tracks []playlist.Track) ([]playlist.Track, error) {
			if slices.ContainsFunc(tracks, func(t playlist.Track) bool { return t.Path == undo.removed.Path }) {
				return nil, playlist.ErrPlaylistUnchanged
			}
			return slices.Insert(tracks, min(undo.savedIdx, len(tracks)), undo.removed), nil
		})
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
			return nil
		}
	}
	m.playlist.Restore(undo.snapshot)
	// After a song mix the restored queue need not hold the playing song
	// at its current row. Playback then goes on detached, and the queue
	// resumes from its current row when the song ends.
	if m.playingTrackActive {
		if cur, idx := m.playlist.Current(); idx < 0 || cur.Path != m.playingTrack.Path {
			m.playbackDetached = true
		}
	}
	m.normalizeQueueOverlay()
	m.playlistUndo = playlistUndo{}
	if m.plCursor >= m.playlist.Len() {
		m.plCursor = max(0, m.playlist.Len()-1)
	}
	m.adjustScroll()
	m.status.Show("Restored previous playlist state", statusTTLDefault)
	return m.rearmStalePreload()
}

// playTrack plays a track, using async starts for streams and local ffmpeg
// formats, and sync I/O for other local files. The player picks the
// pipeline, such as the yt-dlp | ffmpeg chain for a yt-dlp page URL.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	m.pausedAt = time.Time{}
	if track.Feed || playlist.IsFeed(track.Path) {
		m.feedLoading = true
		m.status.Activity("Loading feed...", statusTTLLong)
		return resolveFeedTrackCmd(track.Path, m.requests.stream, nextRequest(&m.requests.queue))
	}
	// The track that plays now is left, so it can scrobble before the
	// engine moves on.
	m.leaveTrack(m.player.PositionAndDuration())
	if m.provider != nil {
		m.playingProvider = m.provider.Name()
	}
	track, fetchCmd := m.beginPlaybackTrack(track)

	dur := time.Duration(track.DurationSecs) * time.Second
	// yt-dlp page URLs (YouTube, SoundCloud, Bandcamp, etc.) and custom URIs
	// such as spotify: open over the network, which can take seconds. A
	// local file that ffmpeg decodes waits for ffprobe and the first audio.
	// Start them off the Update goroutine like streams.
	if track.Stream || playlist.IsYTDL(track.Path) || m.isCustomStreamURI(track.Path) || player.UsesLocalFFmpeg(track.Path) {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		return tea.Batch(playStreamCmd(m.player, track.Path, dur, m.startPosition(track), m.requests.stream), fetchCmd)
	}
	if err := m.player.PlayAt(track.Path, dur, m.startPosition(track)()); err != nil {
		// Provider session went stale (e.g. Spotify auth expired and
		// silent reconnect failed). Surface the standard sign-in
		// overlay rather than the raw stream error.
		if errors.Is(err, playlist.ErrNeedsAuth) {
			m.provPane.signIn = true
			m.err = nil
		} else {
			m.err = err
			applog.Warn("play %q: %v", track.Path, err)
		}
	} else {
		m.err = nil
		// Async starts resume after streamPlayedMsg. A native local file
		// started at the hint, so applyResume seeks here only when that
		// start seek failed.
		m.applyResume()
		m.nowPlaying(track)
		return tea.Batch(m.preloadNext(), fetchCmd, m.backfillLoadedPlaylistDuration(track))
	}

	if fetchCmd != nil {
		return tea.Batch(m.preloadNext(), fetchCmd)
	}
	return m.preloadNext()
}

// isCustomStreamURI reports whether a provider decodes path itself, as the
// Spotify provider does for spotify: URIs, or resolves it at play time.
func (m *Model) isCustomStreamURI(path string) bool {
	if m.hasSourceResolver(path) {
		return true
	}
	for _, pe := range m.providers {
		cs, ok := pe.Provider.(provider.CustomStreamer)
		if !ok {
			continue
		}
		for _, scheme := range cs.URISchemes() {
			if strings.HasPrefix(path, scheme) {
				return true
			}
		}
	}
	return false
}

// hasSourceResolver reports whether the player resolves path when playback
// starts, as it does for qobuz:// and tidal:// URIs. Files that older
// versions wrote reload such tracks without the stream flag, so this check
// still marks them as network tracks.
func (m *Model) hasSourceResolver(path string) bool {
	r, ok := m.player.(interface{ HasSourceResolver(string) bool })
	return ok && r.HasSourceResolver(path)
}

// playlistUpdater is a provider that can change a saved playlist in one
// read-modify-write that no other writer interleaves with, as the local
// provider does. UpdatePlaylist passes the current tracks of the playlist to
// fn and saves the tracks that fn returns. fn runs while other writers wait,
// so keep slow work, such as network calls, out of it.
type playlistUpdater interface {
	UpdatePlaylist(name string, fn func([]playlist.Track) ([]playlist.Track, error)) error
}

// backfillLoadedPlaylistDuration records the decoded duration of a local
// track that has none. It sets the duration in the queue at once. The
// returned command writes it to the loaded playlist file through one locked
// UpdatePlaylist, so a queue edit that saves at the same time is kept. A
// track from a directory source gets no command, because the playlist file
// never stores those tracks.
func (m *Model) backfillLoadedPlaylistDuration(track playlist.Track) tea.Cmd {
	name := m.writableLoadedPlaylist()
	if name == "" || track.DurationSecs > 0 || track.Stream || playlist.IsURL(track.Path) || strings.HasPrefix(track.Path, "ssh://") || m.isCustomStreamURI(track.Path) {
		return nil
	}
	dur := int(m.player.Duration().Seconds())
	if dur <= 0 {
		return nil
	}
	updater, ok := m.localProvider.(playlistUpdater)
	if !ok {
		return nil
	}
	if idx := m.playlist.Index(); idx >= 0 {
		if current, ok := m.playlist.Track(idx); ok && current.Path == track.Path {
			track.DurationSecs = dur
			m.playlist.SetTrack(idx, track)
		}
	}
	if track.DirSourced {
		return nil
	}
	return func() tea.Msg {
		_ = updater.UpdatePlaylist(name, func(tracks []playlist.Track) ([]playlist.Track, error) {
			for i := range tracks {
				if tracks[i].DirSourced || tracks[i].Path != track.Path || tracks[i].DurationSecs != 0 {
					continue
				}
				tracks[i].DurationSecs = dur
				return tracks, nil
			}
			return nil, playlist.ErrPlaylistUnchanged
		})
		return nil
	}
}

// beginPlaybackTrack centralizes metadata refresh and model state reset for a
// new active track. It is used both by explicit playback and by gapless
// transitions, which advance audio without calling playTrack.
func (m *Model) beginPlaybackTrack(track playlist.Track) (playlist.Track, tea.Cmd) {
	m.resetTitleScroll()
	nextRequest(&m.requests.stream)
	if m.player != nil {
		m.player.CancelSeekYTDL()
		m.player.SetPlaybackGeneration(m.requests.stream)
		m.player.ClearPreload()
	}
	nextRequest(&m.requests.preload)
	m.preloading = false
	m.preloadFailed = ""
	nextRequest(&m.requests.lyrics)
	track = playlist.RefreshEmbeddedMetadata(track)
	context, index := track.PlaybackContext()
	if index < 0 && m.playlist != nil {
		context = m.playlist.Tracks()
		index = m.playlist.Index()
		if index < 0 || index >= len(context) || context[index].Path != track.Path {
			index = trackIndexByPath(context, track.Path)
		}
	}
	m.setPlaybackContext(context, index)
	m.setPlaybackTrack(track)
	positionSec := 0
	if m.resume.path == track.Path {
		positionSec = m.resume.secs
	}
	m.persistPlaybackContext(track, positionSec, time.Now())
	historyCmd := m.recordListenedTrack(track)
	m.reconnect.attempts = 0
	m.reconnect.at = time.Time{}
	m.reconnect.ytdlLiveDrain = false
	m.streamTitle = ""
	m.lyrics.lines = nil
	m.lyrics.err = nil
	m.lyrics.query = ""
	m.lyrics.scroll = 0
	m.resetSeek()
	if m.lyrics.visible {
		q := lyricsLookupKey(track, track.Artist, track.Title)
		if q == "" {
			return track, historyCmd
		}
		m.lyrics.loading = true
		m.lyrics.query = q
		return track, tea.Batch(historyCmd, m.fetchLyricsForTrack(track, track.Artist, track.Title))
	}
	m.lyrics.loading = false
	return track, historyCmd
}

func (m *Model) fetchLyricsForTrack(track playlist.Track, artist, title string) tea.Cmd {
	return fetchTrackLyricsCmd(track, artist, title, m.lyrics.query, nextRequest(&m.requests.lyrics), m.trackLyricsSources())
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
		track, idx := m.currentPlaybackTrack()
		pausedFor := time.Duration(0)
		if !m.pausedAt.IsZero() {
			pausedFor = time.Since(m.pausedAt)
		}
		if m.currentPlaybackIsLive(track) || shouldReconnectOnUnpause(track, idx, pausedFor) {
			if playlist.IsYTDL(track.Path) && m.player.IsYTDLSeek() {
				return m.reconnectYTDLOnUnpause()
			}
			m.pausedAt = time.Time{}
			m.player.Stop()
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

	// The Update loop unpauses after the reconnect, and only when the same
	// track still plays. A skip or a stop can come first.
	p, gen, seekGen := m.player, m.requests.stream, m.seek.gen
	return func() tea.Msg {
		return ytdlUnpauseReconnectMsg{err: p.SeekYTDL(0), gen: gen, seekGen: seekGen}
	}
}

// shouldReconnectOnUnpause reports whether unpausing should reconnect and
// restart instead of resuming buffered audio.
func shouldReconnectOnUnpause(track playlist.Track, idx int, pausedFor time.Duration) bool {
	if idx < 0 {
		return false
	}
	// Whether a flagged yt-dlp track is still live depends on the player, so
	// the caller decides that through currentPlaybackIsLive.
	if track.IsLive() && !playlist.IsYTDL(track.Path) {
		return true
	}
	return pausedFor >= ytdlReconnectPauseThreshold && playlist.IsYTDL(track.Path)
}

// startPosition returns where track should begin. The returned func may make a
// provider HTTP call, so callers run it on their own goroutine.
func (m *Model) startPosition(track playlist.Track) func() time.Duration {
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
	return func() time.Duration { return positioner.TrackPosition(track) }
}

// clearResume drops the startup hint for track.
func (m *Model) clearResume(track playlist.Track) {
	if m.resume.path == track.Path {
		m.resume.path = ""
		m.resume.secs = 0
	}
}

// applyResume seeks to the saved resume position if the current track matches
// and playback did not already start there. A seek that restarts a decoder, as
// for yt-dlp or a network stream, runs in the returned command so the network
// never blocks Update.
func (m *Model) applyResume() tea.Cmd {
	// secs == 0 is indistinguishable from "never played"; skip resume.
	if m.resume.path == "" || m.resume.secs <= 0 {
		return nil
	}
	track, _ := m.currentPlaybackTrack()
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
	// A seekable decoder already started at the hint and can play on past it
	// before this runs. A second seek would restart a local ffmpeg decoder in
	// Update, so spend the hint here.
	if m.player.Position() >= target-time.Second {
		m.clearResume(track)
		return nil
	}
	if m.needsDebouncedSeek() {
		m.seek.active = true
		m.seek.inFlight = true
		m.seek.pending = false
		m.seek.targetPos = target
		m.seek.timer = 0
		m.seek.timerFor = 0
		if m.player.IsYTDLSeek() {
			m.player.CancelSeekYTDL()
		}
		m.status.Activityf(statusTTLLong, "Resuming at %s…", formatJumpClock(target))
		return m.seekCmd(target, true)
	}
	if err := m.player.Seek(target - m.player.Position()); err == nil {
		m.resume.path = ""
		m.resume.secs = 0
	}
	return nil
}
