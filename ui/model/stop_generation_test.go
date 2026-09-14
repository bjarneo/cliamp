package model

import (
	"testing"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// Every path that stops playback must refuse a stream that was still
// spinning up for the stopped track, and leave the model ready to play again.
func TestStopRefusesPendingStreamStart(t *testing.T) {
	track := playlist.Track{Path: "https://example.com/last.mp3", Stream: true, DurationSecs: 200}

	tests := []struct {
		name string
		stop func(m Model) Model
	}{
		{name: "next past the end of the queue", stop: func(m Model) Model {
			m.nextTrack()
			return m
		}},
		{name: "stop message", stop: func(m Model) Model {
			updated, _ := m.Update(playback.StopMsg{})
			return updated.(Model)
		}},
		{name: "remove the playing track from the playlist", stop: func(m Model) Model {
			m.plCursor = 0
			m.removeSelectedFromPlaylist()
			return m
		}},
		{name: "plugin removes the playing track", stop: func(m Model) Model {
			m.removeIndex(0)
			return m
		}},
		{name: "IPC queue.clear", stop: func(m Model) Model {
			reply := make(chan ipc.Response, 1)
			updated, _ := m.Update(ipc.QueueRequestMsg{Op: "queue.clear", Reply: reply})
			return updated.(Model)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			pl := playlist.New()
			pl.Add(track)
			pl.SetIndex(0)
			m := Model{player: engine, playlist: pl}

			m.playTrack(track)
			pending := m.requests.stream
			if engine.playGeneration != pending {
				t.Fatalf("player generation %d after playTrack, want %d", engine.playGeneration, pending)
			}
			if !m.buffering {
				t.Fatal("stream start should leave the model buffering until the pipeline is ready")
			}

			m = tt.stop(m)
			if engine.playGeneration == pending {
				t.Fatal("stopping did not advance the playback generation")
			}
			if engine.playGeneration != m.requests.stream {
				t.Fatalf("player generation %d, model generation %d", engine.playGeneration, m.requests.stream)
			}
			// The refused stream result no longer clears buffering, so the
			// stop must, or play/pause and Enter stay blocked.
			if m.buffering {
				t.Fatal("model still buffering after the stop")
			}

			// The pipeline that was spinning up for the stopped track becomes ready now.
			if err := engine.PlayAtForGeneration(track.Path, 0, 0, pending); err != nil {
				t.Fatal(err)
			}
			if engine.playing {
				t.Fatal("stale stream start began playback after the stop")
			}

			// Paths that keep the track in the playlist must be able to restart it.
			if m.playlist.Len() > 0 && m.togglePlayPause() == nil {
				t.Fatal("play/pause refused to restart playback after the stop")
			}
		})
	}
}
