package podcast

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func episodeTrack(guid, path string) playlist.Track {
	meta := map[string]string{provider.MetaPodcastFeed: "https://example.com/feed"}
	if guid != "" {
		meta[provider.MetaPodcastGUID] = guid
	}
	return playlist.Track{Path: path, Title: "Ep", Stream: true, ProviderMeta: meta}
}

func TestEpisodeKey(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  string
	}{
		{"feed scopes the guid", episodeTrack("guid-1", "https://cdn.example.com/a.mp3"), "https://example.com/feed" + keySep + "guid-1"},
		{"falls back to path", episodeTrack("", "https://cdn.example.com/a.mp3"), "https://example.com/feed" + keySep + "https://cdn.example.com/a.mp3"},
		{"not an episode", playlist.Track{Path: "https://stream.example.com/live.mp3"}, ""},
		{"blank feed is not an episode", playlist.Track{
			Path:         "https://cdn.example.com/a.mp3",
			ProviderMeta: map[string]string{provider.MetaPodcastFeed: "  "},
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := episodeKey(tt.track); got != tt.want {
				t.Errorf("episodeKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProgressStoreRecordAndResume(t *testing.T) {
	tests := []struct {
		name         string
		position     time.Duration
		duration     time.Duration
		wantPlayed   bool
		wantPosition time.Duration
		wantResume   time.Duration
	}{
		{"mid episode", 20 * time.Minute, time.Hour, false, 20 * time.Minute, 20*time.Minute - resumeRewind},
		{"inside the tail counts as played", time.Hour - 30*time.Second, time.Hour, true, 0, 0},
		{"exactly at the tail edge", time.Hour - playedTail, time.Hour, true, 0, 0},
		{"just before the tail", time.Hour - playedTail - time.Second, time.Hour, false, time.Hour - playedTail - time.Second, time.Hour - playedTail - time.Second - resumeRewind},
		{"too early to resume", 5 * time.Second, time.Hour, false, 5 * time.Second, 0},
		{"unknown duration never plays out", 20 * time.Minute, 0, false, 20 * time.Minute, 20*time.Minute - resumeRewind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			s := newProgressStore()
			track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")
			s.record(track, tt.position, tt.duration)

			state, ok := s.state(track)
			if !ok {
				t.Fatal("state() returned no entry for a recorded episode")
			}
			if state.Played != tt.wantPlayed {
				t.Errorf("Played = %v, want %v", state.Played, tt.wantPlayed)
			}
			if state.Position != tt.wantPosition {
				t.Errorf("Position = %v, want %v", state.Position, tt.wantPosition)
			}
			if got := s.resumeAt(track); got != tt.wantResume {
				t.Errorf("resumeAt() = %v, want %v", got, tt.wantResume)
			}
		})
	}
}

func TestProgressStoreUsesTrackDurationWhenUnreported(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	s := newProgressStore()
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")
	track.DurationSecs = 3600

	s.record(track, time.Hour-10*time.Second, 0)

	state, ok := s.state(track)
	if !ok {
		t.Fatal("state() returned no entry")
	}
	if !state.Played {
		t.Error("Played = false; the track's own duration should mark the tail as played")
	}
}

func TestProgressStoreReplayClearsPlayed(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	s := newProgressStore()
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")

	s.record(track, time.Hour, time.Hour)
	s.record(track, 4*time.Minute, time.Hour)

	state, ok := s.state(track)
	if !ok {
		t.Fatal("state() returned no entry")
	}
	if state.Played {
		t.Error("Played = true after replaying from the middle, want false")
	}
	if state.Position != 4*time.Minute {
		t.Errorf("Position = %v, want 4m0s", state.Position)
	}
}

func TestProgressStoreIgnoresNonEpisodes(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	s := newProgressStore()
	radio := playlist.Track{Path: "https://stream.example.com/live.mp3", Stream: true}

	s.record(radio, 10*time.Minute, 0)

	if _, ok := s.state(radio); ok {
		t.Error("state() returned an entry for a track that is not an episode")
	}
	if s.has() {
		t.Error("has() = true after recording only a non-episode")
	}
}

func TestProgressStorePersistsAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")

	s := newProgressStore()
	s.record(track, 20*time.Minute, time.Hour)
	s.flush()

	if _, err := os.Stat(filepath.Join(dir, "podcast_progress.json")); err != nil {
		t.Fatalf("progress file not written: %v", err)
	}

	reloaded := newProgressStore()
	if reloaded.loadErr != nil {
		t.Fatalf("loadErr = %v", reloaded.loadErr)
	}
	state, ok := reloaded.state(track)
	if !ok {
		t.Fatal("state() returned no entry after reload")
	}
	if state.Position != 20*time.Minute {
		t.Errorf("Position = %v, want 20m0s", state.Position)
	}
}

func TestProgressStoreCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "podcast_progress.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newProgressStore()

	if s.loadErr == nil {
		t.Error("loadErr = nil for a corrupt store, want an error")
	}
	if s.has() {
		t.Error("has() = true after a failed load")
	}
}

func TestProgressStorePrunesOldestFirst(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	s := newProgressStore()
	now := time.Now().Unix()
	for i := range maxStoredEpisodes + 10 {
		key := fmt.Sprintf("feed%s%d", keySep, i)
		s.episodes[key] = episodeState{UpdatedUnix: now + int64(i)}
		s.aliases[fmt.Sprintf("title:show%s%d", keySep, i)] = key
	}
	oldest := fmt.Sprintf("feed%s%d", keySep, 0)
	s.aliases["title:ambiguous"] = ambiguousAlias

	s.prune()

	if len(s.episodes) != maxStoredEpisodes {
		t.Errorf("episodes = %d, want %d", len(s.episodes), maxStoredEpisodes)
	}
	if _, ok := s.episodes[oldest]; ok {
		t.Error("the least recently updated episode survived pruning")
	}
	// Aliases follow their episodes out, so none is left pointing at nothing.
	for title, target := range s.aliases {
		if target == ambiguousAlias {
			continue
		}
		if _, ok := s.episodes[target]; !ok {
			t.Errorf("alias %q points at pruned episode %q", title, target)
		}
	}
	if s.aliases["title:ambiguous"] != ambiguousAlias {
		t.Error("the ambiguous marker was pruned")
	}
}

func TestProviderProgressRoundTrip(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")

	if !p.CanReportPlayback(track) || !p.CanTrackPosition(track) {
		t.Fatal("provider does not claim a podcast episode")
	}
	if got := p.TrackPosition(track); got != 0 {
		t.Errorf("TrackPosition() = %v before anything was reported, want 0", got)
	}
	if err := p.ReportProgress(track, 20*time.Minute); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	if got, want := p.TrackPosition(track), 20*time.Minute-resumeRewind; got != want {
		t.Errorf("TrackPosition() = %v, want %v", got, want)
	}
	if !p.HasPlaybackState() {
		t.Error("HasPlaybackState() = false after a report")
	}
	state, ok := p.PlaybackState(track)
	if !ok || state.Played || state.Position != 20*time.Minute {
		t.Errorf("PlaybackState() = %+v, %v; want a 20m unplayed position", state, ok)
	}
}

// ReportNowPlaying fires with the offset TrackPosition just returned, so it
// must not write that back and shorten the stored position.
func TestProviderNowPlayingKeepsStoredPosition(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")

	if err := p.ReportProgress(track, 20*time.Minute); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	if err := p.ReportNowPlaying(track, 0, true); err != nil {
		t.Fatalf("ReportNowPlaying: %v", err)
	}

	state, ok := p.PlaybackState(track)
	if !ok {
		t.Fatal("PlaybackState() returned no entry")
	}
	if state.Position != 20*time.Minute {
		t.Errorf("Position = %v, want 20m0s", state.Position)
	}
}

func TestProviderScrobbleMarksPlayed(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	track := episodeTrack("guid-1", "https://cdn.example.com/a.mp3")

	if err := p.ReportScrobble(track, time.Hour, time.Hour, true); err != nil {
		t.Fatalf("ReportScrobble: %v", err)
	}

	state, ok := p.PlaybackState(track)
	if !ok || !state.Played {
		t.Errorf("PlaybackState() = %+v, %v; want played", state, ok)
	}
	if got := p.TrackPosition(track); got != 0 {
		t.Errorf("TrackPosition() = %v for a played episode, want 0", got)
	}
}

func TestProviderIgnoresForeignTracks(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	radio := playlist.Track{Path: "https://stream.example.com/live.mp3", Stream: true}

	if p.CanReportPlayback(radio) || p.CanTrackPosition(radio) {
		t.Error("provider claimed a track that is not a podcast episode")
	}
	if _, ok := p.PlaybackState(radio); ok {
		t.Error("PlaybackState() claimed a track that is not a podcast episode")
	}
}

func restoredTrack(show, title string) playlist.Track {
	// What a saved playlist gives back: a URL, a show, a title, no metadata.
	return playlist.Track{
		Path:         "https://cdn.example.com/rewritten-" + title + ".mp3",
		Title:        title,
		Album:        show,
		Stream:       true,
		DurationSecs: 3600,
	}
}

func TestTitleKey(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{"show and title", restoredTrack("Part Of The Problem", "Netanyahu Knew"), true},
		{"no show", playlist.Track{Title: "Orphan"}, false},
		{"no title", playlist.Track{Album: "Some Show"}, false},
		{"blank both", playlist.Track{Album: "  ", Title: "\t"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := titleKey(tt.track) != ""; got != tt.want {
				t.Errorf("titleKey(%+v) non-empty = %v, want %v", tt.track, got, tt.want)
			}
		})
	}
}

