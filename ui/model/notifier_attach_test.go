package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

type fakeNotifier struct {
	updates []playback.State
	seeked  []time.Duration
}

func (f *fakeNotifier) Update(state playback.State) {
	f.updates = append(f.updates, state)
}

func (f *fakeNotifier) Seeked(position time.Duration) {
	f.seeked = append(f.seeked, position)
}

func TestAttachNotifierPublishesCurrentPlaybackState(t *testing.T) {
	pl := playlist.New()
	pl.Add(playlist.Track{
		Title:  "Song",
		Artist: "Artist",
		Album:  "Album",
		Path:   "/tmp/song.mp3",
	})

	notifier := &fakeNotifier{}
	m := Model{
		player:   &fakeEngine{},
		playlist: pl,
	}

	next, _ := m.Update(AttachNotifier(notifier))
	nextModel := next.(Model)
	if len(nextModel.notifiers) != 1 || nextModel.notifiers[0] != notifier {
		t.Fatal("notifier was not attached to model")
	}
	if len(notifier.updates) != 1 {
		t.Fatalf("notifier update count = %d, want 1", len(notifier.updates))
	}

	want := playback.State{
		Status: playback.StatusPlaying,
		Track: playback.Track{
			Title:    "Song",
			Artist:   "Artist",
			Album:    "Album",
			URL:      "/tmp/song.mp3",
			Duration: time.Hour,
		},
	}
	if got := notifier.updates[0]; got != want {
		t.Fatalf("notifier update = %#v, want %#v", got, want)
	}
}

func TestAttachedNotifiersFanOut(t *testing.T) {
	pl := playlist.New()
	pl.Add(playlist.Track{
		Title: "Song",
		Path:  "/tmp/song.mp3",
	})

	first, second := &fakeNotifier{}, &fakeNotifier{}
	m := Model{
		player:   &fakeEngine{},
		playlist: pl,
	}

	next, _ := m.Update(AttachNotifier(first))
	m = next.(Model)
	next, _ = m.Update(AttachNotifier(second))
	m = next.(Model)

	// Attaching the second notifier broadcasts to all attached notifiers.
	if len(first.updates) != 2 {
		t.Fatalf("first notifier updates = %d, want 2", len(first.updates))
	}
	if len(second.updates) != 1 {
		t.Fatalf("second notifier updates = %d, want 1", len(second.updates))
	}

	m.notifyPlayback()
	if len(first.updates) != 3 || len(second.updates) != 2 {
		t.Fatalf("updates = (%d,%d), want (3,2)", len(first.updates), len(second.updates))
	}
}
