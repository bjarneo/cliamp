package model

import (
	"context"
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
			msg := fetchTrackLyricsCmd(tt.track, "Artist", "Title", "Artist\nTitle", 1, sources)().(lyricsLoadedMsg)
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
