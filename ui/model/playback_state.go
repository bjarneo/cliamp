package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func (m Model) currentPlaybackTrack() (playlist.Track, int) {
	if m.buffering && m.pendingTrack != nil {
		return *m.pendingTrack, 0
	}
	if m.playingTrackActive && m.player != nil && m.player.IsPlaying() {
		return m.playingTrack, 0
	}
	if m.playlist == nil {
		return playlist.Track{}, -1
	}
	return m.playlist.Current()
}

func (m Model) currentPlaybackIsLive(track playlist.Track) bool {
	if track.IsLive() {
		return true
	}
	reporter, ok := m.player.(interface{ IsLiveStream() bool })
	return ok && reporter.IsLiveStream()
}

func (m *Model) setPlaybackTrack(track playlist.Track) {
	m.playingTrack = track
	m.playingTrackActive = true
	m.playbackDetached = false
}

func (m *Model) detachPlaybackTrack() {
	if m.pendingTrack != nil {
		m.pendingDetached = true
		if m.playingTrackActive {
			m.playbackDetached = true
		}
		return
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		return
	}
	m.playingTrack = track
	m.playingTrackActive = true
	m.playbackDetached = true
}

func (m *Model) clearPlaybackTrack() {
	m.playingTrack = playlist.Track{}
	m.playingTrackActive = false
	m.playbackDetached = false
}

func (m *Model) failPlaybackTrack() {
	m.pendingTrack = nil
	m.pendingDetached = false
	m.buffering = false
	// Pipeline construction can fail before the engine replaces its source.
	if !m.player.IsPlaying() {
		m.clearPlaybackTrack()
	}
}

// invalidatePlaybackRequest also acknowledges a source that started before its
// completion message reached the UI. The engine freezes old commits while
// changing generations, so the pending request can now be reconciled safely.
func (m *Model) invalidatePlaybackRequest() tea.Cmd {
	previous := m.requests.stream
	nextRequest(&m.requests.stream)
	if m.player == nil {
		return nil
	}
	started := m.player.SetPlaybackGeneration(m.requests.stream)
	if m.pendingTrack == nil || previous == 0 || started != previous {
		return nil
	}
	track, cmd := m.beginPlaybackTrack(*m.pendingTrack)
	m.nowPlaying(track)
	return cmd
}

// stopPlayback stops audio and clears the active track. It also advances the
// stream generation so a yt-dlp or HTTP stream still spinning up for the
// previous track is refused when it becomes ready, instead of starting to play
// seconds after the user stopped or ran past the end of the queue.
func (m *Model) stopPlayback() {
	m.invalidatePlaybackRequest()
	m.player.Stop()
	// The refused stream result would have cleared this; nothing else will.
	m.buffering = false
	m.pendingTrack = nil
	m.pendingDetached = false
	m.clearPlaybackTrack()
}
