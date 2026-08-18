package model

import (
	"strings"
	"time"

	"github.com/bjarneo/cliamp/applog"
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

// maybeScrobble fires a playback-complete report for the given track if all
// conditions are met:
//   - a provider claims the track via provider metadata
//   - the track reached at least 50% of its known duration
//
// The call is dispatched in a goroutine so it never blocks the UI. The same
// 50% threshold gates a local history entry so skipped tracks never land in
// "Recently Played".
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) {
	dur := duration
	if dur <= 0 {
		dur = time.Duration(track.DurationSecs) * time.Second
	}
	pastThreshold := dur > 0 && elapsed >= dur/2

	// Emit scrobble event to Lua plugins for all tracks (not just Navidrome).
	if m.luaMgr != nil && m.luaMgr.HasHooks() && pastThreshold {
		data := trackToMap(track)
		data["played_secs"] = elapsed.Seconds()
		m.luaMgr.Emit(luaplugin.EventTrackScrobble, data)
	}

	// Record into local history regardless of provider. Live streams without
	// duration are filtered by pastThreshold. The write is synchronous so
	// successive scrobbles preserve their ordering on disk; the file is small
	// (~30 KB at the 200-entry cap) so the latency is sub-millisecond.
	if pastThreshold && m.historyStore != nil {
		_ = m.historyStore.Record(track, time.Now())
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
	go func() {
		if err := reporter.ReportScrobble(track, elapsed, duration, canSeek); err != nil {
			applog.Warn("scrobble failed for %q: %v", track.Title, err)
		}
	}()
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
