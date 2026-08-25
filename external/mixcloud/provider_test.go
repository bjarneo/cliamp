package mixcloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func providerWithServer(t *testing.T, cfg Config, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	p := NewFromConfig(cfg)
	if p == nil {
		server.Close()
		t.Fatal("NewFromConfig returned nil")
	}
	p.client.baseURL = server.URL
	p.client.httpClient = server.Client()
	return p, server
}

func offlineProvider(t *testing.T, cfg Config) *Provider {
	t.Helper()
	p, server := providerWithServer(t, cfg, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL.Path)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	t.Cleanup(server.Close)
	return p
}

func TestNewFromConfigAndStyles(t *testing.T) {
	if NewFromConfig(Config{}) != nil {
		t.Fatal("disabled provider should be nil")
	}
	p := NewFromConfig(Config{Enabled: true, Styles: []string{" Deep House ", "deep-house", "Drum & Bass"}})
	if got, want := p.styles, []string{"deep-house", "drum-bass"}; !slices.Equal(got, want) {
		t.Fatalf("styles = %v, want %v", got, want)
	}
	if p.maxItems != DefaultMaxItems || p.streamCreators != DefaultStreamCreators {
		t.Fatalf("defaults = max %d stream %d", p.maxItems, p.streamCreators)
	}
}

func TestExplicitEmptyStylesDoNotRestoreDefaults(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, StylesSet: true})
	if got := p.styleSnapshot(); len(got) != 0 {
		t.Fatalf("explicit empty styles = %v, want none", got)
	}
}

func TestPublicPlaylistsNeedNoNetwork(t *testing.T) {
	p := offlineProvider(t, Config{Enabled: true, Styles: []string{"house"}})
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{recentID: true, popularID: true, "style:house:latest": true, "style:house:popular": true}
	for _, item := range lists {
		delete(want, item.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing playlist IDs: %v", want)
	}
}

func TestBrowseEntriesExposeShowsCreatorsAndGenres(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, Username: "alice"})
	entries := p.BrowseEntries()
	if len(entries) != 3 {
		t.Fatalf("browse entries = %d, want 3", len(entries))
	}
	if entries[0].ID != browseCreatorsID || entries[0].Name != "Creators" || entries[0].Section != accountSection || entries[0].Mode != provider.BrowseArtistAlbums {
		t.Fatalf("creators entry = %+v", entries[0])
	}
	if entries[1].ID != browseShowsID || entries[1].Name != "Shows" || entries[1].Mode != provider.BrowseAlbums {
		t.Fatalf("shows entry = %+v", entries[1])
	}
	if entries[2].ID != browseGenresID || entries[2].Name != "Genres" || entries[2].Mode != provider.BrowseGenres {
		t.Fatalf("genres entry = %+v", entries[2])
	}
	for _, entry := range entries {
		if entry.AfterSection != accountSection || !entry.OpenInPlaylist {
			t.Fatalf("browse entry placement/leaf behavior = %+v", entry)
		}
	}
	if entries[0].AfterID != favoritesID {
		t.Fatalf("creators placement = %+v, want immediately after Favorites", entries[0])
	}

	publicEntries := NewFromConfig(Config{Enabled: true}).BrowseEntries()
	if len(publicEntries) != 3 || publicEntries[0].ID != browseCreatorsID || !publicEntries[0].OpenInPlaylist {
		t.Fatalf("public browse capabilities = %+v, want config-independent creator behavior", publicEntries)
	}
}

