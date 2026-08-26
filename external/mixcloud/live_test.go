package mixcloud

import (
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// TestLivePublicAPI is opt-in so ordinary and offline test runs remain
// deterministic. It catches drift in Mixcloud's public discover/detail
// response shapes without requiring an account or storing credentials.
func TestLivePublicAPI(t *testing.T) {
	if os.Getenv("CLIAMP_LIVE_MIXCLOUD") != "1" {
		t.Skip("set CLIAMP_LIVE_MIXCLOUD=1 to exercise api.mixcloud.com")
	}

	p := NewFromConfig(Config{Enabled: true, MaxItems: 3, Styles: []string{"house"}})
	tracks, err := p.Tracks(recentID)
	if err != nil {
		t.Fatalf("recent releases: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("recent releases returned no shows")
	}
	for _, track := range tracks {
		u, err := url.Parse(track.Path)
		if err != nil || u.Hostname() != "www.mixcloud.com" {
			t.Fatalf("unexpected playback URL %q: %v", track.Path, err)
		}
		if track.Meta(provider.MetaMixcloudKey) == "" || track.Title == "" {
			t.Fatalf("incomplete live show mapping: %+v", track)
		}
	}

	detail, err := p.AlbumTracks(tracks[0].Meta(provider.MetaMixcloudKey))
	if err != nil {
		t.Fatalf("show detail: %v", err)
	}
	if len(detail) != 1 || detail[0].Path == "" {
		t.Fatalf("show detail mapping = %+v", detail)
	}

	creator := tracks[0].Meta(provider.MetaMixcloudCreator)
	if creator == "" {
		t.Fatalf("recent show has no creator metadata: %+v", tracks[0])
	}
	collections, err := p.ArtistAlbums(creator)
	if err != nil {
		t.Fatalf("creator collections: %v", err)
	}
	if len(collections) != 2 || collections[0].Name != "Uploads" || collections[1].Name != "Favorites" {
		t.Fatalf("creator collections = %+v", collections)
	}
	for _, collection := range collections {
		if _, err := p.AlbumTracks(collection.ID); err != nil {
			t.Fatalf("creator %s: %v", collection.Name, err)
		}
	}
}

// TestLivePublicAccount is opt-in and uses only a public Mixcloud username.
// It verifies the account connections used by cliamp without reading browser
// cookies or requiring a developer token.
func TestLivePublicAccount(t *testing.T) {
	if os.Getenv("CLIAMP_LIVE_MIXCLOUD") != "1" {
		t.Skip("set CLIAMP_LIVE_MIXCLOUD=1 to exercise api.mixcloud.com")
	}
	username := os.Getenv("CLIAMP_LIVE_MIXCLOUD_USER")
	if username == "" {
		t.Skip("set CLIAMP_LIVE_MIXCLOUD_USER to a public profile username")
	}

	p := NewFromConfig(Config{
		Enabled:        true,
		Username:       username,
		StylesSet:      true,
		MaxItems:       3,
		StreamCreators: 2,
	})
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("account playlists: %v", err)
	}
	wantLists := []string{streamID, activityID, uploadsID, favoritesID, listensID}
	for _, id := range wantLists {
		if !slices.ContainsFunc(lists, func(item playlist.PlaylistInfo) bool { return item.ID == id }) {
			t.Errorf("account playlists omit %q: %+v", id, lists)
		}
	}
	for _, id := range wantLists {
		if _, err := p.Tracks(id); err != nil {
			t.Errorf("account view %q: %v", id, err)
		}
	}
	if _, err := p.Artists(); err != nil {
		t.Errorf("followed creators: %v", err)
	}
}
