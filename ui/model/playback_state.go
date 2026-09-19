package model

import "github.com/bjarneo/cliamp/playlist"

func (m Model) currentPlaybackTrack() (playlist.Track, int) {
	if m.playingTrackActive && (m.buffering || (m.player != nil && m.player.IsPlaying())) {
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
	m.playingProvider = ""
}

// stopPlayback stops audio and clears the active track. It also advances the
// stream generation so a yt-dlp or HTTP stream still spinning up for the
// previous track is refused when it becomes ready, instead of starting to play
// seconds after the user stopped or ran past the end of the queue.
func (m *Model) stopPlayback() {
	m.continuation.waiting = false
	nextRequest(&m.requests.stream)
	m.player.SetPlaybackGeneration(m.requests.stream)
	m.player.Stop()
	// The refused stream result would have cleared this; nothing else will.
	m.buffering = false
	m.clearPlaybackTrack()
}