func TestGenresLoadLiveCategoriesAndPersistFavorites(t *testing.T) {
	var saved []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"slug": "house", "name": "House", "format": "music"},
				map[string]any{"slug": "technology", "name": "Technology", "format": "talk"},
			}})
		case "/discover/technology/popular/":
			writeJSON(t, w, map[string]any{"data": []any{map[string]any{
				"key": "/creator/tech-show/", "name": "Tech Show", "user": map[string]any{"username": "creator"},
			}}})
		case "/search/":
			if r.URL.Query().Get("type") != "tag" {
				t.Errorf("search type = %q", r.URL.Query().Get("type"))
			}
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"key": "/genres/acid-techno/", "name": "Acid Techno"},
			}})
		default:
			http.NotFound(w, r)
		}
	}
	p, server := providerWithServer(t, Config{
		Enabled: true,
		Styles:  []string{"house", "deep-house"},
		SaveStyles: func(styles []string) error {
			saved = slices.Clone(styles)
			return nil
		},
	}, handler)
	defer server.Close()

	genres, err := p.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 3 {
		t.Fatalf("genres = %+v, want two live categories plus configured deep-house", genres)
	}
	if !genres[0].Favorite || genres[0].Name != "House" || genres[0].Group != "Music" {
		t.Fatalf("house genre = %+v", genres[0])
	}
	if genres[1].Favorite || genres[1].Group != "Talk" {
		t.Fatalf("technology genre = %+v", genres[1])
	}
	if !genres[2].Favorite || genres[2].ID != "deep-house" {
		t.Fatalf("configured-only genre = %+v", genres[2])
	}

	favorite, err := p.ToggleGenreFavorite("technology")
	if err != nil || !favorite {
		t.Fatalf("ToggleGenreFavorite = %v, %v", favorite, err)
	}
	if !slices.Equal(saved, []string{"house", "deep-house", "technology"}) {
		t.Fatalf("saved styles = %v", saved)
	}
	tracks, err := p.GenreTracks("technology", "popular")
	if err != nil || len(tracks) != 1 || tracks[0].Title != "Tech Show" {
		t.Fatalf("GenreTracks = %+v, %v", tracks, err)
	}
	results, err := p.SearchGenres(context.Background(), "acid techno", 20)
	if err != nil || len(results) != 1 || results[0].ID != "acid-techno" || results[0].Name != "Acid Techno" {
		t.Fatalf("SearchGenres = %+v, %v", results, err)
	}
}

func TestGenreFavoriteSaveFailureKeepsProviderState(t *testing.T) {
	p := NewFromConfig(Config{
		Enabled: true,
		Styles:  []string{"house"},
		SaveStyles: func([]string) error {
			return errors.New("disk full")
		},
	})
	if favorite, err := p.ToggleGenreFavorite("techno"); err == nil || favorite || !strings.Contains(err.Error(), "save genre favorites") {
		t.Fatalf("ToggleGenreFavorite = %v, %v, want failed and unchanged", favorite, err)
	}
	if got := p.styleSnapshot(); !slices.Equal(got, []string{"house"}) {
		t.Fatalf("styles after failed save = %v", got)
	}
}

func TestTracksMapsCapturedMixcloudFields(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discover/all/latest/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{"data": []any{map[string]any{
			"key":          "/spartacus/cinematic-soundscapes/",
			"url":          "https://www.mixcloud.com/spartacus/cinematic-soundscapes/",
			"name":         "Cinematic Soundscapes",
			"created_time": "2014-09-10T14:42:03Z",
			"audio_length": 2631,
			"tags":         []any{map[string]any{"name": "Chillout"}, map[string]any{"name": "Soundtrack"}},
			"pictures":     map[string]any{"extra_large": "https://img.example/show.jpg"},
			"user":         map[string]any{"name": "Spartacus", "username": "spartacus"},
		}}})
	}
	p, server := providerWithServer(t, Config{Enabled: true, MaxItems: 1}, handler)
	defer server.Close()

	tracks, err := p.Tracks(recentID)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d", len(tracks))
	}
	track := tracks[0]
	if track.Path != "https://www.mixcloud.com/spartacus/cinematic-soundscapes/" || !track.Stream {
		t.Fatalf("stable playback URL mapping = %+v", track)
	}
	if track.Title != "Cinematic Soundscapes" || track.Artist != "Spartacus" || track.DurationSecs != 2631 || track.Year != 2014 {
		t.Fatalf("metadata mapping = %+v", track)
	}
	if track.Genre != "Chillout, Soundtrack" || track.AlbumArtURL != "https://img.example/show.jpg" {
		t.Fatalf("rich metadata mapping = %+v", track)
	}
	if track.Meta(provider.MetaMixcloudKey) != "/spartacus/cinematic-soundscapes/" {
		t.Fatalf("mixcloud key = %q", track.Meta(provider.MetaMixcloudKey))
	}
	if track.Meta(provider.MetaMixcloudCreator) != "spartacus" {
		t.Fatalf("mixcloud creator = %q", track.Meta(provider.MetaMixcloudCreator))
	}
}

