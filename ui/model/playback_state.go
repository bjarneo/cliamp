package model

import (
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

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
		// A yt-dlp live flag is a listing-time snapshot and may be restored from
		// a favorite or saved playlist. Once the broadcast ends the same URL
		// serves a finite recording, and the player then reports its duration.
		return !playlist.IsYTDL(track.Path) || m.player == nil || m.player.Duration() <= 0
	}
	reporter, ok := m.player.(interface{ IsLiveStream() bool })
	return ok && reporter.IsLiveStream()
}

func (m *Model) setPlaybackTrack(track playlist.Track) {
	m.playingTrack = track
	m.playingTrackActive = true
	m.playingTrackStarted = false
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
	m.playingTrackStarted = false
	m.playbackDetached = false
	m.playingProvider = ""
}

// stopPlayback stops audio and clears the active track. It also advances the
// stream generation so a yt-dlp or HTTP stream still spinning up for the
// previous track is refused when it becomes ready, instead of starting to play
// seconds after the user stopped or ran past the end of the queue. It returns
// the track that was playing and whether the engine had started it.
func (m *Model) stopPlayback() (playlist.Track, bool) {
	nextRequest(&m.requests.stream)
	m.player.SetPlaybackGeneration(m.requests.stream)
	m.player.Stop()
	// The refused stream result would have cleared this; nothing else will.
	m.buffering = false
	// A pending reconnect would restart the playlist's current track when its
	// timer fires, and its "reconnecting in" message would stay up.
	if !m.reconnect.at.IsZero() {
		m.err = nil
	}
	m.reconnect = reconnectState{}
	finished, started := m.playingTrack, m.playingTrackActive && m.playingTrackStarted
	m.clearPlaybackTrack()
	return finished, started
}

// endQueue stops playback because nothing follows the current track and tells
// plugins which track finished, so they can tell the end of the queue apart
// from a manual stop. Only a track the engine started is reported: a stream
// still buffering, a failed start, or an empty player only stops.
func (m *Model) endQueue() {
	if finished, started := m.stopPlayback(); started {
		m.emitPlugin(luaplugin.EventQueueEnd, trackToMap(finished))
	}
}
