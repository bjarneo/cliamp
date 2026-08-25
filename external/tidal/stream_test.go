package tidal

import "testing"

func TestStreamURLRegistryEvictsOnReResolve(t *testing.T) {
	const first = "https://cdn.tidal.com/mediatracks/abc/0.flac?exp=1"
	const second = "https://cdn.tidal.com/mediatracks/abc/0.flac?exp=2"

	streamURLs.register("track-1", first)
	if !IsStreamURL(first) {
		t.Fatal("registered URL not recognized")
	}

	// Re-resolving the same track replaces its URL: the stale one must be
	// evicted so the registry stays bounded by tracks, not resolutions.
	streamURLs.register("track-1", second)
	if IsStreamURL(first) {
		t.Error("stale URL still registered after re-resolve")
	}
	if !IsStreamURL(second) {
		t.Error("fresh URL not recognized")
	}

	streamURLs.register("track-2", "")
	if IsStreamURL("") {
		t.Error("empty URL must not be registered")
	}
}
