package embyapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// providerDialects runs a provider test once for each server dialect.
var providerDialects = []struct {
	name      string
	metaKey   string
	newClient func(baseURL, token, userID, user, password string) *Client
}{
	{"jellyfin", provider.MetaJellyfinID, NewJellyfinClient},
	{"emby", provider.MetaEmbyID, NewEmbyClient},
}

func TestProviderName(t *testing.T) {
	p := NewProvider(NewEmbyClient("https://emby.example.com", "tok", "user-1", "", ""), "Emby")
	if p.Name() != "Emby" {
		t.Fatalf("Name() = %q, want Emby", p.Name())
	}
}

func TestProviderPlaylists(t *testing.T) {
	for _, d := range providerDialects {
		t.Run(d.name, func(t *testing.T) {
			c := mock(d.newClient("https://media.example.com", "tok", "", "", ""), func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/Users/Me":
					return jsonResponse(`{"Id":"user-1","Name":"Nomad"}`), nil
				case "/Users/user-1/Views":
					return jsonResponse(`{"Items":[{"Id":"lib-1","Name":"Music","CollectionType":"music"}]}`), nil
				case "/Items":
					return jsonResponse(`{"Items":[{"Id":"album-1","Name":"Kind of Blue","AlbumArtist":"Miles Davis","ProductionYear":1959,"ChildCount":5}]}`), nil
				default:
					t.Fatalf("unexpected path %s", req.URL.Path)
					return nil, nil
				}
			})

			lists, err := NewProvider(c, d.name).Playlists()
			if err != nil {
				t.Fatalf("Playlists() error: %v", err)
			}
			if len(lists) != 1 {
				t.Fatalf("expected 1 playlist, got %d", len(lists))
			}
			if lists[0].ID != "album-1" || lists[0].TrackCount != 5 {
				t.Fatalf("playlist = %+v", lists[0])
			}
			if lists[0].Name != "Miles Davis — Kind of Blue (1959)" {
				t.Fatalf("playlist name = %q", lists[0].Name)
			}
		})
	}
}

func TestProviderTracks(t *testing.T) {
	for _, d := range providerDialects {
		t.Run(d.name, func(t *testing.T) {
			c := mock(d.newClient("https://media.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/Items" {
					t.Fatalf("unexpected path %s", req.URL.Path)
				}
				return jsonResponse(`{"Items":[{"Id":"track-1","Name":"So What","Album":"Kind of Blue","Artists":["Miles Davis"],"ProductionYear":1959,"IndexNumber":1,"RunTimeTicks":5650000000}]}`), nil
			})

			tracks, err := NewProvider(c, d.name).Tracks("album-1")
			if err != nil {
				t.Fatalf("Tracks() error: %v", err)
			}
			if len(tracks) != 1 {
				t.Fatalf("expected 1 track, got %d", len(tracks))
			}
			tr := tracks[0]
			if tr.Title != "So What" || tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" || tr.TrackNumber != 1 || !tr.Stream {
				t.Fatalf("track = %+v", tr)
			}
			if got := tr.Meta(d.metaKey); got != "track-1" {
				t.Fatalf("track meta %s = %q, want track-1", d.metaKey, got)
			}
		})
	}
}

// TestProviderWrapsErrors verifies that provider errors name the operation and
// keep the client error.
func TestProviderWrapsErrors(t *testing.T) {
	failure := errors.New("server unreachable")
	for _, d := range providerDialects {
		c := mock(d.newClient("https://media.example.com", "tok", "user-1", "", ""), func(*http.Request) (*http.Response, error) {
			return nil, failure
		})
		p := NewProvider(c, d.name)
		tests := []struct {
			op   string
			call func() error
		}{
			{"playlists", func() error { _, err := p.Playlists(); return err }},
			{"tracks", func() error { _, err := p.Tracks("album-1"); return err }},
			{"artists", func() error { _, err := p.Artists(); return err }},
			{"artist albums", func() error { _, err := p.ArtistAlbums("artist-1"); return err }},
			{"album list", func() error { _, err := p.AlbumList(SortAlbumsByName, 0, 10); return err }},
			{"search", func() error { _, err := p.SearchTracks(t.Context(), "blue", 10); return err }},
		}
		for _, tt := range tests {
			t.Run(d.name+"/"+tt.op, func(t *testing.T) {
				err := tt.call()
				if !errors.Is(err, failure) || !strings.HasPrefix(err.Error(), tt.op+": "+d.name+": ") {
					t.Fatalf("error = %v, want prefix %q and the client error", err, tt.op+": "+d.name+": ")
				}
			})
		}
	}
}

func TestProviderCanReportPlayback(t *testing.T) {
	for _, d := range providerDialects {
		p := NewProvider(d.newClient("https://media.example.com", "tok", "user-1", "", ""), d.name)
		tests := []struct {
			name  string
			track playlist.Track
			want  bool
		}{
			{"own track", trackWithMeta(d.metaKey, "track-1"), true},
			{"navidrome track", trackWithMeta(provider.MetaNavidromeID, "nav-1"), false},
			{"no metadata", playlist.Track{}, false},
		}
		for _, tt := range tests {
			t.Run(d.name+"/"+tt.name, func(t *testing.T) {
				if got := p.CanReportPlayback(tt.track); got != tt.want {
					t.Fatalf("CanReportPlayback() = %v, want %v", got, tt.want)
				}
			})
		}
	}
}

