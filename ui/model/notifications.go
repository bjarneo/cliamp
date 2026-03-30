package model

import (
	"strings"
	"time"

	"cliamp/luaplugin"
	"cliamp/mpris"
	"cliamp/playlist"
	"cliamp/provider"
)

// notifyAll sends the current playback state to both MPRIS and Lua plugins.
func (m *Model) notifyAll() {
	m.notifyMPRIS()
	m.notifyPlugins()
}

// notifyPlugins emits a playback state event to Lua plugins.
func (m *Model) notifyPlugins() {
	if m.luaMgr == nil || !m.luaMgr.HasHooks() {
		return
	}
	track, _ := m.playlist.Current()
	artist, title := m.resolveTrackDisplay(track)
	status := "stopped"
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = "paused"
		} else {
			status = "playing"
		}
	}
	data := trackToMap(track)
	data["status"] = status
	data["title"] = title
	data["artist"] = artist
	data["position"] = m.player.Position().Seconds()
	m.luaMgr.Emit(luaplugin.EventPlaybackState, data)
}

// resolveTrackDisplay returns the display artist and title, applying ICY
// stream title override for radio streams.
func (m *Model) resolveTrackDisplay(track playlist.Track) (artist, title string) {
	artist, title = track.Artist, track.Title
	if m.streamTitle != "" && track.Stream {
		if a, t, ok := strings.Cut(m.streamTitle, " - "); ok {
			artist, title = a, t
		} else {
			title = m.streamTitle
		}
	}
	return
}

// trackToMap builds a metadata map from a track for Lua plugin events.
func trackToMap(track playlist.Track) map[string]any {
	return map[string]any{
		"title":    track.Title,
		"artist":   track.Artist,
		"album":    track.Album,
		"genre":    track.Genre,
		"year":     track.Year,
		"path":     track.Path,
		"duration": track.DurationSecs,
		"stream":   track.Stream,
	}
}

// notifyMPRIS sends the current playback state to the MPRIS service
// so desktop widgets and playerctl stay in sync.
func (m *Model) notifyMPRIS() {
	if m.mpris == nil {
		return
	}
	status := "Stopped"
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = "Paused"
		} else {
			status = "Playing"
		}
	}
	track, _ := m.playlist.Current()
	artist, title := m.resolveTrackDisplay(track)
	info := mpris.TrackInfo{
		Title:       title,
		Artist:      artist,
		Album:       track.Album,
		Genre:       track.Genre,
		TrackNumber: track.TrackNumber,
		URL:         track.Path,
		Length:      m.player.Duration().Microseconds(),
	}
	m.mpris.Update(status, info, m.player.Volume(),
		m.player.Position().Microseconds(), m.player.Seekable())
}

// nowPlaying fires a now-playing notification for the given track if configured.
func (m *Model) nowPlaying(track playlist.Track) {
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		m.luaMgr.Emit(luaplugin.EventTrackChange, trackToMap(track))
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return
	}
	canSeek := m.player.Seekable()
	go reporter.ReportNowPlaying(track, m.player.Position(), canSeek)
}

// maybeScrobble fires a playback-complete report for the given track if all
// conditions are met:
//   - a provider claims the track via provider metadata
//   - the track reached at least 50% of its known duration
//
// The call is dispatched in a goroutine so it never blocks the UI.
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) {
	// Emit scrobble event to Lua plugins for all tracks (not just Navidrome).
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		dur := duration
		if dur <= 0 {
			dur = time.Duration(track.DurationSecs) * time.Second
		}
		if dur > 0 && elapsed >= dur/2 {
			data := trackToMap(track)
			data["played_secs"] = elapsed.Seconds()
			m.luaMgr.Emit(luaplugin.EventTrackScrobble, data)
		}
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return
	}
	if duration <= 0 {
		// Unknown duration: use DurationSecs metadata as fallback.
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	if duration <= 0 {
		return // still unknown — skip
	}
	if elapsed < duration/2 {
		return // less than 50% played
	}
	canSeek := m.player.Seekable()
	go reporter.ReportScrobble(track, elapsed, duration, canSeek)
}

// findPlaybackReporter returns the first registered provider that can report
// playback for the given track.
func (m *Model) findPlaybackReporter(track playlist.Track) provider.PlaybackReporter {
	match := func(p playlist.Provider) provider.PlaybackReporter {
		reporter, ok := p.(provider.PlaybackReporter)
		if !ok || !reporter.CanReportPlayback(track) {
			return nil
		}
		return reporter
	}

	if reporter := match(m.provider); reporter != nil {
		return reporter
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if reporter := match(pe.Provider); reporter != nil {
			return reporter
		}
	}
	return nil
}
