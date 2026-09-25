package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"golang.org/x/oauth2"
)

// stubAddTrack accepts any POST to a playlist's items.
func stubAddTrack(t *testing.T, calls *int) *SpotifyProvider {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"snapshot_id":"new"}`)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320)
}

// A playlist this client just wrote to must drop its resolved URIs alongside
// its cached tracks, or the next read would slice a list taken before the
// write. Driven through the real write path rather than a copy of it.
func TestInvalidationDropsResolvedURIs(t *testing.T) {
	calls := 0
	p := stubAddTrack(t, &calls)
	p.mu.Lock()
	p.trackCache["list"] = &playlistCache{snapshotID: "old", tracks: []playlist.Track{{Path: "spotify:track:a"}}}
	p.pending["list"] = &pendingTracks{want: 50, total: 100, uris: []string{"spotify:track:a"}}
	p.mu.Unlock()

	if err := p.AddTrackToPlaylist(context.Background(), "list", playlist.Track{Path: "spotify:track:b"}); err != nil {
		t.Fatalf("AddTrackToPlaylist: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.pending["list"]; ok {
		t.Error("an in-flight read, and the resolve it holds, outlived a write to the playlist")
	}
	if _, ok := p.trackCache["list"]; ok {
		t.Error("cached tracks outlived a write to the playlist")
	}
}

func metadataImage(id byte, size metadatapb.Image_Size) *metadatapb.Image {
	return &metadatapb.Image{FileId: []byte{0xab, id}, Size: size.Enum()}
}

// A track read through the client protocol must carry the same cover the Web
// API path would pick, or Liked Songs and refused playlists lose their art.
func TestCoverFromMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		album *metadatapb.Album
		want  string
	}{
		{
			"picks the 300px size, like the Web API path",
			&metadatapb.Album{CoverGroup: &metadatapb.ImageGroup{Image: []*metadatapb.Image{
				metadataImage(1, metadatapb.Image_SMALL),
				metadataImage(2, metadatapb.Image_DEFAULT),
				metadataImage(3, metadatapb.Image_LARGE),
			}}},
			"https://i.scdn.co/image/ab02",
		},
		{
			"falls back to the older cover list",
			&metadatapb.Album{Cover: []*metadatapb.Image{metadataImage(4, metadatapb.Image_DEFAULT)}},
			"https://i.scdn.co/image/ab04",
		},
		{
			"largest when nothing reaches 300px",
			&metadatapb.Album{CoverGroup: &metadatapb.ImageGroup{Image: []*metadatapb.Image{
				metadataImage(5, metadatapb.Image_SMALL),
			}}},
			"https://i.scdn.co/image/ab05",
		},
		{"no art at all", &metadatapb.Album{}, ""},
		{"no album", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := coverFromMetadata(tc.album); got != tc.want {
				t.Errorf("cover = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrackFromMetadataCarriesAlbumArt(t *testing.T) {
	tr := &metadatapb.Track{Album: &metadatapb.Album{CoverGroup: &metadatapb.ImageGroup{
		Image: []*metadatapb.Image{metadataImage(2, metadatapb.Image_DEFAULT)},
	}}}
	if got := trackFromMetadata("spotify:track:x", tr).AlbumArtURL; got != "https://i.scdn.co/image/ab02" {
		t.Errorf("AlbumArtURL = %q, want the album's cover", got)
	}
}