func TestTitleKeyIgnoresCaseAndPadding(t *testing.T) {
	a := titleKey(playlist.Track{Album: "Part Of The Problem", Title: "Netanyahu Knew"})
	b := titleKey(playlist.Track{Album: "  part of the problem ", Title: "NETANYAHU KNEW"})
	if a != b {
		t.Errorf("keys differ across case and padding:\n%q\n%q", a, b)
	}
}

// The whole point: an episode played from a feed must be found again after a
// saved playlist strips its metadata and its URL has been rewritten.
func TestPositionSurvivesLosingTheMetadata(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")

	fromFeed := episodeTrack("guid-1", "https://cdn.example.com/original.mp3")
	fromFeed.Album = "Part Of The Problem"
	fromFeed.Title = "Netanyahu Knew"
	if err := p.ReportProgress(fromFeed, 20*time.Minute); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}

	restored := restoredTrack("Part Of The Problem", "Netanyahu Knew")

	if !p.CanTrackPosition(restored) {
		t.Fatal("CanTrackPosition() = false for an episode the store already knows")
	}
	if got, want := p.TrackPosition(restored), 20*time.Minute-resumeRewind; got != want {
		t.Errorf("TrackPosition() = %v, want %v", got, want)
	}
	state, ok := p.PlaybackState(restored)
	if !ok || state.Position != 20*time.Minute {
		t.Errorf("PlaybackState() = %+v, %v; want the stored 20m", state, ok)
	}
}

