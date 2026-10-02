package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// capableProv reports playback only for the tracks whose path is its path.
type capableProv struct {
	plainProv
	path string
}

func (p *capableProv) CanReportPlayback(track playlist.Track) bool { return track.Path == p.path }
func (p *capableProv) ReportNowPlaying(playlist.Track, time.Duration, bool) error {
	return nil
}
func (p *capableProv) ReportScrobble(playlist.Track, time.Duration, time.Duration, bool) error {
	return nil
}

// findCapable prefers the active provider, then takes the providers in
// order, and skips a provider that is not configured.
func TestFindCapable(t *testing.T) {
	a := &capableProv{path: "a"}
	b := &capableProv{path: "b"}
	other := &capableProv{path: "a"}
	entries := []provider.Entry{
		{Key: "none", Name: "None"},
		{Key: "plain", Name: "Plain", Provider: &plainProv{}},
		{Key: "a", Name: "A", Provider: a},
		{Key: "b", Name: "B", Provider: b},
		{Key: "other", Name: "Other", Provider: other},
	}
	tests := []struct {
		name     string
		active   playlist.Provider
		path     string
		want     provider.PlaybackReporter
		wantName string
	}{
		{name: "first entry in order", path: "a", want: a, wantName: "A"},
		{name: "a later entry", path: "b", want: b, wantName: "B"},
		{name: "the active provider first", active: other, path: "a", want: other, wantName: "Plain"},
		{name: "an active provider that does not match", active: b, path: "a", want: a, wantName: "A"},
		{name: "no match", path: "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{provider: tt.active, providers: entries}
			got, name := findCapable(m, func(r provider.PlaybackReporter) bool {
				return r.CanReportPlayback(playlist.Track{Path: tt.path})
			})
			if got != tt.want || name != tt.wantName {
				t.Fatalf("findCapable = (%v, %q), want (%v, %q)", got, name, tt.want, tt.wantName)
			}
		})
	}
}
