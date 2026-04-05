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

	nextModel, cmd := m.Update(AttachNotifier(notifier))
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want Model", nextModel)
	}
	if next.notifier != notifier {
		t.Fatal("notifier was not attached to model")
	}
	if len(notifier.updates) != 1 {
		t.Fatalf("notifier update count = %d, want 1", len(notifier.updates))
	}

	got := notifier.updates[0]
	if got.Status != playback.StatusPlaying {
		t.Fatalf("notifier status = %q, want %q", got.Status, playback.StatusPlaying)
	}
	if got.Track.Title != "Song" {
		t.Fatalf("notifier title = %q, want %q", got.Track.Title, "Song")
	}
	if got.Track.Artist != "Artist" {
		t.Fatalf("notifier artist = %q, want %q", got.Track.Artist, "Artist")
	}
	if got.Track.Album != "Album" {
		t.Fatalf("notifier album = %q, want %q", got.Track.Album, "Album")
	}
	if got.Track.URL != "/tmp/song.mp3" {
		t.Fatalf("notifier url = %q, want %q", got.Track.URL, "/tmp/song.mp3")
	}
}
