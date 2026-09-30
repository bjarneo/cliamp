package playback

import (
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

type (
	PlayPauseMsg   struct{}
	PlayMsg        struct{}
	PauseMsg       struct{}
	NextMsg        struct{}
	PrevMsg        struct{}
	StopMsg        struct{}
	QuitMsg        struct{}
	SeekMsg        struct{ Offset time.Duration }
	SetPositionMsg struct {
		Position time.Duration
	}
	SetVolumeMsg  struct{ VolumeDB float64 }
	SetShuffleMsg struct{ On bool }
	SetRepeatMsg  struct{ Mode playlist.RepeatMode }

	// EnqueueMsg appends a track to the playlist and marks it queued
	// (plays next), matching the UI's "queue next" action.
	EnqueueMsg struct{ Track playlist.Track }

	// PlayTracksMsg replaces the playlist and starts playing Tracks[Index],
	// optionally at Position, optionally paused. It is how remote controllers
	// (Spotify Connect) hand a resolved context to the player.
	// Shuffle and Repeat are nil to leave the playlist's current mode alone.
	// Queue holds Spotify's queue: tracks appended after Tracks and queued.
	// ContextName is the remote context label, shown in the status line.
	PlayTracksMsg struct {
		Tracks      []playlist.Track
		Index       int
		Position    time.Duration
		Paused      bool
		Shuffle     *bool
		Repeat      *playlist.RepeatMode
		Queue       []playlist.Track
		ContextName string
	}
)

type Status string

const (
	StatusStopped Status = "Stopped"
	StatusPlaying Status = "Playing"
	StatusPaused  Status = "Paused"
)

type Track struct {
	Title       string
	Artist      string
	Album       string
	Genre       string
	TrackNumber int
	URL         string
	ArtURL      string
	Duration    time.Duration
}

type State struct {
	Status   Status
	Track    Track
	VolumeDB float64
	Position time.Duration
	Seekable bool
	// Shuffle and Repeat mirror the playlist's modes; NextURL is the path of
	// the track queued to play next, if any.
	Shuffle bool
	Repeat  playlist.RepeatMode
	NextURL string
}

type Notifier interface {
	Update(State)
	Seeked(time.Duration)
}
