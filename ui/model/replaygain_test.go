package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// rgFakeEngine reports ReplayGain the way the player does.
type rgFakeEngine struct {
	playbackFakeEngine
	mode    string
	applied float64
	used    string
}

func (f *rgFakeEngine) ReplayGainMode() string               { return f.mode }
func (f *rgFakeEngine) ReplayGainApplied() (float64, string) { return f.applied, f.used }

func TestReplayGainMetadataRow(t *testing.T) {
	local := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	stream := playlist.Track{Path: "http://nas/stream?id=1", Title: "B", Stream: true}
	tests := []struct {
		name    string
		engine  *rgFakeEngine
		track   playlist.Track
		playing bool
		want    string
	}{
		{"off shows nothing", &rgFakeEngine{mode: "off"}, local, true, ""},
		{"playing track: the applied gain", &rgFakeEngine{mode: "track", applied: -10.72, used: "track"}, local, true, "-10.72 dB (track gain)"},
		{"playing track without values", &rgFakeEngine{mode: "album"}, local, true, "none (no ReplayGain values)"},
		{"playing stream: the applied gain", &rgFakeEngine{mode: "album", applied: -9.92, used: "track"}, stream, true, "-9.92 dB (track gain)"},
		{"local track not playing: unknown yet", &rgFakeEngine{mode: "track"}, local, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.engine.playing = tc.playing
			pl := playlist.New()
			pl.Add(tc.track)
			m := Model{player: tc.engine, playlist: pl}
			if tc.playing {
				m.setPlaybackTrack(tc.track)
			}
			var got string
			for _, f := range m.metadataFields() {
				if f.label == "ReplayGain" {
					got = f.value
				}
			}
			if got != tc.want {
				t.Errorf("ReplayGain row = %q, want %q", got, tc.want)
			}
		})
	}
}