func TestExclusiveShowsKeepCleanMetadataAndAreNotSkipped(t *testing.T) {
	show := apiCloudcast{
		Key:         "/creator/members-only/",
		URL:         "https://www.mixcloud.com/creator/members-only/",
		Name:        "Members Only",
		User:        apiUser{Name: "Creator", Username: "creator"},
		IsExclusive: true,
	}

	track := trackFromCloudcast(show)
	if track.Title != "Members Only" {
		t.Fatalf("exclusive track title = %q", track.Title)
	}
	if track.Unplayable {
		t.Fatal("exclusive show was marked unplayable before session entitlement was checked")
	}
	if track.Meta(provider.MetaMixcloudExclusive) != "true" {
		t.Fatalf("exclusive metadata = %q", track.Meta(provider.MetaMixcloudExclusive))
	}
	albums := albumsFromCloudcasts([]apiCloudcast{show})
	if len(albums) != 1 || albums[0].Name != "Members Only" || !albums[0].Restricted {
		t.Fatalf("exclusive show browse metadata = %+v", albums)
	}
}

func TestAccountPlaylistErrorsPreservePublicDiscovery(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		failure string
		status  int
	}{
		{
			name:    "invalid configured username",
			cfg:     Config{Enabled: true, Username: "missing", Styles: []string{"house"}},
			failure: "/missing/playlists/",
			status:  http.StatusNotFound,
		},
		{
			name:    "invalid access token",
			cfg:     Config{Enabled: true, AccessToken: "expired", Styles: []string{"house"}},
			failure: "/me/",
			status:  http.StatusUnauthorized,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, server := providerWithServer(t, tt.cfg, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.failure {
					t.Errorf("path = %q, want %q", r.URL.Path, tt.failure)
				}
				w.WriteHeader(tt.status)
				writeJSON(t, w, map[string]any{"error": map[string]any{"message": "User does not exist."}})
			})
			defer server.Close()

			lists, err := p.Playlists()
			if err == nil || !strings.Contains(err.Error(), "account views unavailable") {
				t.Fatalf("Playlists error = %v, want account warning", err)
			}
			for _, id := range []string{recentID, popularID, "style:house:latest", "style:house:popular"} {
				if !slices.ContainsFunc(lists, func(item playlist.PlaylistInfo) bool { return item.ID == id }) {
					t.Errorf("public lists omit %q: %+v", id, lists)
				}
			}
			if slices.ContainsFunc(lists, func(item playlist.PlaylistInfo) bool { return item.ID == streamID }) {
				t.Fatalf("unavailable account view included: %+v", lists)
			}
		})
	}
}

func TestSearchPreservesCanceledContext(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.SearchTracks(ctx, "house", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "search shows") {
		t.Fatalf("error = %v, want search operation context", err)
	}
	_, err = p.SearchGenres(ctx, "house", 10)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "search genres") {
		t.Fatalf("genre search error = %v, want wrapped context.Canceled", err)
	}
}

