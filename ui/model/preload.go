package model

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// streamPreloadLeadTime is how far before the end of a stream we arm the
// gapless next pipeline. Opening the preload HTTP connection too early can
// cause the server to close the current stream (e.g., per-user concurrent
// stream limits on Navidrome), which makes the mp3 decoder error out and
// triggers a premature gapless transition. 3 seconds is short enough that
// most servers won't enforce a concurrency limit for such a brief overlap,
// and any resulting early skip is imperceptible (≤3 s from the true end).
const streamPreloadLeadTime = 3 * time.Second

// ytdlPreloadLeadTime is the lead time used for yt-dlp (YouTube/SoundCloud)
// URLs. These need longer because spinning up the yt-dlp | ffmpeg pipe chain
// takes 3-10 seconds, so we start preloading much earlier.
const ytdlPreloadLeadTime = 15 * time.Second

// rearmPreload discards any armed gapless pipeline and re-arms from the
// current playlist state. Call after any change that alters which track
// plays next.
func (m *Model) rearmPreload() tea.Cmd {
	nextRequest(&m.requests.preload)
	m.preloading = false
	m.player.ClearPreload()
	return m.preloadNext()
}

// preloadNext looks ahead in the playlist and preloads the next track for
// gapless transition. A track whose preload failed is not retried until a new
// track starts; playback falls back to non-gapless for it.
//
// For HTTP streams with a known duration, preloading is deferred until the
// current track is within streamPreloadLeadTime of its end. This prevents the
// gapless streamer from having a live HTTP connection armed too early, which
// would cause the player to skip to the next track if the decoder signals EOF
// prematurely (e.g. a mis-estimated Content-Length from a transcoding server).
// When position has not yet reached the threshold, this function returns nil
// and the tick loop will retry on the next pass.
func (m *Model) preloadNext() tea.Cmd {
	next, ok := m.preloadTarget()
	if !ok || next.Path == m.preloadFailed {
		return nil
	}
	isYTDL := playlist.IsYTDL(next.Path)
	// Preload yt-dlp tracks with the same lead-time deferral as HTTP streams.
	if isYTDL {
		dur := m.player.Duration()
		if dur > 0 {
			remaining := dur - m.player.Position()
			if remaining > ytdlPreloadLeadTime {
				return nil
			}
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		m.preloading, m.preloadFor = true, next.Path
		return preloadYTDLStreamCmd(m.player, next.Path, nextDur, nextRequest(&m.requests.preload), m.player.BeginPreload())
	}
	if next.Stream {
		// For streams, only arm gapless if we're within the lead-time window.
		// Without a known boundary, opening the next connection now can leave it
		// stale for the entire track or pause and turn one EOF into several skips.
		dur := m.player.Duration()
		if dur <= 0 {
			return nil
		}
		pos := m.player.Position()
		remaining := dur - pos
		if remaining > streamPreloadLeadTime {
			// Too early — caller should retry from the tick loop.
			return nil
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		// Mark in-flight so the tick loop doesn't dispatch a second concurrent
		// preload before this goroutine has finished arming gapless.SetNext.
		m.preloading, m.preloadFor = true, next.Path
		return preloadStreamCmd(m.player, next.Path, nextDur, nextRequest(&m.requests.preload), m.player.BeginPreload())
	}
	nextDur := time.Duration(next.DurationSecs) * time.Second
	m.preloading, m.preloadFor = true, next.Path
	return preloadLocalCmd(m.player, next.Path, nextDur, nextRequest(&m.requests.preload), m.player.BeginPreload())
}

// preloadTarget returns the track the gapless pipeline should hold next. It is
// false when nothing should be armed: a live stream is playing, the queue ends
// here, or the next yt-dlp track is the one already playing.
func (m *Model) preloadTarget() (playlist.Track, bool) {
	// Live streams do not have a track boundary. Preloading another station
	// would turn a transient EOF into a gapless switch instead of reconnecting
	// the station the user selected.
	current, currentIdx := m.currentPlaybackTrack()
	if currentIdx >= 0 && m.currentPlaybackIsLive(current) {
		return playlist.Track{}, false
	}
	var next playlist.Track
	var ok bool
	if m.playbackDetached {
		var idx int
		next, idx = m.playlist.Current()
		ok = idx >= 0
	} else {
		next, ok = m.playlist.PeekNext()
	}
	if !ok || (playlist.IsYTDL(next.Path) && currentIdx >= 0 && next.Path == current.Path) {
		return playlist.Track{}, false
	}
	return next, true
}

// dropStalePreload discards an armed or in-flight preload that is no longer
// for the track that plays next, so a queue change can never play the old
// one. Update runs it after every message; the tick loop then arms the new
// next track.
func (m *Model) dropStalePreload() {
	if m.player == nil || m.playlist == nil || (!m.preloading && !m.player.HasPreload()) {
		return
	}
	if next, ok := m.preloadTarget(); ok && next.Path == m.preloadFor {
		return
	}
	m.preloading = false
	m.player.ClearPreload()
}
