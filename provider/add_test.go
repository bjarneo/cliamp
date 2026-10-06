package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

type addTestProvider struct{ added []string }

func (p *addTestProvider) Name() string                                { return "Test" }
func (p *addTestProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (p *addTestProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }

// oneAtATime can only add one track per call. Its add of fail.mp3 fails.
type oneAtATime struct{ addTestProvider }

func (p *oneAtATime) AddTrackToPlaylist(_ context.Context, _ string, track playlist.Track) error {
	if track.Path == "fail.mp3" {
		return errors.New("disk full")
	}
	p.added = append(p.added, track.Path)
	return nil
}

// batchWriter adds all tracks in one call and skips dup.mp3.
type batchWriter struct{ oneAtATime }

func (p *batchWriter) AddTracksToPlaylist(_ context.Context, _ string, tracks []playlist.Track) (added, skipped int, err error) {
	for _, track := range tracks {
		if track.Path == "dup.mp3" {
			skipped++
			continue
		}
		p.added = append(p.added, "batch:"+track.Path)
		added++
	}
	return added, skipped, nil
}

func TestAddTracks(t *testing.T) {
	tracks := func(paths ...string) []playlist.Track {
		out := make([]playlist.Track, len(paths))
		for i, path := range paths {
			out[i] = playlist.Track{Path: path}
		}
		return out
	}
	for _, tc := range []struct {
		name                   string
		p                      playlist.Provider
		tracks                 []playlist.Track
		wantAdded, wantSkipped int
		wantErr                bool
		wantWritten            []string
	}{
		{name: "batch writer", p: &batchWriter{}, tracks: tracks("a.mp3", "dup.mp3"), wantAdded: 1, wantSkipped: 1, wantWritten: []string{"batch:a.mp3"}},
		{name: "one at a time", p: &oneAtATime{}, tracks: tracks("a.mp3", "b.mp3"), wantAdded: 2, wantWritten: []string{"a.mp3", "b.mp3"}},
		{name: "one at a time stops at an error", p: &oneAtATime{}, tracks: tracks("a.mp3", "fail.mp3", "b.mp3"), wantAdded: 1, wantErr: true, wantWritten: []string{"a.mp3"}},
		{name: "no writer", p: &addTestProvider{}, tracks: tracks("a.mp3"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			added, skipped, err := AddTracks(context.Background(), tc.p, "Mix", tc.tracks)
			if added != tc.wantAdded || skipped != tc.wantSkipped || (err != nil) != tc.wantErr {
				t.Fatalf("AddTracks = %d, %d, %v; want %d, %d, error %v", added, skipped, err, tc.wantAdded, tc.wantSkipped, tc.wantErr)
			}
			var written []string
			switch p := tc.p.(type) {
			case *batchWriter:
				written = p.added
			case *oneAtATime:
				written = p.added
			}
			if !reflect.DeepEqual(written, tc.wantWritten) {
				t.Fatalf("written = %v, want %v", written, tc.wantWritten)
			}
		})
	}
}
