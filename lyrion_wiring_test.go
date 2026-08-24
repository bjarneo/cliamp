package main

import (
	"testing"

	"github.com/bjarneo/cliamp/external/lyrion"
)

// A Lyrion track is a finite file, so it must route to the buffered download
// pipeline alongside the other provider endpoints.
func TestIsBufferedProviderURLIncludesLyrion(t *testing.T) {
	u := lyrion.New("http://nas.local:9000", "", "").StreamURL("77")
	if !isBufferedProviderURL(u) {
		t.Errorf("isBufferedProviderURL(%q) = false, want true", u)
	}

	withAuth := lyrion.New("http://nas.local:9000", "bob", "pw").StreamURL("77")
	if !isBufferedProviderURL(withAuth) {
		t.Errorf("isBufferedProviderURL(%q) = false for an authenticated URL", withAuth)
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
