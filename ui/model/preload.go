package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/playlist"
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

// preloadNext looks ahead in the playlist and preloads the next track for
// gapless transition. Errors are silently ignored — playback falls back to
// non-gapless if preloading fails.
//
// For HTTP streams with a known duration, preloading is deferred until the
// current track is within streamPreloadLeadTime of its end. This prevents the
// gapless streamer from having a live HTTP connection armed too early, which
// would cause the player to skip to the next track if the decoder signals EOF
// prematurely (e.g. a mis-estimated Content-Length from a transcoding server).
// When position has not yet reached the threshold, this function returns nil
// and the tick loop will retry on the next pass.
func (m *Model) preloadNext() tea.Cmd {
	next, ok := m.playlist.PeekNext()
	if !ok {
		return nil
	}
	// Preload yt-dlp tracks with the same lead-time deferral as HTTP streams.
	if playlist.IsYTDL(next.Path) {
		dur := m.player.Duration()
		if dur > 0 {
			remaining := dur - m.player.Position()
			if remaining > ytdlPreloadLeadTime {
				return nil
			}
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		m.preloading = true
		return preloadYTDLStreamCmd(m.player, next.Path, nextDur)
	}
	if next.Stream {
		// For streams, only arm gapless if we're within the lead-time window.
		// If we don't know the duration yet (0), preload immediately as before
		// so that streams without duration metadata still get gapless behaviour.
		dur := m.player.Duration()
		if dur > 0 {
			pos := m.player.Position()
			remaining := dur - pos
			if remaining > streamPreloadLeadTime {
				// Too early — caller should retry from the tick loop.
				return nil
			}
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		// Mark in-flight so the tick loop doesn't dispatch a second concurrent
		// preload before this goroutine has finished arming gapless.SetNext.
		m.preloading = true
		return preloadStreamCmd(m.player, next.Path, nextDur)
	}
	nextDur := time.Duration(next.DurationSecs) * time.Second
	m.player.Preload(next.Path, nextDur)
	return nil
}
