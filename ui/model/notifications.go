package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// notifyAll sends the current playback state to both OS media controls and Lua plugins.
func (m *Model) notifyAll() {
	m.notifyPlayback()
	m.notifyPlugins()
}

func (m *Model) attachNotifier(notifier playback.Notifier) {
	m.notifier = notifier
	m.notifyAll()
}

// notifyPlugins emits a playback state event to Lua plugins.
func (m *Model) notifyPlugins() {
	if m.luaMgr == nil || !m.luaMgr.HasHooks() {
		return
	}
	track, _ := m.currentPlaybackTrack()
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
			if t != "" {
				artist, title = a, t
			}
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

func (m *Model) notifyPlayback() {
	if m.notifier == nil {
		return
	}
	status := playback.StatusStopped
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = playback.StatusPaused
		} else {
			status = playback.StatusPlaying
		}
	}
	track, _ := m.currentPlaybackTrack()
	artist, title := m.resolveTrackDisplay(track)
	m.notifier.Update(playback.State{
		Status: status,
		Track: playback.Track{
			Title:       title,
			Artist:      artist,
			Album:       track.Album,
			Genre:       track.Genre,
			TrackNumber: track.TrackNumber,
			URL:         track.Path,
			ArtURL:      track.AlbumArtURL,
			Duration:    m.player.Duration(),
		},
		VolumeDB: m.player.Volume(),
		Position: m.player.Position(),
		Seekable: m.player.Seekable(),
	})
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
	position := m.player.Position()
	go func() {
		if err := reporter.ReportNowPlaying(track, position, canSeek); err != nil {
			applog.Warn("now-playing report failed for %q: %v", track.Title, err)
		}
	}()
}

// recordListenedTrack adds a starting track to local history and refreshes
// any Recently Played surfaces. Called when playback of the track begins so
// the list mirrors what is playing right now, not the previous song.
//
// The write is synchronous so successive entries preserve their ordering on
// disk; the file is small (~30 KB at the 200-entry cap) so latency is
// sub-millisecond.
func (m *Model) recordListenedTrack(track playlist.Track) tea.Cmd {
	if m.historyStore == nil {
		return nil
	}
	if err := m.historyStore.Record(track, time.Now()); err != nil {
		applog.Warn("history record failed for %q: %v", track.Path, err)
		return nil
	}
	if m.plManager.visible {
		m.plMgrRefreshList()
		if m.plManager.screen == plMgrScreenTracks && m.plManager.selPlaylist == history.PlaylistName {
			m.plMgrReloadTracks(history.PlaylistName)
		}
	}
	return m.fetchProviderPlaylists()
}

// maybeScrobble fires a playback-complete report for the given track when it
// is left (skip, stop, natural end) and past 50% of its known duration,
// matching Last.fm-style play-count conventions. Local history is recorded
// separately at track start via recordListenedTrack.
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) tea.Cmd {
	dur := duration
	if dur <= 0 {
		dur = time.Duration(track.DurationSecs) * time.Second
	}
	pastThreshold := dur > 0 && elapsed >= dur/2

	var refresh tea.Cmd

	// Emit scrobble event to Lua plugins for all tracks (not just Navidrome).
	if m.luaMgr != nil && m.luaMgr.HasHooks() && pastThreshold {
		data := trackToMap(track)
		data["played_secs"] = elapsed.Seconds()
		m.luaMgr.Emit(luaplugin.EventTrackScrobble, data)
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return refresh
	}
	if duration <= 0 {
		// Unknown duration: use DurationSecs metadata as fallback.
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	if duration <= 0 {
		return refresh // still unknown — skip
	}
	if elapsed < duration/2 {
		return refresh // less than 50% played
	}
	canSeek := m.player.Seekable()
	go func() {
		if err := reporter.ReportScrobble(track, elapsed, duration, canSeek); err != nil {
			applog.Warn("scrobble failed for %q: %v", track.Title, err)
		}
	}()
	return refresh
}

// findTrackPosition returns the provider that can report track's saved
// position, independent of whether it also reports playback.
func (m *Model) findTrackPosition(track playlist.Track) provider.TrackPosition {
	match := func(p playlist.Provider) provider.TrackPosition {
		tp, ok := p.(provider.TrackPosition)
		if !ok {
			return nil
		}
		return tp
	}

	if tp := match(m.provider); tp != nil {
		return tp
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if tp := match(pe.Provider); tp != nil {
			return tp
		}
	}
	return nil
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

// progressReportInterval bounds how often interim listening positions are
// pushed to providers that accept them.
const progressReportInterval = 15 * time.Second

// tickProgressReport pushes an interim position update for the playing track to
// providers that accept them, at most once per progressReportInterval.
func (m *Model) tickProgressReport(now time.Time) {
	if m.player == nil || !m.player.IsPlaying() || m.player.IsPaused() {
		return
	}
	if !m.lastProgressReport.IsZero() && now.Sub(m.lastProgressReport) < progressReportInterval {
		return
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		return
	}
	reporter, ok := m.findPlaybackReporter(track).(provider.ProgressReporter)
	if !ok {
		return
	}
	m.lastProgressReport = now
	position := m.player.Position()
	go func() {
		if err := reporter.ReportProgress(track, position); err != nil {
			applog.Warn("progress report failed for %q: %v", track.Title, err)
		}
	}()
}
