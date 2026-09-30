package spotify

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

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
			name:   "an image without a width still counts",
			images: []spotifyImage{{URL: "unsized"}},
			want:   "unsized",
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

// imageURLs projects each row's ImageURL for comparison.
func imageURLs[T any](rows []T, imageURL func(T) string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = imageURL(r)
	}
	return out
}

// Playlist, album, and artist rows carry the images Spotify already returns on
// those objects; pseudo-rows and objects without images stay empty.
func TestCollectionImageURLs(t *testing.T) {
	// Spotify typically offers 640/300/64; pickCoverImage takes the 300px one.
	images := func(id string) string {
		return fmt.Sprintf(`"images":[{"url":"https://i.scdn.co/%[1]s-640","width":640,"height":640},`+
			`{"url":"https://i.scdn.co/%[1]s-300","width":300,"height":300},`+
			`{"url":"https://i.scdn.co/%[1]s-64","width":64,"height":64}]`, id)
	}
	art := func(id string) string { return "https://i.scdn.co/" + id + "-300" }
	body := func(s string) func(*testing.T, url.Values) string {
		return func(*testing.T, url.Values) string { return s }
	}
	albumInfoArt := func(a provider.AlbumInfo) string { return a.ImageURL }
	artistInfoArt := func(a provider.ArtistInfo) string { return a.ImageURL }
	playlistInfoArt := func(pl playlist.PlaylistInfo) string { return pl.ImageURL }

	tests := []struct {
		name     string
		handlers map[string]func(*testing.T, url.Values) string
		load     func(t *testing.T, p *SpotifyProvider) ([]string, error)
		want     []string
	}{
		{
			name: "followed artists",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/me/following": body(`{"artists":{"items":[` +
					`{"id":"a1","name":"With Art",` + images("a1") + `},` +
					`{"id":"a2","name":"No Art","images":[]}],"total":2}}`),
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.Artists()
				return imageURLs(got, artistInfoArt), err
			},
			want: []string{art("a1"), ""},
		},
		{
			name: "artist albums",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/artists/art1/albums": body(`{"items":[` +
					`{"id":"al1","name":"With Art",` + images("al1") + `},` +
					`{"id":"al2","name":"No Art"}],"total":2}`),
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.ArtistAlbums("art1")
				return imageURLs(got, albumInfoArt), err
			},
			want: []string{art("al1"), ""},
		},
		{
			name: "saved albums browse",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/me/albums": body(`{"items":[{"album":{"id":"al1","name":"Saved",` + images("al1") + `}}],"total":1}`),
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.AlbumList(SortRecent, 0, 10)
				return imageURLs(got, albumInfoArt), err
			},
			want: []string{art("al1")},
		},
		{
			name: "playlist listing",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/me":        body(`{"id":"me"}`),
				"/v1/me/tracks": body(`{"total":3}`),
				"/v1/me/playlists": func(t *testing.T, query url.Values) string {
					// The fields filter drops anything it does not name.
					if fields := query.Get("fields"); !strings.Contains(fields, "images") {
						t.Errorf("fields = %q, want playlist images requested", fields)
					}
					return `{"items":[` +
						`{"id":"owned","name":"Owned","owner":{"id":"me"},"items":{"total":2},` + images("pl1") + `},` +
						`{"id":"followed","name":"Followed","owner":{"id":"other"},"items":{"total":3},"images":null}` +
						`],"total":2}`
				},
				"/v1/me/albums": body(`{"items":[{"album":{"id":"al1","name":"Saved","total_tracks":9,` +
					`"artists":[{"name":"Band"}],` + images("al1") + `}}],"total":1}`),
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.Playlists()
				return imageURLs(got, playlistInfoArt), err
			},
			// Your Music is a pseudo-row with no Spotify image.
			want: []string{"", art("pl1"), "", art("al1")},
		},
		{
			name: "search",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/me": body(`{"id":"me"}`),
				"/v1/search": body(`{` +
					`"albums":{"items":[{"id":"al1","name":"Album",` + images("al1") + `}]},` +
					`"artists":{"items":[{"id":"a1","name":"Artist",` + images("a1") + `},{"id":"a2","name":"Bare"}]},` +
					`"playlists":{"items":[{"id":"pl1","name":"List","owner":{"id":"x"},` + images("pl1") + `}]}}`),
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.SearchAll(t.Context(), "q", 10)
				out := imageURLs(got.Albums, albumInfoArt)
				out = append(out, imageURLs(got.Artists, artistInfoArt)...)
				return append(out, imageURLs(got.Playlists, playlistInfoArt)...), err
			},
			want: []string{art("al1"), art("a1"), "", art("pl1")},
		},
		{
			name: "artist detail",
			handlers: map[string]func(*testing.T, url.Values) string{
				"/v1/artists/art1": body(`{"id":"art1","name":"Ringo",` + images("art1") + `}`),
				"/v1/artists/art1/albums": body(`{"items":[` +
					`{"id":"al1","name":"Debut",` + images("al1") + `}],"total":1}`),
				// No album-tracks or search handlers: the popular pool is best-effort.
			},
			load: func(t *testing.T, p *SpotifyProvider) ([]string, error) {
				got, err := p.ArtistDetail("art1")
				return append([]string{got.Info.ImageURL}, imageURLs(got.Discography, albumInfoArt)...), err
			},
			want: []string{art("art1"), art("al1")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			for path, h := range tt.handlers {
				m.handlers[path] = h
			}
			got, err := tt.load(t, newTestProvider())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ImageURLs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlbumSearchHitCarriesCover(t *testing.T) {
	hit := albumFromItem(&spotifyAlbumItem{
		ID: "al1", Name: "Kamikaze",
		Images: []spotifyImage{{URL: "https://i.scdn.co/image/big", Width: 640}, {URL: "https://i.scdn.co/image/mid", Width: 300}},
	})
	if !hit.IsAlbum() || hit.AlbumArtURL != "https://i.scdn.co/image/mid" {
		t.Fatalf("album hit: album=%v art=%q, want the ~300px cover", hit.IsAlbum(), hit.AlbumArtURL)
	}
}
