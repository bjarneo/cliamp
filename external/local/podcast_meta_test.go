package local

import (
	"maps"
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

// Playlists written before the shared track codec keep the feed and GUID as
// podcast_feed and podcast_guid. They must still load.
func TestParseTrackFieldsReadsLegacyPodcastKeys(t *testing.T) {
	const feed, guid = "https://rss.example.com/show", "guid-1"
	tests := []struct {
		name   string
		fields map[string]string
		want   map[string]string
	}{
		{
			name:   "feed and guid",
			fields: map[string]string{"podcast_feed": feed, "podcast_guid": guid},
			want:   map[string]string{provider.MetaPodcastFeed: feed, provider.MetaPodcastGUID: guid},
		},
		{
			name:   "feed only",
			fields: map[string]string{"podcast_feed": feed},
			want:   map[string]string{provider.MetaPodcastFeed: feed},
		},
		{
			name:   "guid without a feed is not read",
			fields: map[string]string{"podcast_guid": guid},
		},
		{
			name: "provider_meta keys win",
			fields: map[string]string{
				"podcast_feed":               "https://old.example.com/rss",
				"podcast_guid":               "old-guid",
				"provider_meta.podcast.feed": feed,
				"provider_meta.podcast.guid": guid,
			},
			want: map[string]string{provider.MetaPodcastFeed: feed, provider.MetaPodcastGUID: guid},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fields["path"] = "https://cdn.example.com/ep1.mp3"
			got := parseTrackFields(tt.fields).ProviderMeta
			if !maps.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
				t.Errorf("ProviderMeta = %v, want %v", got, tt.want)
			}
		})
	}
}
