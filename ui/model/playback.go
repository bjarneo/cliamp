package model

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const ytdlReconnectPauseThreshold = 45 * time.Second

func (m *Model) replacePlaylist(tracks []playlist.Track) {
	m.playlist.Replace(tracks)
	m.normalizeQueueOverlay()
}

// nextTrack advances to the next playlist track and starts playing it.
// Unplayable tracks are skipped automatically.
func (m *Model) nextTrack() tea.Cmd {
	if m.playbackDetached {
		m.playbackDetached = false
		if m.playlist.Len() == 0 {
			m.player.Stop()
			m.clearPlaybackTrack()
			return nil
		}
		return m.playCurrentTrack()
	}
	track, ok := m.playlist.Next()
	m.normalizeQueueOverlay()
	if !ok {
		m.player.Stop()
		m.clearPlaybackTrack()
		return nil
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return m.playTrack(track)
}

// prevTrack goes to the previous track, or restarts if >3s into the current one.
// Unplayable tracks are skipped automatically.
func (m *Model) prevTrack() tea.Cmd {
	if m.player.Position() > 3*time.Second {
		if m.player.Seekable() {
			// Seekable media rewinds in place; non-seekable streams must be restarted.
			m.player.Seek(-m.player.Position())
			return nil
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
	m.titleOff = 0
	m.plCursor = idx
	m.adjustScroll()
	return m.playTrack(track)
}

// playCurrentTrack starts playing the selected track, skipping forward in
// playlist order if the selection is unplayable.
func (m *Model) playCurrentTrack() tea.Cmd {
	m.titleOff = 0
	if m.playlist.Len() == 0 {
		return nil
	}
	activation, ok := m.playlist.ActivateSelected()
	if !ok {
		m.player.Stop()
		m.clearPlaybackTrack()
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
	m.player.Stop()
	m.player.ClearPreload()
	m.playlist.Add(track)
	m.loadedPlaylist = ""
	m.addToHeaderState([]playlist.Track{track})
	idx := m.playlist.Len() - 1
	m.playlist.SetIndex(idx)
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
		m.playlist.SetIndex(idx)
		m.plCursor = idx
		m.adjustScroll()
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	return nil
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
	nextRequest(&m.requests.spotLists)
	nextRequest(&m.requests.spotMutation)
	m.spotSearch = spotSearchState{}
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
	return nil
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
		m.player.Stop()
		m.player.ClearPreload()
		m.clearPlaybackTrack()
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

func (m *Model) undoPlaylistMutation() {
	undo := m.playlistUndo
	if !undo.active {
		m.status.Warning("Nothing to undo", statusTTLShort)
		return
	}
	if undo.persisted {
		saver := m.localSaver()
		if saver == nil {
			m.status.Warning("Undo unavailable", statusTTLDefault)
			return
		}
		if err := saver.SavePlaylist(undo.loaded, cloneTracks(undo.saved)); err != nil {
			m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
			return
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
}

// playTrack plays a track, using async HTTP for streams and sync I/O for local files.
// yt-dlp URLs are streamed via a piped yt-dlp | ffmpeg chain for instant playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	m.pausedAt = time.Time{}
	if track.Feed || playlist.IsFeed(track.Path) {
		m.feedLoading = true
		m.status.Activity("Loading feed...", statusTTLLong)
		return resolveFeedTrackCmd(track.Path)
	}
	track, fetchCmd := m.beginPlaybackTrack(track)

	// Stream yt-dlp URLs (YouTube, SoundCloud, Bandcamp, etc.) via pipe chain.
	if playlist.IsYTDL(track.Path) {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		dur := time.Duration(track.DurationSecs) * time.Second
		if fetchCmd != nil {
			return tea.Batch(playYTDLStreamCmd(m.player, track.Path, dur, m.requests.stream), fetchCmd)
		}
		return playYTDLStreamCmd(m.player, track.Path, dur, m.requests.stream)
	}
	// Fire now-playing notification for Navidrome tracks.
	m.nowPlaying(track)
	dur := time.Duration(track.DurationSecs) * time.Second
	if track.Stream {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		return tea.Batch(playStreamCmd(m.player, track.Path, dur, m.requests.stream), fetchCmd)
	}
	if err := m.player.Play(track.Path, dur); err != nil {
		// Provider session went stale (e.g. Spotify auth expired and
		// silent reconnect failed). Surface the standard sign-in
		// overlay rather than the raw stream error.
		if errors.Is(err, playlist.ErrNeedsAuth) {
			m.provSignIn = true
			m.err = nil
		} else {
			m.err = err
		}
	} else {
		m.err = nil
		m.applyResume()
		m.backfillLoadedPlaylistDuration(track)
	}

	if fetchCmd != nil {
		return tea.Batch(m.preloadNext(), fetchCmd)
	}
	return m.preloadNext()
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

// beginPlaybackTrack centralizes metadata refresh and model state reset for a
// new active track. It is used both by explicit playback and by gapless
// transitions, which advance audio without calling playTrack.
func (m *Model) beginPlaybackTrack(track playlist.Track) (playlist.Track, tea.Cmd) {
	nextRequest(&m.requests.stream)
	nextRequest(&m.requests.preload)
	m.preloading = false
	nextRequest(&m.requests.lyrics)
	track = playlist.RefreshEmbeddedMetadata(track)
	m.setPlaybackTrack(track)
	m.reconnect.attempts = 0
	m.reconnect.at = time.Time{}
	m.streamTitle = ""
	m.lyrics.lines = nil
	m.lyrics.err = nil
	m.lyrics.query = ""
	m.lyrics.scroll = 0
	m.seek.active = false
	m.seek.timer = 0
	m.seek.timerFor = 0
	m.seek.grace = 0
	m.seek.graceFor = 0
	if m.lyrics.visible {
		q := lyricsLookupKey(track, track.Artist, track.Title)
		if q == "" {
			m.lyrics.loading = false
			return track, nil
		}
		m.lyrics.loading = true
		m.lyrics.query = q
		return track, m.fetchLyricsForTrack(track, track.Artist, track.Title)
	}
	m.lyrics.loading = false
	return track, nil
}

func (m *Model) fetchLyricsForTrack(track playlist.Track, artist, title string) tea.Cmd {
	return fetchTrackLyricsCmd(track, artist, title, m.lyrics.query, nextRequest(&m.requests.lyrics))
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
		if shouldReconnectOnUnpause(track, idx, pausedFor) {
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

	p := m.player
	return func() tea.Msg {
		err := p.SeekYTDL(0)
		if err == nil {
			p.TogglePause()
		}
		return ytdlUnpauseReconnectMsg{err: err}
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

// applyResume seeks to the saved resume position if the current track matches.
// It clears the resume state after a successful seek so it only fires once.
func (m *Model) applyResume() {
	// secs == 0 is indistinguishable from "never played"; skip resume.
	if m.resume.path == "" || m.resume.secs <= 0 {
		return
	}
	track, _ := m.currentPlaybackTrack()
	if track.Path != m.resume.path {
		return
	}
	// Only seek if the player reports the stream is seekable; otherwise the
	// seek is a no-op that returns nil, which we must not mistake for success.
	if !m.player.Seekable() {
		return
	}
	target := time.Duration(m.resume.secs) * time.Second
	if err := m.player.Seek(target - m.player.Position()); err == nil {
		m.resume.path = ""
		m.resume.secs = 0
	}
}
