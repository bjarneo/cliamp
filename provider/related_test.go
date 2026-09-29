package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

type fakeRelater struct {
	prefix string
	tracks []playlist.Track
	gotN   int
}

func (f *fakeRelater) CanRelate(t playlist.Track) bool {
	return len(t.Path) >= len(f.prefix) && t.Path[:len(f.prefix)] == f.prefix
}

func (f *fakeRelater) RelatedTracks(_ context.Context, _ playlist.Track, n int) ([]playlist.Track, error) {
	f.gotN = n
	return f.tracks, nil
}

func paths(tracks []playlist.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Path
	}
	return out
}

func TestRelaterForPicksFirstThatRecognizesTrack(t *testing.T) {
	spotify := &fakeRelater{prefix: "spotify:"}
	web := &fakeRelater{prefix: "https://"}
	r, ok := RelaterFor(playlist.Track{Path: "https://www.youtube.com/watch?v=x"}, spotify, web)
	if !ok || r != web {
		t.Fatalf("RelaterFor = %v, %v; want the https relater", r, ok)
	}
	if r, ok := RelaterFor(playlist.Track{Path: "/music/a.mp3"}, spotify, web); ok {
		t.Fatalf("RelaterFor(local file) = %v, true; want none", r)
	}
}

func TestRelatedDropsSeedAndRepeatsAndCaps(t *testing.T) {
	r := &fakeRelater{tracks: []playlist.Track{
		{Path: "seed"}, {Path: "a"}, {Path: "b"}, {Path: "a"}, {Path: "c"}, {Path: "d"},
	}}
	got, err := Related(context.Background(), r, playlist.Track{Path: "seed"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("Related = %v, want %v", paths(got), want)
	}
	if r.gotN != 3 {
		t.Fatalf("relater asked for %d, want 3", r.gotN)
	}
}
