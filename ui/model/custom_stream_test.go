package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// customStreamTestProvider claims spotify: URIs like the Spotify provider.
type customStreamTestProvider struct {
	commandsTestProvider
}

func (customStreamTestProvider) URISchemes() []string { return []string{"spotify:"} }

func (customStreamTestProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, errors.New("not used")
}

func (customStreamTestProvider) Authenticate() error { return nil }

func newCustomStreamModel(player *playbackFakeEngine) Model {
	prov := customStreamTestProvider{commandsTestProvider{name: "Spotify"}}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Song", Path: "spotify:track:abc", DurationSecs: 200},
		{Title: "Local", Path: "local.mp3", DurationSecs: 100},
	})
	p.SetIndex(0)
	m := Model{
		player:    player,
		playlist:  p,
		provider:  prov,
		providers: []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: prov}},
		vis:       ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.SetVisualizer("none")
	return m
}

// streamPlayedFrom runs cmd and returns the streamPlayedMsg it produces.
func streamPlayedFrom(t *testing.T, cmd tea.Cmd) streamPlayedMsg {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case streamPlayedMsg:
			return msg
		case tea.BatchMsg:
			pending = append(pending, msg...)
		}
	}
	t.Fatal("command produced no streamPlayedMsg")
	return streamPlayedMsg{}
}

func TestPlayTrackStartsCustomURIOffUpdate(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantAsync bool
	}{
		{name: "spotify track", path: "spotify:track:abc", wantAsync: true},
		{name: "local file", path: "local.mp3", wantAsync: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{}
			m := newCustomStreamModel(player)

			cmd := m.playTrack(playlist.Track{Title: "Song", Path: tt.path, DurationSecs: 200})

			if m.buffering != tt.wantAsync {
				t.Fatalf("buffering = %v, want %v", m.buffering, tt.wantAsync)
			}
			if gotSync := len(player.playCalls) > 0; gotSync == tt.wantAsync {
				t.Fatalf("play calls inside playTrack = %v, want sync start %v", player.playCalls, !tt.wantAsync)
			}
			if !tt.wantAsync {
				return
			}

			msg := streamPlayedFrom(t, cmd)
			if msg.path != tt.path || len(player.playCalls) != 1 {
				t.Fatalf("async start = %+v, play calls = %v", msg, player.playCalls)
			}
			updated, _ := m.Update(msg)
			m = updated.(Model)
			if m.buffering || m.err != nil {
				t.Fatalf("after start: buffering = %v, err = %v", m.buffering, m.err)
			}
		})
	}
}

func TestStreamPlayedNeedsAuthAsksForSignIn(t *testing.T) {
	player := &playbackFakeEngine{}
	m := newCustomStreamModel(player)
	track, _ := m.playlist.Current()
	m.playTrack(track)

	updated, _ := m.Update(streamPlayedMsg{
		path: track.Path,
		gen:  m.requests.stream,
		err:  fmt.Errorf("spotify: stream auth error: %w", playlist.ErrNeedsAuth),
	})
	m = updated.(Model)

	if !m.provSignIn {
		t.Fatal("provSignIn = false, want the sign-in prompt")
	}
	if m.err != nil {
		t.Fatalf("err = %v, want nil so the prompt is not hidden", m.err)
	}
	if !strings.Contains(m.status.text, "Sign-in required") {
		t.Fatalf("status = %q, want a sign-in hint", m.status.text)
	}
}

func TestProviderAuthFailureKeepsSignInPrompt(t *testing.T) {
	m := newCustomStreamModel(&playbackFakeEngine{})
	gen := nextRequest(&m.requests.auth)

	updated, _ := m.Update(provAuthDoneMsg{providerName: "Spotify", gen: gen, err: errors.New("access_denied")})
	m = updated.(Model)
	if !m.provSignIn || m.err == nil {
		t.Fatalf("after failure: provSignIn = %v, err = %v, want prompt and error", m.provSignIn, m.err)
	}

	// Enter retries the sign-in and clears the old error.
	m.focus = focusProvider
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the sign-in prompt returned no command")
	}
	if m.provSignIn || !m.provLoading || m.err != nil {
		t.Fatalf("after retry: provSignIn = %v, provLoading = %v, err = %v", m.provSignIn, m.provLoading, m.err)
	}
}
