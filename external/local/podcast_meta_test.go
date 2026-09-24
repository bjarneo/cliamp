package local

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// An episode saved into a playlist has to come back recognizable: the feed
// marks it seekable, and the GUID keys its listening position.
func TestPlaylistRoundTripKeepsPodcastMeta(t *testing.T) {
	p := newTestProvider(t)
	episode := playlist.Track{
		Path:         "https://cdn.example.com/ep1.mp3",
		Title:        "Netanyahu Knew",
		Album:        "Part Of The Problem",
		DurationSecs: 3768,
		ProviderMeta: map[string]string{
			provider.MetaPodcastFeed:      "https://rss.art19.com/part-of-the-problem",
			provider.MetaPodcastGUID:      "guid-1",
			provider.MetaPodcastPublished: "2026-09-10",
		},
	}

	if _, _, err := p.AddTracks("saved", []playlist.Track{episode}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	tracks, err := p.Tracks("saved")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}

	got := tracks[0]
	if want := "https://rss.art19.com/part-of-the-problem"; got.Meta(provider.MetaPodcastFeed) != want {
		t.Errorf("feed = %q, want %q", got.Meta(provider.MetaPodcastFeed), want)
	}
	if got.Meta(provider.MetaPodcastGUID) != "guid-1" {
		t.Errorf("guid = %q, want guid-1", got.Meta(provider.MetaPodcastGUID))
	}
	if got.DurationSecs != 3768 {
		t.Errorf("duration = %d, want 3768", got.DurationSecs)
	}
}

func TestPlaylistRoundTripWithoutPodcastMeta(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("saved", []playlist.Track{{Path: "/local.mp3", Title: "Local"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	tracks, _ := p.Tracks("saved")
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}
	if tracks[0].ProviderMeta != nil {
		t.Errorf("ProviderMeta = %v, want nil for a track that never had one", tracks[0].ProviderMeta)
	}
}

// A GUID without a feed is not enough to identify an episode, so it is not
// carried alone.
func TestPlaylistRoundTripIgnoresAGuidWithoutAFeed(t *testing.T) {
	p := newTestProvider(t)
	track := playlist.Track{
		Path:         "https://cdn.example.com/ep1.mp3",
		Title:        "Orphan",
		ProviderMeta: map[string]string{provider.MetaPodcastGUID: "guid-1"},
	}
	if _, _, err := p.AddTracks("saved", []playlist.Track{track}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	tracks, _ := p.Tracks("saved")
	if got := tracks[0].Meta(provider.MetaPodcastGUID); got != "" {
		t.Errorf("guid = %q, want it dropped without a feed", got)
	}
}