func TestTracksWrapsPlaylistOperation(t *testing.T) {
	p, server := providerWithServer(t, Config{Enabled: true}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	_, err := p.Tracks(recentID)
	if err == nil || !strings.Contains(err.Error(), `load playlist "discover:latest"`) {
		t.Fatalf("Tracks error = %v, want playlist operation context", err)
	}
}

func TestAccountPlaylistsUseMeWithToken(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "token" {
			t.Errorf("missing access token")
		}
		switch r.URL.Path {
		case "/me/":
			writeJSON(t, w, map[string]any{"username": "token-owner", "name": "Token Owner"})
		case "/me/playlists/":
			writeJSON(t, w, map[string]any{"data": []any{map[string]any{
				"key": "/token-owner/playlists/late-night/", "name": "Late Night", "cloudcast_count": 12,
			}}})
		case "/me/following/":
			writeJSON(t, w, map[string]any{"data": []any{}})
		default:
			http.NotFound(w, r)
		}
	}
	p, server := providerWithServer(t, Config{Enabled: true, Username: "alice", AccessToken: "token", Styles: []string{"house"}}, handler)
	defer server.Close()

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	wantPrefix := []string{streamID, favoritesID, uploadsID, activityID, listensID, listenLaterID}
	if len(lists) < len(wantPrefix) {
		t.Fatalf("account lists = %+v", lists)
	}
	for i, id := range wantPrefix {
		if lists[i].ID != id || lists[i].Section != accountSection {
			t.Fatalf("account list %d = %+v, want %q in %q", i, lists[i], id, accountSection)
		}
	}
	var collectionFound, listenLaterFound bool
	for _, item := range lists {
		collectionFound = collectionFound || item.ID == "collection:/token-owner/playlists/late-night/" && item.TrackCount == 12
		listenLaterFound = listenLaterFound || item.ID == listenLaterID
	}
	if !collectionFound || !listenLaterFound {
		t.Fatalf("collection=%v listen-later=%v lists=%v", collectionFound, listenLaterFound, lists)
	}
	artists, err := p.Artists()
	if err != nil {
		t.Fatalf("Artists: %v", err)
	}
	if len(artists) != 1 || artists[0].ID != "token-owner" {
		t.Fatalf("token-backed account identity = %+v, want token owner", artists)
	}
}

func TestFollowingStreamMergesNewestAndDeduplicates(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alice/following/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"username": "one"}, map[string]any{"username": "two"},
			}})
		case "/one/cloudcasts/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"key": "/one/old/", "name": "Old", "created_time": "2026-01-01T00:00:00Z", "user": map[string]any{"username": "one"}},
				map[string]any{"key": "/shared/show/", "name": "Shared", "created_time": "2026-03-01T00:00:00Z", "user": map[string]any{"username": "shared"}},
			}})
		case "/two/cloudcasts/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"key": "/two/new/", "name": "New", "created_time": "2026-04-01T00:00:00Z", "user": map[string]any{"username": "two"}},
				map[string]any{"key": "/shared/show/", "name": "Shared", "created_time": "2026-03-01T00:00:00Z", "user": map[string]any{"username": "shared"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}
	p, server := providerWithServer(t, Config{Enabled: true, Username: "alice", MaxItems: 10, StreamCreators: 2}, handler)
	defer server.Close()

	tracks, err := p.Tracks(streamID)
	if err != nil {
		t.Fatalf("Tracks(stream): %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("tracks = %d, want 3 merged and deduplicated shows", len(tracks))
	}
	if got := []string{tracks[0].Title, tracks[1].Title, tracks[2].Title}; !slices.Equal(got, []string{"New", "Shared", "Old"}) {
		t.Fatalf("titles = %v", got)
	}
}

func TestFollowingStreamPreservesPartialRateLimit(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alice/following/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"username": "working"}, map[string]any{"username": "limited"},
			}})
		case "/working/cloudcasts/":
			writeJSON(t, w, map[string]any{"data": []any{
				map[string]any{"key": "/working/show/", "name": "Show", "user": map[string]any{"username": "working"}},
			}})
		case "/limited/cloudcasts/":
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusForbidden)
			writeJSON(t, w, map[string]any{"error": map[string]any{"type": "RateLimitException", "message": "slow down"}})
		default:
			http.NotFound(w, r)
		}
	}
	p, server := providerWithServer(t, Config{Enabled: true, Username: "alice", MaxItems: 10, StreamCreators: 2}, handler)
	defer server.Close()

	_, err := p.Tracks(streamID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 12*time.Second {
		t.Fatalf("error = %T %v, want preserved rate limit", err, err)
	}
}

