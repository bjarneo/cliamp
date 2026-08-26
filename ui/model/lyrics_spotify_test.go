package model

import (
	"context"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

type fakeSpotifyLyricFetcher struct {
	lines []lyrics.Line
	err   error
}

func (f *fakeSpotifyLyricFetcher) TrackLyrics(ctx context.Context, trackID string) ([]lyrics.Line, error) {
	return f.lines, f.err
}

func TestFetchTrackLyricsCmdPrefersSpotify(t *testing.T) {
	want := []lyrics.Line{{Start: time.Second, Text: "spotify line"}}
	msg := fetchTrackLyricsCmd(
		playlist.Track{Path: "spotify:track:abc"},
		"Artist", "Title", "Artist\nTitle", 1,
		&fakeSpotifyLyricFetcher{lines: want},
	)().(lyricsLoadedMsg)

	if msg.err != nil {
		t.Fatalf("err = %v, want nil", msg.err)
	}
	if len(msg.lines) != 1 || msg.lines[0].Text != "spotify line" {
		t.Fatalf("lines = %+v, want spotify result", msg.lines)
	}
}

func TestFetchTrackLyricsCmdEmbeddedBeatsSpotify(t *testing.T) {
	fetcher := &fakeSpotifyLyricFetcher{}
	msg := fetchTrackLyricsCmd(
		playlist.Track{Path: "spotify:track:abc", EmbeddedLyrics: "[00:01.00]embedded"},
		"Artist", "Title", "Artist\nTitle", 1,
		fetcher,
	)().(lyricsLoadedMsg)

	if msg.err != nil {
		t.Fatalf("err = %v, want nil", msg.err)
	}
	if len(msg.lines) != 1 || msg.lines[0].Text != "embedded" {
		t.Fatalf("lines = %+v, want embedded lyrics", msg.lines)
	}
}

func TestSpotifyLyricFetcherFromProviders(t *testing.T) {
	m := Model{
		providers: []ProviderEntry{
			{Key: "radio", Name: "Radio"},
			{Key: "spotify", Name: "Spotify", Provider: &fakeSpotifyProvider{}},
		},
	}
	if m.spotifyLyricFetcher() == nil {
		t.Fatal("spotifyLyricFetcher() = nil, want fake provider")
	}

	m2 := Model{
		providers: []ProviderEntry{
			{Key: "radio", Name: "Radio"},
			{Key: "spotify", Name: "Spotify"}, // not configured → nil Provider
		},
	}
	if m2.spotifyLyricFetcher() != nil {
		t.Fatal("spotifyLyricFetcher() = non-nil, want nil for unconfigured provider")
	}
}

// fakeSpotifyProvider satisfies playlist.Provider plus the lyric capability.
type fakeSpotifyProvider struct {
	playlist.Provider
}

func (p *fakeSpotifyProvider) TrackLyrics(ctx context.Context, trackID string) ([]lyrics.Line, error) {
	return nil, nil
}