// A track the store has never seen must not be claimed, so radio streams and
// library tracks are never mistaken for episodes.
func TestUnknownTracksAreNotClaimed(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")

	tests := []struct {
		name  string
		track playlist.Track
	}{
		{"a radio stream", playlist.Track{Path: "https://ice1.somafm.com/groovesalad", Title: "Groove Salad", Stream: true}},
		{"a library track", playlist.Track{Path: "https://nas.local/stream/42", Title: "So What", Album: "Kind of Blue"}},
		{"an unheard episode", restoredTrack("Some Show", "Never Played")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p.CanReportPlayback(tt.track) || p.CanTrackPosition(tt.track) {
				t.Error("provider claimed a track it has never recorded")
			}
			if err := p.ReportProgress(tt.track, 5*time.Minute); err != nil {
				t.Fatalf("ReportProgress: %v", err)
			}
			if _, ok := p.PlaybackState(tt.track); ok {
				t.Error("a position was recorded for a track the provider does not track")
			}
		})
	}
}

// Once known by title, a restored episode keeps recording where it stopped.
func TestRestoredEpisodeKeepsRecording(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	fromFeed := episodeTrack("guid-1", "https://cdn.example.com/original.mp3")
	fromFeed.Album = "Part Of The Problem"
	fromFeed.Title = "Netanyahu Knew"
	if err := p.ReportProgress(fromFeed, 5*time.Minute); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}

	restored := restoredTrack("Part Of The Problem", "Netanyahu Knew")
	if err := p.ReportProgress(restored, 30*time.Minute); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}

	state, ok := p.PlaybackState(restored)
	if !ok || state.Position != 30*time.Minute {
		t.Errorf("PlaybackState() = %+v, %v; want 30m", state, ok)
	}
	// The original identity must see the same advance, not a stale 5m.
	if got, ok := p.PlaybackState(fromFeed); !ok || got.Position != 30*time.Minute {
		t.Errorf("PlaybackState(fromFeed) = %+v, %v; want 30m", got, ok)
	}
}

