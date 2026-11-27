package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeLyricsSource answers for the tracks whose path starts with prefix, as
// the Spotify provider does for spotify:track: paths.
type fakeLyricsSource struct {
	playlist.Provider
	prefix string
	lines  []lyrics.Line
	asked  []string
}

func (f *fakeLyricsSource) TrackLyrics(_ context.Context, track playlist.Track) ([]lyrics.Line, error) {
	f.asked = append(f.asked, track.Path)
	if !strings.HasPrefix(track.Path, f.prefix) {
		return nil, lyrics.ErrNotFound
	}
	return f.lines, nil
}

func TestFetchTrackLyricsCmdSources(t *testing.T) {
	spotifyLines := []lyrics.Line{{Start: time.Second, Text: "spotify line"}}
	otherLines := []lyrics.Line{{Start: time.Second, Text: "other line"}}
	tests := []struct {
		name     string
		track    playlist.Track
		wantText string
	}{
		{name: "the owning source answers", track: playlist.Track{Path: "spotify:track:abc"}, wantText: "spotify line"},
		{name: "a second source answers", track: playlist.Track{Path: "other:track:1"}, wantText: "other line"},
		{name: "embedded lyrics come first", track: playlist.Track{Path: "spotify:track:abc", EmbeddedLyrics: "[00:01.00]embedded"}, wantText: "embedded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := []trackLyricsSource{
				&fakeLyricsSource{prefix: "spotify:track:", lines: spotifyLines},
				&fakeLyricsSource{prefix: "other:track:", lines: otherLines},
			}
			msg := fetchTrackLyricsCmd(context.Background(), tt.track, "Artist", "Title", "Artist\nTitle", 1, sources)().(lyricsLoadedMsg)
			if msg.err != nil {
				t.Fatalf("err = %v, want nil", msg.err)
			}
			if len(msg.lines) != 1 || msg.lines[0].Text != tt.wantText {
				t.Fatalf("lines = %+v, want %q", msg.lines, tt.wantText)
			}
		})
	}
}

// The Model asks every registered provider that has the capability. It
// does not pick a provider by its key.
func TestTrackLyricsSourcesFromProviders(t *testing.T) {
	spotify := &fakeLyricsSource{prefix: "spotify:track:"}
	renamed := &fakeLyricsSource{prefix: "other:track:"}
	m := Model{
		providers: []provider.Entry{
			{Key: "radio", Name: "Radio", Provider: commandsTestProvider{name: "Radio"}},
			{Key: "spotify", Name: "Spotify", Provider: spotify},
			{Key: "spotify-2", Name: "Other", Provider: renamed},
			{Key: "jellyfin", Name: "Jellyfin"}, // not configured: nil Provider
		},
	}
	got := m.trackLyricsSources()
	if len(got) != 2 || got[0] != spotify || got[1] != renamed {
		t.Fatalf("trackLyricsSources() = %v, want the two capable providers", got)
	}
	if got := (Model{}).trackLyricsSources(); len(got) != 0 {
		t.Fatalf("trackLyricsSources() without providers = %v, want none", got)
	}
}

// The lyrics overlay and the IPC lyrics operation use one lookup order, so
// they return the same lyrics for the track that plays.
func TestLyricsOverlayAndIPCAgree(t *testing.T) {
	tests := []struct {
		name     string
		track    playlist.Track
		wantText string
	}{
		{name: "a provider track gets the provider lyrics", track: playlist.Track{Title: "Song", Artist: "Artist", Path: "spotify:track:abc"}, wantText: "spotify line"},
		{name: "a second provider answers", track: playlist.Track{Title: "Song", Artist: "Artist", Path: "other:track:1"}, wantText: "other line"},
		{name: "embedded lyrics come first", track: playlist.Track{Title: "Song", Artist: "Artist", Path: "spotify:track:abc", EmbeddedLyrics: "[00:01.00]embedded"}, wantText: "embedded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(tt.track)
			pl.SetIndex(0)
			m := Model{
				player:   &playbackFakeEngine{playing: true},
				playlist: pl,
				providers: []provider.Entry{
					{Key: "spotify", Name: "Spotify", Provider: &fakeLyricsSource{prefix: "spotify:track:", lines: []lyrics.Line{{Start: time.Second, Text: "spotify line"}}}},
					{Key: "other", Name: "Other", Provider: &fakeLyricsSource{prefix: "other:track:", lines: []lyrics.Line{{Start: time.Second, Text: "other line"}}}},
				},
			}
			m.lyrics.visible = true
			overlay := m.retryLyrics()().(lyricsLoadedMsg)
			if overlay.err != nil || len(overlay.lines) != 1 || overlay.lines[0].Text != tt.wantText {
				t.Fatalf("overlay = %+v, %v, want %q", overlay.lines, overlay.err, tt.wantText)
			}

			reply := make(chan ipc.Response, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cmd := m.handleIPCLyrics(ipcLyricsRequest{Context: ctx, Reply: reply}); cmd != nil {
				cmd()
			}
			got := <-reply
			if !got.OK || len(got.Lyrics) != 1 || got.Lyrics[0].Text != tt.wantText {
				t.Fatalf("IPC lyrics = %+v, want %q", got, tt.wantText)
			}
		})
	}
}

