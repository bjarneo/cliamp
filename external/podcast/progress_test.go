package podcast

import (
	"os"
	"path/filepath"
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
		{"guid wins", episodeTrack("guid-1", "https://cdn.example.com/a.mp3"), "guid-1"},
		{"falls back to path", episodeTrack("", "https://cdn.example.com/a.mp3"), "https://cdn.example.com/a.mp3"},
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
		s.entries[string(rune('a'+i%26))+string(rune(i))] = episodeState{UpdatedUnix: now + int64(i)}
	}
	oldest := string(rune('a')) + string(rune(0))

	s.prune()

	if len(s.entries) != maxStoredEpisodes {
		t.Errorf("entries = %d, want %d", len(s.entries), maxStoredEpisodes)
	}
	if _, ok := s.entries[oldest]; ok {
		t.Error("the least recently updated entry survived pruning")
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