// Two episodes with one title cannot be told apart, so the title must claim
// neither: a restored track with that title is not recognized, and neither
// episode's position leaks onto the other.
func TestTitleSharedByTwoEpisodesIsAmbiguous(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New("us")
	first := episodeTrack("guid-1", "https://cdn.example.com/1.mp3")
	first.Album, first.Title = "Some Show", "Bonus Episode"
	second := episodeTrack("guid-2", "https://cdn.example.com/2.mp3")
	second.Album, second.Title = "Some Show", "Bonus Episode"

	if err := p.ReportProgress(first, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := p.ReportProgress(second, 40*time.Minute); err != nil {
		t.Fatal(err)
	}

	restored := restoredTrack("Some Show", "Bonus Episode")
	if p.CanTrackPosition(restored) {
		t.Error("an ambiguous title was resolved to an episode")
	}
	// Each episode keeps its own position under its own key.
	if got, _ := p.PlaybackState(first); got.Position != 10*time.Minute {
		t.Errorf("first = %v, want 10m", got.Position)
	}
	if got, _ := p.PlaybackState(second); got.Position != 40*time.Minute {
		t.Errorf("second = %v, want 40m", got.Position)
	}
}

// A version 1 file, a flat map keyed by bare GUID, migrates to feed-scoped
// keys; its title keys named no episode and are dropped.
func TestProgressStoreMigratesVersionOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	v1 := `{
	  "guid-1": {"feed": "https://example.com/feed", "position_sec": 1200, "updated_unix": 1},
	  "title:some show\u0000old title": {"position_sec": 300, "updated_unix": 1}
	}`
	if err := os.WriteFile(filepath.Join(dir, "podcast_progress.json"), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newProgressStore()

	if s.loadErr != nil {
		t.Fatalf("loadErr = %v", s.loadErr)
	}
	state, ok := s.state(episodeTrack("guid-1", "https://cdn.example.com/a.mp3"))
	if !ok || state.Position != 20*time.Minute {
		t.Errorf("migrated state = %+v, %v; want 20m under the feed-scoped key", state, ok)
	}
	if len(s.aliases) != 0 {
		t.Errorf("aliases = %v, want none carried over from version 1", s.aliases)
	}
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "podcast_progress.json"))
	var file progressFile
	if err := json.Unmarshal(data, &file); err != nil || file.Version != progressFormat {
		t.Errorf("rewritten file version = %d (%v), want %d", file.Version, err, progressFormat)
	}
}

// A file that cannot be read is left alone: no write may replace it with the
// empty state that the failure left in memory.
func TestProgressStoreNeverOverwritesAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	path := filepath.Join(dir, "podcast_progress.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New("us")

	if err := p.ReportScrobble(episodeTrack("guid-1", "https://cdn.example.com/a.mp3"), time.Hour, time.Hour, true); err != nil {
		t.Fatalf("ReportScrobble: %v", err)
	}
	p.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Errorf("file was rewritten to %q; the unreadable original should have been kept", data)
	}
}

// A failed write is reported, not swallowed, so the listener can act on it.
func TestScrobbleReportsAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	p := New("us")
	// Make the target path a directory so the atomic write cannot replace it.
	if err := os.Mkdir(filepath.Join(dir, "podcast_progress.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := p.ReportScrobble(episodeTrack("guid-1", "https://cdn.example.com/a.mp3"), time.Hour, time.Hour, true)

	if err == nil {
		t.Fatal("ReportScrobble returned nil for a write that cannot succeed")
	}
	if !strings.Contains(err.Error(), "podcast progress") {
		t.Errorf("error = %q, want it to name the store", err)
	}
}
