package playlist

import (
	"reflect"
	"testing"
)

func TestPlaybackTracksPreservesPlayNextAndShuffleOrder(t *testing.T) {
	p := New()
	p.Replace([]Track{{Path: "a"}, {Path: "b"}, {Path: "c"}, {Path: "d"}, {Path: "e"}})
	p.ToggleShuffle()
	order, _ := p.OrderWindow(0, p.Len())
	p.SetIndex(order[1])
	p.Queue(order[4])
	p.Queue(order[0])

	want := []string{p.Tracks()[order[1]].Path, p.Tracks()[order[4]].Path, p.Tracks()[order[0]].Path}
	for _, index := range order[2:] {
		want = append(want, p.Tracks()[index].Path)
	}
	if got := playbackPaths(p.PlaybackTracks()); !reflect.DeepEqual(got, want) {
		t.Fatalf("PlaybackTracks() = %v, want %v", got, want)
	}
}

func TestPlaybackTracksKeepsRemainingOrderAfterQueuedCurrent(t *testing.T) {
	p := New()
	p.Replace([]Track{{Path: "a"}, {Path: "b"}, {Path: "c"}, {Path: "d"}})
	p.Queue(3)
	p.Queue(1)
	if _, ok := p.Next(); !ok {
		t.Fatal("failed to activate play-next track")
	}
	if got, want := playbackPaths(p.PlaybackTracks()), []string{"d", "b", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PlaybackTracks() = %v, want %v", got, want)
	}
}

func playbackPaths(tracks []Track) []string {
	paths := make([]string, len(tracks))
	for i, track := range tracks {
		paths[i] = track.Path
	}
	return paths
}
