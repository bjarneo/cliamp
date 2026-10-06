package main

import (
	"testing"

	"github.com/bjarneo/cliamp/external/lyrion"
)

// A Lyrion track is a finite file, so it must route to the buffered download
// pipeline alongside the other provider endpoints.
func TestIsBufferedProviderURLIncludesLyrion(t *testing.T) {
	// The matcher sees the URL a source resolver produced, not the track path.
	for _, c := range []*lyrion.Client{
		lyrion.New("http://nas.local:9000", "", ""),
		lyrion.New("http://nas.local:9000", "bob", "pw"),
	} {
		u, _, err := c.ResolveSource(lyrion.TrackURIPrefix + "77")
		if err != nil {
			t.Fatalf("ResolveSource: %v", err)
		}
		if !isBufferedProviderURL(u) {
			t.Errorf("isBufferedProviderURL(%q) = false, want true", u)
		}
	}
}

// A lyrion:// track path is resolved before the matcher ever sees it, so the
// URI form itself must not be mistaken for a directly fetchable stream URL.
// (That track paths carry no credentials is asserted in the lyrion package.)
func TestLyrionTrackURIIsNotABufferedURL(t *testing.T) {
	if isBufferedProviderURL(lyrion.TrackURIPrefix + "77") {
		t.Error("a lyrion:// URI should not match the buffered URL matcher; it is resolved first")
	}
}

func TestIsBufferedProviderURLRejectsLiveStreams(t *testing.T) {
	for _, u := range []string{
		"https://stream.example.com/live.mp3",
		"http://nas.local:9000/music/current/cover.jpg",
	} {
		if isBufferedProviderURL(u) {
			t.Errorf("isBufferedProviderURL(%q) = true, want false", u)
		}
	}
}
