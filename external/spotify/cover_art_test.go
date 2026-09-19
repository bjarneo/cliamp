package spotify

import "testing"

func TestPickCoverImage(t *testing.T) {
	tests := []struct {
		name   string
		images []spotifyImage
		want   string
	}{
		{
			name: "prefers the smallest image at or above the target",
			images: []spotifyImage{
				{URL: "big", Width: 640, Height: 640},
				{URL: "mid", Width: 300, Height: 300},
				{URL: "tiny", Width: 64, Height: 64},
			},
			want: "mid",
		},
		{
			name: "falls back to the largest when all are below target",
			images: []spotifyImage{
				{URL: "tiny", Width: 64, Height: 64},
				{URL: "small", Width: 160, Height: 160},
			},
			want: "small",
		},
		{
			name:   "no images",
			images: nil,
			want:   "",
		},
		{
			name:   "skips entries without a URL",
			images: []spotifyImage{{URL: "", Width: 640}, {URL: "ok", Width: 300}},
			want:   "ok",
		},
		{
			name:   "exact target is taken",
			images: []spotifyImage{{URL: "exact", Width: 300}},
			want:   "exact",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickCoverImage(tc.images); got != tc.want {
				t.Errorf("pickCoverImage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrackFromItemCarriesAlbumArt(t *testing.T) {
	item := &spotifyItem{
		ID: "abc", Name: "Song", Type: "track", URI: "spotify:track:abc",
	}
	item.Album.Name = "The Album"
	item.Album.Images = []spotifyImage{
		{URL: "https://i.scdn.co/big", Width: 640},
		{URL: "https://i.scdn.co/mid", Width: 300},
	}

	track := trackFromItem(item)
	if got, want := track.AlbumArtURL, "https://i.scdn.co/mid"; got != want {
		t.Errorf("AlbumArtURL = %q, want %q", got, want)
	}
}

// Episodes carry their own images; the show's art is the fallback.
func TestTrackFromItemEpisodeArt(t *testing.T) {
	episode := &spotifyItem{ID: "e1", Name: "Ep 1", Type: "episode", URI: "spotify:episode:e1"}
	episode.Show.Name = "The Show"
	episode.Show.Images = []spotifyImage{{URL: "show-art", Width: 640}}
	episode.Images = []spotifyImage{{URL: "episode-art", Width: 640}}

	if got := trackFromItem(episode).AlbumArtURL; got != "episode-art" {
		t.Errorf("AlbumArtURL = %q, want the episode's own art", got)
	}

	episode.Images = nil
	if got := trackFromItem(episode).AlbumArtURL; got != "show-art" {
		t.Errorf("AlbumArtURL = %q, want the show art as fallback", got)
	}
}

func TestTrackFromItemWithoutArt(t *testing.T) {
	item := &spotifyItem{ID: "x", Name: "No Art", Type: "track", URI: "spotify:track:x"}
	if got := trackFromItem(item).AlbumArtURL; got != "" {
		t.Errorf("AlbumArtURL = %q, want empty when the API returns no images", got)
	}
}
