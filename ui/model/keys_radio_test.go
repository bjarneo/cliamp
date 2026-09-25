package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// radioTestProvider can build a station from a track.
type radioTestProvider struct {
	commandsTestProvider
}

func (p *radioTestProvider) TrackRadio(context.Context, string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "spotify:track:r1"}, {Path: "spotify:track:r2"}}, nil
}

func radioTestModel(t *testing.T) Model {
	t.Helper()
	m := keybindingTestModel()
	p := &radioTestProvider{commandsTestProvider{name: "Spotify"}}
	m.provider = p
	m.providers = append(m.providers, ProviderEntry{Key: "spotify", Name: "Spotify", Provider: p})
	m.replacePlayerPlaylist([]playlist.Track{{Path: "spotify:track:seed", Title: "Seed"}})
	m.focus = focusPlaylist
	m.plCursor = 0
	return m
}

func pressRadio(m *Model) tea.Cmd { return m.handleKey(tea.KeyPressMsg{Text: "W"}) }

// A held key repeats and nothing upstream filters it, so while one station is
// being asked for, further presses must not ask again.
func TestRadioKeyIgnoresRepeatsWhileAStationStarts(t *testing.T) {
	m := radioTestModel(t)

	if pressRadio(&m) == nil {
		t.Fatal("the first press did not ask for a station")
	}
	for i := range 5 {
		if pressRadio(&m) != nil {
			t.Fatalf("repeat %d asked for another station while the first was still starting", i+1)
		}
	}
}

// Starting a station opens its first track and preloads the second, and
// Spotify refuses audio keys once enough opens land in a minute, so stations
// can only start so often.
func TestRadioKeySpacesOutStations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ago     time.Duration
		allowed bool
	}{
		{"just started", 0, false},
		{"three seconds ago", 3 * time.Second, false},
		{"just inside the interval", radioInterval - time.Second, false},
		{"past the interval", radioInterval + time.Second, true},
		{"never started", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := radioTestModel(t)
			if tc.name != "never started" {
				m.trackRadio.lastStart = time.Now().Add(-tc.ago)
			}

			cmd := pressRadio(&m)
			if got := cmd != nil; got != tc.allowed {
				t.Fatalf("station requested = %v, want %v", got, tc.allowed)
			}
			if !tc.allowed && !strings.Contains(m.status.text, "Next radio available in") {
				t.Errorf("a refused press said %q, want how long until the next radio", m.status.text)
			}
		})
	}
}

// However a request ends, it is over, so the key must be free again -- a
// superseded request included, or W would stay locked for good. Only a
// station that actually started spends the interval.
func TestRadioRequestFreesTheKeyHoweverItEnds(t *testing.T) {
	seed := playlist.Track{Path: "spotify:track:seed"}
	for _, tc := range []struct {
		name       string
		msg        func(m Model) trackRadioMsg
		wantSpends bool
	}{
		{"started", func(m Model) trackRadioMsg {
			return trackRadioMsg{seed: seed, tracks: []playlist.Track{{Path: "spotify:track:r1"}}, providerName: "Spotify", gen: m.requests.tracks}
		}, true},
		{"failed", func(m Model) trackRadioMsg {
			return trackRadioMsg{seed: seed, providerName: "Spotify", gen: m.requests.tracks, err: errors.New("no station")}
		}, false},
		{"came back empty", func(m Model) trackRadioMsg {
			return trackRadioMsg{seed: seed, providerName: "Spotify", gen: m.requests.tracks}
		}, false},
		{"superseded", func(m Model) trackRadioMsg {
			return trackRadioMsg{seed: seed, tracks: []playlist.Track{{Path: "spotify:track:r1"}}, providerName: "Spotify", gen: m.requests.tracks + 1}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := radioTestModel(t)
			if pressRadio(&m) == nil {
				t.Fatal("the press did not ask for a station")
			}

			next, _ := m.Update(tc.msg(m))
			m = next.(Model)

			if m.trackRadio.starting {
				t.Error("the request ended but W is still locked")
			}
			if spent := !m.trackRadio.lastStart.IsZero(); spent != tc.wantSpends {
				t.Errorf("interval started = %v, want %v", spent, tc.wantSpends)
			}
		})
	}
}

// A station replaces the loaded playlist, so that playlist must stop being the
// active one, or it stays highlighted and queue refreshes treat it as current.
func TestRadioClearsTheActivePlaylist(t *testing.T) {
	m := radioTestModel(t)
	m.activeProviderPlaylistID = "loaded-playlist"
	if pressRadio(&m) == nil {
		t.Fatal("the press did not ask for a station")
	}
	next, _ := m.Update(trackRadioMsg{
		seed:         playlist.Track{Path: "spotify:track:seed"},
		tracks:       []playlist.Track{{Path: "spotify:track:r1"}},
		providerName: "Spotify",
		gen:          m.requests.tracks,
	})
	if got := next.(Model).activeProviderPlaylistID; got != "" {
		t.Errorf("active playlist = %q after a station replaced it, want none", got)
	}
}
