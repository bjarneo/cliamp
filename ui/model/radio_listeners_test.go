package model

import (
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func channelsListenerTestModel() Model {
	m := keybindingTestModel()
	m.player = &playbackFakeEngine{}
	cp := radio.NewChannels()
	m.provider = cp
	m.providers = []provider.Entry{{Key: "cliamp", Name: "cliamp radio", Provider: cp}}
	m.provPillIdx = 0
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "edm", Name: "EDM"}, {ID: "quiet", Name: "Quiet"}}
	m.focus = focusProvider
	return m
}

func TestRadioListenersApplyToRows(t *testing.T) {
	m := channelsListenerTestModel()
	gen := nextRequest(&m.requests.radioListeners)

	m.handleRadioListenersLoaded(radioListenersLoadedMsg{counts: map[string]int{"edm": 3, "quiet": 0}, gen: gen})

	edm := m.providerRowLabel("  ", m.provPane.lists[0])
	if !strings.Contains(edm, "● 3 listening now") {
		t.Errorf("edm row = %q, want the listener count", edm)
	}
	quiet := m.providerRowLabel("  ", m.provPane.lists[1])
	if !strings.Contains(quiet, "○ quiet right now") {
		t.Errorf("quiet row = %q, want the quiet marker", quiet)
	}
	if m.radioListenersAt.IsZero() {
		t.Error("radioListenersAt is zero after a fetch; want the backoff stamped")
	}
}

func TestRadioListenersStaleAndForeignIgnored(t *testing.T) {
	m := channelsListenerTestModel()
	m.handleRadioListenersLoaded(radioListenersLoadedMsg{counts: map[string]int{"edm": 3}, gen: nextRequest(&m.requests.radioListeners)})

	t.Run("stale generation", func(t *testing.T) {
		m.handleRadioListenersLoaded(radioListenersLoadedMsg{counts: map[string]int{"edm": 99}, gen: m.requests.radioListeners - 1})
		if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 3 listening now") {
			t.Errorf("edm row = %q, want the old count kept", got)
		}
	})

	t.Run("other provider", func(t *testing.T) {
		other := commandsTestProvider{name: "Spotify"}
		m.provider = other
		m.handleRadioListenersLoaded(radioListenersLoadedMsg{counts: map[string]int{"edm": 99}, gen: nextRequest(&m.requests.radioListeners)})
		m.provider = m.providers[0].Provider
		if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 3 listening now") {
			t.Errorf("edm row = %q, want the old count kept", got)
		}
	})
}

func TestRadioListenersFailureShowsNothing(t *testing.T) {
	m := channelsListenerTestModel()
	gen := nextRequest(&m.requests.radioListeners)

	m.handleRadioListenersLoaded(radioListenersLoadedMsg{gen: gen})

	if got := m.providerRowLabel("  ", m.provPane.lists[0]); strings.Contains(got, "listening now") || strings.Contains(got, "quiet") {
		t.Errorf("edm row = %q, want no count on failure", got)
	}
	if m.radioListenersAt.IsZero() {
		t.Error("radioListenersAt is zero after a failure; want the backoff stamped")
	}
}

func TestRadioListenersFailureKeepsPreviousCounts(t *testing.T) {
	m := channelsListenerTestModel()
	m.handleRadioListenersLoaded(radioListenersLoadedMsg{counts: map[string]int{"edm": 3}, gen: nextRequest(&m.requests.radioListeners)})

	m.handleRadioListenersLoaded(radioListenersLoadedMsg{gen: nextRequest(&m.requests.radioListeners)})

	if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 3 listening now") {
		t.Errorf("edm row = %q, want the previous count kept", got)
	}
}

func TestRadioListenersOptimisticBump(t *testing.T) {
	newPlaying := func() Model {
		m := channelsListenerTestModel()
		m.activeProviderPlaylistID = "edm"
		m.player.(*playbackFakeEngine).playing = true
		return m
	}

	t.Run("playing channel gains one", func(t *testing.T) {
		m := newPlaying()
		m.radioListeners = map[string]int{"edm": 3}
		if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 4 listening now") {
			t.Errorf("edm row = %q, want the optimistic bump", got)
		}
	})

	t.Run("playing channel without data still counts itself", func(t *testing.T) {
		m := newPlaying()
		if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 1 listening now") {
			t.Errorf("edm row = %q, want a single self count", got)
		}
	})

	t.Run("other rows do not gain", func(t *testing.T) {
		m := newPlaying()
		m.radioListeners = map[string]int{"quiet": 0}
		if got := m.providerRowLabel("  ", m.provPane.lists[1]); !strings.Contains(got, "○ quiet right now") {
			t.Errorf("quiet row = %q, want quiet without a bump", got)
		}
	})

	t.Run("idle channel shows server truth", func(t *testing.T) {
		m := channelsListenerTestModel()
		m.activeProviderPlaylistID = "edm"
		m.radioListeners = map[string]int{"edm": 3}
		if got := m.providerRowLabel("  ", m.provPane.lists[0]); !strings.Contains(got, "● 3 listening now") {
			t.Errorf("edm row = %q, want no bump when idle", got)
		}
	})
}

func TestMaybeFetchRadioListeners(t *testing.T) {
	t.Run("other providers and detached skip", func(t *testing.T) {
		m := keybindingTestModel()
		if cmd := m.maybeFetchRadioListeners(); cmd != nil {
			t.Error("non-channels provider returned a fetch command")
		}
		m = channelsListenerTestModel()
		m.detached = true
		if cmd := m.maybeFetchRadioListeners(); cmd != nil {
			t.Error("detached model returned a fetch command")
		}
	})

	t.Run("fresh cache skips, stale fetches", func(t *testing.T) {
		m := channelsListenerTestModel()
		m.radioListeners = map[string]int{"edm": 1}
		m.radioListenersAt = time.Now()
		if cmd := m.maybeFetchRadioListeners(); cmd != nil {
			t.Error("fresh cache returned a fetch command")
		}
		m.radioListenersAt = time.Now().Add(-radioListenersTTL - time.Minute)
		if cmd := m.maybeFetchRadioListeners(); cmd == nil {
			t.Error("stale cache returned no fetch command")
		}
	})
}

func TestProviderRowLabelPlainForOtherProviders(t *testing.T) {
	m := keybindingTestModel()
	m.radioListeners = map[string]int{"mix": 10}
	info := playlist.PlaylistInfo{ID: "mix", Name: "Mix", TrackCount: 12}
	if got, want := m.providerRowLabel("  ", info), playlistLabel("  ", info); got != want {
		t.Errorf("row = %q, want plain %q", got, want)
	}
}