// TestProviderRestoreTrack verifies that restoring a saved stream URL
// refreshes credentials and preserves track metadata.
func TestProviderRestoreTrack(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{
			name:  "current history URL",
			track: playlist.Track{Title: "Song", Path: "https://media.example.com/media/Items/track-1/Download?api_key=new-token"},
			want:  true,
		},
		{
			name:  "legacy history URL",
			track: playlist.Track{Title: "Song", Path: "https://media.example.com/media/Items/track-2/Download?api_key=old-token"},
			want:  true,
		},
		{
			name:  "other server",
			track: playlist.Track{Path: "https://other.example.com/Items/track-3/Download"},
		},
	}
	for _, d := range providerDialects {
		p := NewProvider(d.newClient("https://media.example.com/media", "new-token", "user-1", "", ""), d.name)
		for _, tt := range tests {
			t.Run(d.name+"/"+tt.name, func(t *testing.T) {
				got, ok := p.RestoreTrack(tt.track)
				if ok != tt.want {
					t.Fatalf("RestoreTrack() ok = %v, want %v", ok, tt.want)
				}
				if !ok {
					return
				}
				if got.Title != tt.track.Title || !got.Stream {
					t.Fatalf("restored track = %+v", got)
				}
				if got.Meta(d.metaKey) == "" {
					t.Fatalf("restored track is missing %s metadata", d.metaKey)
				}
				if !strings.Contains(got.Path, "api_key=new-token") {
					t.Fatalf("restored path did not refresh credentials: %q", got.Path)
				}
			})
		}
	}
}

// TestProviderRestoreTrackDefersAuthenticationUntilSourceResolution verifies
// that restoring a saved stream URL performs no startup requests and defers
// password authentication until source resolution.
func TestProviderRestoreTrackDefersAuthenticationUntilSourceResolution(t *testing.T) {
	for _, d := range providerDialects {
		t.Run(d.name, func(t *testing.T) {
			c := mock(d.newClient("https://media.example.com", "", "", "user", "password"), func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected startup request: %s", req.URL)
				return nil, nil
			})
			p := NewProvider(c, d.name)
			oldURL := "https://media.example.com/Items/track-1/Download?api_key=old-token"
			got, ok := p.RestoreTrack(playlist.Track{Path: oldURL, Title: "Song"})
			if !ok {
				t.Fatal("RestoreTrack() did not recognize history URL")
			}
			if got.Path != oldURL {
				t.Fatalf("RestoreTrack() path = %q, want saved URL before password authentication", got.Path)
			}
			mock(c, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/Users/AuthenticateByName" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return jsonResponse(`{"User":{"Id":"user-1"},"AccessToken":"new-token"}`), nil
			})
			source, err := p.ResolveSource(got.Path)
			if err != nil {
				t.Fatalf("ResolveSource() error: %v", err)
			}
			if want := "https://media.example.com/Items/track-1/Download?ApiKey=new-token&api_key=new-token"; source != want {
				t.Fatalf("ResolveSource() = %q, want %q", source, want)
			}
			if got.Path != oldURL || got.Title != "Song" || got.Meta(d.metaKey) != "track-1" || !got.Stream {
				t.Fatalf("source resolution changed the logical restored track: %+v", got)
			}
		})
	}
}

func TestProviderResolveSourcePropagatesAuthenticationFailure(t *testing.T) {
	authErr := errors.New("authentication unavailable")
	for _, d := range providerDialects {
		t.Run(d.name, func(t *testing.T) {
			c := mock(d.newClient("https://media.example.com", "", "", "user", "password"), func(*http.Request) (*http.Response, error) {
				return nil, authErr
			})
			got, err := NewProvider(c, d.name).ResolveSource("https://media.example.com/Items/track-1/Download?api_key=old-token")
			if !errors.Is(err, authErr) || got != "" {
				t.Fatalf("ResolveSource() = (%q, %v), want no source and authentication error", got, err)
			}
		})
	}
}

func trackWithMeta(key, value string) playlist.Track {
	return playlist.Track{ProviderMeta: map[string]string{key: value}}
}

// TestProviderCachesReturnCopies verifies that a caller that changes a
// returned slice does not change the provider cache.
func TestProviderCachesReturnCopies(t *testing.T) {
	c := mock(NewJellyfinClient("https://media.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/Users/user-1/Views":
			return jsonResponse(`{"Items":[{"Id":"lib-1","Name":"Music","CollectionType":"music"}]}`), nil
		case req.URL.Query().Get("includeItemTypes") == "MusicAlbum":
			return jsonResponse(`{"Items":[{"Id":"album-1","Name":"Kind of Blue"}]}`), nil
		default:
			return jsonResponse(`{"Items":[{"Id":"track-1","Name":"So What"}]}`), nil
		}
	})
	p := NewProvider(c, "Jellyfin")
	tests := []struct {
		name string
		call func() (*string, error)
	}{
		{"playlists", func() (*string, error) {
			lists, err := p.Playlists()
			if err != nil {
				return nil, err
			}
			return &lists[0].Name, nil
		}},
		{"tracks", func() (*string, error) {
			tracks, err := p.Tracks("album-1")
			if err != nil {
				return nil, err
			}
			return &tracks[0].Title, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change the result of the fetch, then the result of a cache hit.
			// Each later call must still return the value of the first call.
			got, err := tt.call()
			if err != nil {
				t.Fatalf("first call error: %v", err)
			}
			want := *got
			for _, source := range []string{"fetched", "cached"} {
				*got = "changed"
				got, err = tt.call()
				if err != nil {
					t.Fatalf("call after a change to the %s slice: %v", source, err)
				}
				if *got != want {
					t.Fatalf("a change to the %s slice changed the cache: got %q, want %q", source, *got, want)
				}
			}
		})
	}
}