func TestProviderRejectsMalformedRecordsAndKeys(t *testing.T) {
	if got := tracksFromCloudcasts([]apiCloudcast{{Name: "missing identity"}}); len(got) != 0 {
		t.Fatalf("tracks = %v, want malformed item omitted", got)
	}
	if got := albumsFromCloudcasts([]apiCloudcast{{Key: "/user/../show/"}}); len(got) != 0 {
		t.Fatalf("albums = %v, want malformed item omitted", got)
	}

	p := NewFromConfig(Config{Enabled: true})
	if _, err := p.Tracks("collection:/user/../private/"); err == nil {
		t.Fatal("collection traversal key was accepted")
	}
	if _, err := p.AlbumTracks("https://attacker.example/show/"); err == nil {
		t.Fatal("external show key was accepted")
	}
}

func TestCreatorBrowserSeparatesUploadsAndFavorites(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/creator/cloudcasts/":
			writeJSON(t, w, map[string]any{"data": []any{map[string]any{
				"key": "/creator/upload/", "name": "Own Show", "user": map[string]any{"username": "creator"},
			}}})
		case "/creator/favorites/":
			writeJSON(t, w, map[string]any{"data": []any{map[string]any{
				"key": "/someone/favorite/", "name": "Saved Show", "user": map[string]any{"username": "someone"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}
	p, server := providerWithServer(t, Config{Enabled: true, MaxItems: 20}, handler)
	defer server.Close()

	collections, err := p.ArtistAlbums("creator")
	if err != nil {
		t.Fatalf("ArtistAlbums: %v", err)
	}
	if len(collections) != 2 || collections[0].Name != "Uploads" || collections[1].Name != "Favorites" {
		t.Fatalf("creator collections = %+v", collections)
	}

	uploads, err := p.AlbumTracks(collections[0].ID)
	if err != nil {
		t.Fatalf("uploads: %v", err)
	}
	favorites, err := p.AlbumTracks(collections[1].ID)
	if err != nil {
		t.Fatalf("favorites: %v", err)
	}
	if len(uploads) != 1 || uploads[0].Title != "Own Show" || uploads[0].Album != "Uploads" {
		t.Fatalf("upload tracks = %+v", uploads)
	}
	if len(favorites) != 1 || favorites[0].Title != "Saved Show" || favorites[0].Album != "Favorites" {
		t.Fatalf("favorite tracks = %+v", favorites)
	}
}

func TestArtistForTrackUsesOwningCreator(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	track := trackFromCloudcast(apiCloudcast{
		Key:  "/owner/show/",
		Name: "Show",
		User: apiUser{Username: "owner", Name: "Owner Name"},
	})
	artist, ok := p.ArtistForTrack(track)
	if !ok || artist.ID != "owner" || artist.Name != "Owner Name" {
		t.Fatalf("ArtistForTrack = %+v, %v", artist, ok)
	}
	if _, ok := p.ArtistForTrack(playlist.Track{Title: "Local"}); ok {
		t.Fatal("ArtistForTrack claimed a non-Mixcloud track")
	}
}

func TestStyleAlbumSortAndLabels(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, Styles: []string{"deep house"}})
	artist, album := p.BrowseLabels()
	if artist != "Creator" || album != "Show" {
		t.Fatalf("labels = %q %q", artist, album)
	}
	sorts := p.AlbumSortTypes()
	if !slices.ContainsFunc(sorts, func(item provider.SortType) bool {
		return item.ID == "style:deep-house:popular" && strings.Contains(item.Label, "Deep House")
	}) {
		t.Fatalf("style sort missing: %v", sorts)
	}
	if _, ok := p.albumSortPath("deep-house:latest"); ok {
		t.Fatal("sort without style: prefix was accepted")
	}
}
