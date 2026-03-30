package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/playlist"
)

// nextTrack advances to the next playlist track and starts playing it.
// Returns a tea.Cmd for async stream playback.
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
func (m *Model) prevTrack() tea.Cmd {
	if m.player.Position() > 3*time.Second {
		if m.player.Seekable() {
			// Local file or seekable stream: jump back to the beginning.
			m.player.Seek(-m.player.Position())
			return nil
		}
		// Non-seekable stream (e.g. Icecast radio): restart by replaying the URL.
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

// playCurrentTrack starts playing whatever track the playlist cursor points to.
func (m *Model) playCurrentTrack() tea.Cmd {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return nil
	}
	m.titleOff = 0
	return m.playTrack(track)
}

// playTrack plays a track, using async HTTP for streams and sync I/O for local files.
// yt-dlp URLs are streamed via a piped yt-dlp | ffmpeg chain for instant playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
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
		m.err = err
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
