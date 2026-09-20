package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

func TestLyricsSyncable(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{
			name:  "local file",
			track: playlist.Track{Title: "Local", Path: "/tmp/a.mp3", DurationSecs: 180},
			want:  true,
		},
		{
			name:  "youtube music finite track",
			track: playlist.Track{Title: "Song", Path: "https://music.youtube.com/watch?v=abc", Stream: true, DurationSecs: 240},
			want:  true,
		},
		{
			name:  "youtube finite track",
			track: playlist.Track{Title: "Song", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 240},
			want:  true,
		},
		{
			name:  "yt-dlp track (soundcloud) finite",
			track: playlist.Track{Title: "SC", Path: "https://soundcloud.com/x/y", Stream: true, DurationSecs: 120},
			want:  true,
		},
		{
			name:  "youtube live (no duration)",
			track: playlist.Track{Title: "Live", Path: "https://music.youtube.com/watch?v=live", Stream: true, DurationSecs: 0},
			want:  false,
		},
		{
			name:  "icy radio stream without provider metadata",
			track: playlist.Track{Title: "Radio", Path: "https://radio.example/stream", Stream: true},
			want:  false,
		},
		{
			name:  "live radio with identity metadata",
			track: playlist.Track{Path: "https://radio.example/stream", Stream: true, Realtime: true, ProviderMeta: map[string]string{"radio.url": "https://radio.example/stream"}},
			want:  false,
		},
		{
			name:  "live radio with descriptive metadata",
			track: playlist.Track{Path: "https://radio.example/stream", Stream: true, Realtime: true, ProviderMeta: map[string]string{"country": "Norway"}},
			want:  false,
		},
		{
			name:  "explicit live status overrides duration",
			track: playlist.Track{Path: "https://www.youtube.com/watch?v=live", Stream: true, Realtime: true, DurationSecs: 240},
			want:  false,
		},
		{
			name:  "navidrome provider stream",
			track: playlist.Track{Title: "Nav", Path: "https://nav.example/stream", Stream: true, DurationSecs: 200, ProviderMeta: map[string]string{"navidrome": "id"}},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := playlist.New()
			p.Replace([]playlist.Track{tt.track})
			p.SetIndex(0)
			m := Model{playlist: p, player: &playbackFakeEngine{playing: true}}
			m.setPlaybackTrack(tt.track)
			if got := m.lyricsSyncable(); got != tt.want {
				t.Fatalf("lyricsSyncable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// assertRadioLyricsScrollable exercises the timed-lyrics key path as well as
// sync eligibility, using tracks produced by the actual radio loading paths.
func assertRadioLyricsScrollable(t *testing.T, track playlist.Track) {
	t.Helper()
	m := keybindingTestModel()
	m.playlist.Replace([]playlist.Track{track})
	m.playlist.SetIndex(0)
	m.lyrics.visible = true
	m.lyrics.lines = []lyrics.Line{
		{Start: time.Second, Text: "First"},
		{Start: 2 * time.Second, Text: "Second"},
	}
	if m.lyricsSyncable() {
		t.Error("live radio lyrics must not follow stream elapsed time")
	}
	for _, keys := range [][]tea.KeyPressMsg{
		{{Text: "j"}, {Text: "k"}},
		{{Code: tea.KeyDown}, {Code: tea.KeyUp}},
	} {
		m.handleKey(keys[0])
		if m.lyrics.scroll != 1 {
			t.Fatalf("%s did not scroll timed radio lyrics down", keys[0].String())
		}
		m.handleKey(keys[1])
		if m.lyrics.scroll != 0 {
			t.Fatalf("%s did not scroll timed radio lyrics up", keys[1].String())
		}
	}
}

func TestRadioLyricsRemainScrollable(t *testing.T) {
	_, _, tracks := radioFavoriteTestModel(t)
	for _, track := range tracks {
		t.Run(track.Title, func(t *testing.T) {
			assertRadioLyricsScrollable(t, track)
		})
	}
}

func TestLyricsSyncableRuntimeLiveStream(t *testing.T) {
	m := keybindingTestModel()
	m.playlist.Replace([]playlist.Track{{
		Path: "https://radio.example/stream", Stream: true,
		ProviderMeta: map[string]string{"title": "Station"},
	}})
	m.playlist.SetIndex(0)
	m.player.(*playbackFakeEngine).live = true
	if m.lyricsSyncable() {
		t.Fatal("runtime-detected live stream must not synchronize lyrics")
	}
}
