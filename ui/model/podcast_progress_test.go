package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// stateProv is a stub provider that keeps listening state locally, the way the
// podcast provider does.
type stateProv struct {
	states map[string]provider.PlaybackState
}

func (p *stateProv) Name() string { return "Stub" }

func (p *stateProv) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

func (p *stateProv) Tracks(string) ([]playlist.Track, error) { return nil, nil }

func (p *stateProv) CanTrackPosition(track playlist.Track) bool {
	_, ok := p.states[track.Path]
	return ok
}

func (p *stateProv) TrackPosition(track playlist.Track) time.Duration {
	return p.states[track.Path].Position
}

func (p *stateProv) HasPlaybackState() bool { return len(p.states) > 0 }

func (p *stateProv) PlaybackState(track playlist.Track) (provider.PlaybackState, bool) {
	state, ok := p.states[track.Path]
	return state, ok
}

func episodeTrack(path string) playlist.Track {
	return playlist.Track{Path: path, Stream: true}
}

// A stored position is where the episode starts, without the UI seeking after
// the fact.
func TestStartPositionUsesStoredEpisodePosition(t *testing.T) {
	prov := &stateProv{states: map[string]provider.PlaybackState{
		"https://cdn/ep1.mp3": {Position: 12 * time.Minute},
	}}
	m := Model{provider: prov}

	if got := m.startPosition(episodeTrack("https://cdn/ep1.mp3"))(); got != 12*time.Minute {
		t.Errorf("startPosition() = %v, want 12m0s", got)
	}
	if got := m.startPosition(episodeTrack("https://cdn/unknown.mp3"))(); got != 0 {
		t.Errorf("startPosition() = %v for an unknown episode, want 0", got)
	}
}

func TestHasPlaybackState(t *testing.T) {
	tests := []struct {
		name string
		prov playlist.Provider
		want bool
	}{
		{"with stored state", &stateProv{states: map[string]provider.PlaybackState{
			"https://cdn/ep1.mp3": {Played: true},
		}}, true},
		{"empty store", &stateProv{states: map[string]provider.PlaybackState{}}, false},
		{"provider without the capability", &plainProv{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{provider: tt.prov}
			if got := m.hasPlaybackState(); got != tt.want {
				t.Errorf("hasPlaybackState() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlaybackStateFrom(t *testing.T) {
	prov := &stateProv{states: map[string]provider.PlaybackState{
		"https://cdn/done.mp3":    {Played: true},
		"https://cdn/partial.mp3": {Position: 5 * time.Minute},
	}}
	m := Model{provider: prov}
	reporters := m.playbackStateReporters()

	tests := []struct {
		name       string
		path       string
		wantOK     bool
		wantPlayed bool
	}{
		{"played episode", "https://cdn/done.mp3", true, true},
		{"partly played episode", "https://cdn/partial.mp3", true, false},
		{"unknown episode", "https://cdn/other.mp3", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := playbackStateFrom(reporters, episodeTrack(tt.path))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if state.Played != tt.wantPlayed {
				t.Errorf("Played = %v, want %v", state.Played, tt.wantPlayed)
			}
		})
	}
}

// A provider that reports state must not be consulted for tracks it does not
// claim, so a mixed playlist keeps radio rows unmarked.
func TestPlaybackStateSkipsForeignTracks(t *testing.T) {
	prov := &stateProv{states: map[string]provider.PlaybackState{
		"https://cdn/ep1.mp3": {Played: true},
	}}
	m := Model{provider: prov}

	if _, ok := playbackStateFrom(m.playbackStateReporters(), playlist.Track{Path: "https://radio/live.mp3"}); ok {
		t.Error("playbackStateFrom claimed a radio stream")
	}
}
