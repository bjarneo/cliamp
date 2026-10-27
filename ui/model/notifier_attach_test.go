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
		player:   &playbackFakeEngine{playing: true, duration: time.Hour},
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
		VolumeMinDB: -50,
	}
	if got := notifier.updates[0]; got != want {
		t.Fatalf("notifier update = %#v, want %#v", got, want)
	}
}

func TestAttachNotifierPublishesArtURL(t *testing.T) {
	// Regression test for https://github.com/bjarneo/cliamp/issues/660:
	// the MPRIS snapshot must carry the track's cover URL so headless
	// mode (`cliamp -d`) exposes mpris:artUrl like the TUI does.
	pl := playlist.New()
	pl.Add(playlist.Track{
		Title:       "Mock Cover Stream",
		Artist:      "Test Artist",
		Album:       "Test Album",
		Path:        "https://music.163.com/#/song?id=2699649656",
		AlbumArtURL: "https://p4.music.126.net/My7NFFe1Pa8SE1mSDmPlyg==/109951170919292311.jpg",
		Stream:      true,
	})

	notifier := &fakeNotifier{}
	m := Model{
		player:   &playbackFakeEngine{playing: true, duration: 389 * time.Second},
		playlist: pl,
	}

	next, _ := m.Update(AttachNotifier(notifier))
	_ = next.(Model)
	if len(notifier.updates) != 1 {
		t.Fatalf("notifier update count = %d, want 1", len(notifier.updates))
	}

	got := notifier.updates[0].Track.ArtURL
	want := "https://p4.music.126.net/My7NFFe1Pa8SE1mSDmPlyg==/109951170919292311.jpg"
	if got != want {
		t.Fatalf("notifier ArtURL = %q, want %q (mpris:artUrl missing in headless mode)", got, want)
	}
}
