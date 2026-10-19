package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func TestURLShortcutFromProvider(t *testing.T) {
	m := Model{focus: focusProvider}
	m.handleKey(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if !m.urlInput.active {
		t.Fatal("URL input did not open from provider browser")
	}
}

func TestExplicitURLLoadReplacesQueueAndStartsFirstTrack(t *testing.T) {
	for _, playing := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "playing"}[playing], func(t *testing.T) {
			engine := &playbackFakeEngine{playing: playing}
			m := Model{player: engine, playlist: playlist.New(), loadedPlaylist: "old", focus: focusProvider, plCursor: 1}
			m.playlist.Add(playlist.Track{Path: "old.mp3"}, playlist.Track{Path: "other.mp3"})
			m.playlist.SetIndex(1)
			m.requests.url, m.requests.queue = 1, 0
			cmd := m.handleFeedsLoaded(feedsLoadedMsg{tracks: []playlist.Track{{Path: "first.mp3"}, {Path: "next.mp3"}}, loadGen: 1, queueGen: 0, replace: true, autoPlay: true})
			if got := m.playlist.Tracks(); len(got) != 2 || got[0].Path != "first.mp3" {
				t.Fatalf("queue = %v", got)
			}
			if m.plCursor != 0 || m.playlist.Index() != 0 || m.loadedPlaylist != "" || m.focus != focusPlaylist {
				t.Fatal("queue selection was not reset")
			}
			if cmd == nil {
				t.Fatal("new playlist was not started")
			}
		})
	}
}

func TestFailedURLLoadPreservesQueue(t *testing.T) {
	for _, err := range []error{nil, errors.New("unavailable")} {
		engine := &playbackFakeEngine{playing: true}
		m := Model{player: engine, playlist: playlist.New(), requests: requestState{url: 1}}
		m.playlist.Add(playlist.Track{Path: "old.mp3"})
		m.handleFeedsLoaded(feedsLoadedMsg{replace: true, loadGen: 1, queueGen: 0, err: err})
		if m.playlist.Len() != 1 || !engine.playing {
			t.Fatal("empty or failed URL changed queue or playback")
		}
	}
}

func TestStaleURLLoadDoesNotReplaceNewerQueue(t *testing.T) {
	engine := &playbackFakeEngine{playing: true}
	m := Model{player: engine, playlist: playlist.New(), requests: requestState{url: 1}}
	m.playlist.Add(playlist.Track{Path: "old.mp3"})
	msg := feedsLoadedMsg{tracks: []playlist.Track{{Path: "stale.mp3"}}, loadGen: 1, queueGen: 0, replace: true, autoPlay: true}
	m.replacePlaylist([]playlist.Track{{Path: "newer.mp3"}})
	stops, plays := engine.stopCalls, len(engine.playCalls)
	if cmd := m.handleFeedsLoaded(msg); cmd != nil {
		t.Fatal("stale URL load returned a playback command")
	}
	if m.playlist.Len() != 1 || m.playlist.Tracks()[0].Path != "newer.mp3" || !engine.IsPlaying() || engine.stopCalls != stops || len(engine.playCalls) != plays {
		t.Fatal("stale URL load changed the queue or playback")
	}
	msg.loadGen = 0
	if cmd := m.handleFeedsLoaded(msg); cmd != nil {
		t.Fatal("superseded URL load returned a playback command")
	}
}
