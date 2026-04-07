package model

import (
	"testing"
	"time"

	"cliamp/internal/playback"
	"cliamp/playlist"
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
	if nextModel.notifier != notifier {
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
