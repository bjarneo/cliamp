package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// notifyAll sends one playback snapshot to both OS media controls and Lua
// plugins, so the two cannot disagree on the source and the speaker lock is
// taken once per update.
func (m *Model) notifyAll() {
	wantPlayback, wantPlugins := m.notifier != nil, m.wantsPluginState()
	if !wantPlayback && !wantPlugins {
		return
	}
	track, stats, ok := m.playbackSnapshot()
	if !ok {
		return
	}
	if wantPlayback {
		m.notifyPlaybackWith(track, stats)
	}
	if wantPlugins {
		m.notifyPluginsWith(track, stats)
	}
}

func (m *Model) attachNotifier(notifier playback.Notifier) {
	m.notifier = notifier
	m.notifyAll()
}

func (m *Model) wantsPluginState() bool {
	return m.luaMgr != nil && m.luaMgr.HasHooks()
}

// notifyPluginsWith emits a playback state event to Lua plugins.
func (m *Model) notifyPluginsWith(track playlist.Track, stats player.PlaybackStats) {
	artist, title := m.resolveTrackDisplay(track)
	status := "stopped"
	if stats.Playing {
		if stats.Paused {
			status = "paused"
		} else {
			status = "playing"
		}
	}
	data := trackToMap(track)
	data["status"] = status
	data["title"] = title
	data["artist"] = artist
	data["position"] = stats.Position.Seconds()
	m.luaMgr.Emit(luaplugin.EventPlaybackState, data)
}

// resolveTrackDisplay returns the display artist and title, applying ICY
// stream title override for radio streams.
func (m *Model) resolveTrackDisplay(track playlist.Track) (artist, title string) {
	artist, title = track.Artist, track.Title
	if m.streamTitle != "" && track.Stream && (m.pending == nil || (m.playing != nil && track.Path == m.playing.track.Path)) {
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
	if track, stats, ok := m.playbackSnapshot(); ok {
		m.notifyPlaybackWith(track, stats)
	}
}

func (m *Model) notifyPlaybackWith(track playlist.Track, stats player.PlaybackStats) {
	status := playback.StatusStopped
	if stats.Playing {
		if stats.Paused {
			status = playback.StatusPaused
		} else {
			status = playback.StatusPlaying
		}
	}
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
			Duration:    stats.Duration,
		},
		VolumeDB: m.player.Volume(),
		Position: stats.Position,
		Seekable: stats.Seekable,
	})
}

// nowPlaying fires a now-playing notification for the given track if configured.
func (m *Model) nowPlaying(track playlist.Track, position time.Duration, canSeek bool) {
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		m.luaMgr.Emit(luaplugin.EventTrackChange, trackToMap(track))
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return
	}
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
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration, canSeek bool) {
	if duration <= 0 {
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	if duration <= 0 || elapsed < duration/2 {
		return
	}
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		data := trackToMap(track)
		data["played_secs"] = elapsed.Seconds()
		m.luaMgr.Emit(luaplugin.EventTrackScrobble, data)
	}
	if reporter := m.findPlaybackReporter(track); reporter != nil {
		go func() {
			if err := reporter.ReportScrobble(track, elapsed, duration, canSeek); err != nil {
				applog.Warn("scrobble failed for %q: %v", track.Title, err)
			}
		}()
	}
}

// findTrackPosition returns the provider that can report track's saved
// position, independent of whether it also reports playback.
func (m *Model) findTrackPosition(track playlist.Track) provider.TrackPosition {
	match := func(p playlist.Provider) provider.TrackPosition {
		tp, ok := p.(provider.TrackPosition)
		if !ok {
			return nil
		}
		if !tp.CanTrackPosition(track) {
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
	track, stats, current := m.playbackSnapshot()
	if !current || !stats.Playing || stats.Paused {
		return
	}
	reporter, ok := m.findPlaybackReporter(track).(provider.ProgressReporter)
	if !ok {
		return
	}
	m.lastProgressReport = now
	position := stats.Position
	go func() {
		if err := reporter.ReportProgress(track, position); err != nil {
			applog.Warn("progress report failed for %q: %v", track.Title, err)
		}
	}()
}
