package model

import (
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/playlist"
)

// nextTrack advances to the next playlist track and starts playing it.
// Unplayable tracks are skipped automatically.
func (m *Model) nextTrack() tea.Cmd {
	track, ok := m.playlist.Next()
	if !ok {
		m.player.Stop()
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
		track, idx := m.playlist.Current()
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
		m.status.Show("No available tracks", statusTTLDefault)
		return nil
	}
	if activation.Skipped {
		m.status.Show("Track unavailable, skipping...", statusTTLDefault)
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
	m.netSearch = netSearchState{}
	m.focus = m.prevFocus
}

// closeSpotSearch fully resets the Spotify search overlay, dropping cached
// results, playlists, and the selected track.
func (m *Model) closeSpotSearch() {
	m.spotSearch = spotSearchState{}
}

// queueTrackNext adds a track to the playlist and queues it to play next.
func (m *Model) queueTrackNext(track playlist.Track) tea.Cmd {
	m.playlist.Add(track)
	idx := m.playlist.Len() - 1
	m.playlist.Queue(idx)
	m.status.Showf(statusTTLMedium, "Queued: %s", track.DisplayName())
	if !m.player.IsPlaying() {
		cmd := m.nextTrack()
		m.notifyPlayback()
		return cmd
	}
	return nil
}

// playTrack plays a track, using async HTTP for streams and sync I/O for local files.
// yt-dlp URLs are streamed via a piped yt-dlp | ffmpeg chain for instant playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	if track.Feed || playlist.IsFeed(track.Path) {
		m.feedLoading = true
		m.status.Show("Loading feed...", statusTTLLong)
		return resolveFeedTrackCmd(track.Path)
	}

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
	var fetchCmd tea.Cmd
	if m.lyrics.visible && track.Artist != "" && track.Title != "" {
		m.lyrics.loading = true
		m.lyrics.query = track.Artist + "\n" + track.Title
		fetchCmd = fetchLyricsCmd(track.Artist, track.Title)
	}

	// Stream yt-dlp URLs (YouTube, SoundCloud, Bandcamp, etc.) via pipe chain.
	if playlist.IsYTDL(track.Path) {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		dur := time.Duration(track.DurationSecs) * time.Second
		if fetchCmd != nil {
			return tea.Batch(playYTDLStreamCmd(m.player, track.Path, dur), fetchCmd)
		}
		return playYTDLStreamCmd(m.player, track.Path, dur)
	}
	// Fire now-playing notification for Navidrome tracks.
	m.nowPlaying(track)
	dur := time.Duration(track.DurationSecs) * time.Second
	if track.Stream {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		return tea.Batch(playStreamCmd(m.player, track.Path, dur), fetchCmd)
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
	}

	if fetchCmd != nil {
		return tea.Batch(m.preloadNext(), fetchCmd)
	}
	return m.preloadNext()
}

// togglePlayPause starts playback if stopped, or toggles pause if playing.
// For live streams, unpausing reconnects to get current audio instead of
// playing stale data sitting in OS/decoder buffers from before the pause.
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
		track, idx := m.playlist.Current()
		if shouldReconnectOnUnpause(track, idx) {
			m.player.Stop()
			return m.playTrack(track)
		}
	}
	m.player.TogglePause()
	return nil
}

// shouldReconnectOnUnpause reports whether unpausing should reconnect and
// restart instead of resuming buffered audio.
func shouldReconnectOnUnpause(track playlist.Track, idx int) bool {
	return idx >= 0 && track.IsLive()
}

// applyResume seeks to the saved resume position if the current track matches.
// It clears the resume state after a successful seek so it only fires once.
func (m *Model) applyResume() {
	// secs == 0 is indistinguishable from "never played"; skip resume.
	if m.resume.path == "" || m.resume.secs <= 0 {
		return
	}
	track, _ := m.playlist.Current()
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