// A local audio file gets the lyrics of the .lrc file next to it, after its
// embedded lyrics and before any other lookup, even without artist and title.
func TestLRCFileNextToLocalTrack(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(filepath.Join(dir, "song.lrc"), []byte("[00:01.00]from the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := &fakeLyricsSource{prefix: song, lines: []lyrics.Line{{Start: time.Second, Text: "provider line"}}}
	tests := []struct {
		name     string
		track    playlist.Track
		wantText string
	}{
		{name: "the .lrc file", track: playlist.Track{Path: song, Title: "song"}, wantText: "from the file"},
		{name: "embedded lyrics come first", track: playlist.Track{Path: song, Title: "song", EmbeddedLyrics: "[00:01.00]embedded"}, wantText: "embedded"},
		// Without an artist, a search for the title alone could find
		// another song, so there is none.
		{name: "no file and no artist", track: playlist.Track{Path: filepath.Join(dir, "untagged.mp3"), Title: "untagged"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := lyricsLookupKey(tt.track, tt.track.Artist, tt.track.Title)
			if query == "" {
				t.Fatal("lyricsLookupKey() is empty, so the lyrics are never looked up")
			}
			msg := fetchTrackLyricsCmd(tt.track, tt.track.Artist, tt.track.Title, query, 1, []trackLyricsSource{other})().(lyricsLoadedMsg)
			if tt.wantText == "" {
				if !errors.Is(msg.err, lyrics.ErrNotFound) {
					t.Fatalf("lines = %+v, %v, want ErrNotFound", msg.lines, msg.err)
				}
				return
			}
			if msg.err != nil || len(msg.lines) != 1 || msg.lines[0].Text != tt.wantText {
				t.Fatalf("lines = %+v, %v, want %q", msg.lines, msg.err, tt.wantText)
			}
		})
	}
}

// Only a local audio file has an .lrc file to read.
func TestLRCFileOnlyForLocalAudioFiles(t *testing.T) {
	for _, tt := range []struct {
		path string
		want bool
	}{
		{"/music/song.mp3", true},
		{"music/Song.FLAC", true},
		{"/music/cover.jpg", false},
		{"https://example.com/song.mp3", false},
		{"spotify:track:abc", false},
		{"ssh://host/music/song.mp3", false},
		{"", false},
	} {
		if got := localAudioFile(tt.path); got != tt.want {
			t.Errorf("localAudioFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
		if got, want := len(lyricsLookups(playlist.Track{Path: tt.path}, nil)), map[bool]int{true: 1}[tt.want]; got != want {
			t.Errorf("lyricsLookups(%q) has %d lookups, want %d", tt.path, got, want)
		}
	}
	if got := lyricsLookupKey(playlist.Track{Path: "https://example.com/stream", Title: "Stream"}, "", "Stream"); got != "" {
		t.Errorf("lyricsLookupKey() for a stream without an artist = %q, want empty", got)
	}
}

// The lyrics remote operation reads the .lrc file too, and, like the
// overlay, it does not search for a title without an artist.
func TestIPCLyricsOfUntaggedLocalFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.lrc"), []byte("[00:01.00]from the file"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		path     string
		wantText string
	}{
		{"the .lrc file", filepath.Join(dir, "song.mp3"), "from the file"},
		{"no file and no artist", filepath.Join(dir, "untagged.mp3"), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(playlist.Track{Path: tt.path, Title: strings.TrimSuffix(filepath.Base(tt.path), ".mp3")})
			pl.SetIndex(0)
			m := Model{player: &playbackFakeEngine{playing: true}, playlist: pl}

			reply := make(chan ipc.Response, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cmd := m.handleIPCLyrics(ipcLyricsRequest{Context: ctx, Reply: reply}); cmd != nil {
				cmd()
			}
			got := <-reply
			if tt.wantText == "" {
				if got.OK || got.Error != lyrics.ErrNotFound.Error() {
					t.Fatalf("IPC lyrics = %+v, want %q", got, lyrics.ErrNotFound)
				}
				return
			}
			if !got.OK || len(got.Lyrics) != 1 || got.Lyrics[0].Text != tt.wantText {
				t.Fatalf("IPC lyrics = %+v, want %q", got, tt.wantText)
			}
		})
	}
}
