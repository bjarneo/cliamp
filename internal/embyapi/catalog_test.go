package embyapi

import (
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

// catalogAlbums is the album catalog that catalogClient serves.
const catalogAlbums = `{"Items":[
	{"Id":"album-1","Name":"Kind of Blue","AlbumArtist":"Miles Davis","AlbumArtists":[{"Id":"artist-1","Name":"Miles Davis"}],"ProductionYear":1959,"ChildCount":5},
	{"Id":"album-2","Name":"Bitches Brew","AlbumArtists":[{"Id":"artist-1","Name":"Miles Davis"}],"ProductionYear":1970,"ChildCount":6},
	{"Id":"album-3","Name":"A Love Supreme","AlbumArtist":"John Coltrane","AlbumArtists":[{"Id":"artist-2","Name":"John Coltrane"}],"ProductionYear":1965,"ChildCount":4},
	{"Id":"album-4","Name":"Saxophone Colossus","AlbumArtist":"Sonny Rollins","ProductionYear":1956,"ChildCount":5},
	{"Id":"album-5","Name":"Untitled"}
]}`

// catalogClients returns one client for each dialect. Each client serves
// catalogAlbums from one music library and counts its /Items requests.
func catalogClients(t *testing.T) map[string]func(items *int) *Client {
	t.Helper()
	transport := func(items *int) roundTripFunc {
		return func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/Users/user-1/Views":
				return jsonResponse(`{"Items":[{"Id":"lib-1","Name":"Music","CollectionType":"music"},{"Id":"lib-2","Name":"Films","CollectionType":"movies"}]}`), nil
			case "/Items":
				if got := req.URL.Query().Get("parentId"); got != "lib-1" {
					t.Errorf("parentId = %q, want lib-1", got)
				}
				*items++
				return jsonResponse(catalogAlbums), nil
			default:
				t.Errorf("unexpected path %s", req.URL.Path)
				return jsonResponse(`{}`), nil
			}
		}
	}
	return map[string]func(items *int) *Client{
		"jellyfin": func(items *int) *Client {
			return mock(NewJellyfinClient("https://jf.example.com", "tok", "user-1", "", ""), transport(items))
		},
		"emby": func(items *int) *Client {
			return mock(NewEmbyClient("https://emby.example.com", "tok", "user-1", "", ""), transport(items))
		},
	}
}

func albumNames(albums []provider.AlbumInfo) []string {
	names := make([]string, 0, len(albums))
	for _, a := range albums {
		names = append(names, a.Name)
	}
	return names
}

func TestArtists(t *testing.T) {
	for dialect, newClient := range catalogClients(t) {
		t.Run(dialect, func(t *testing.T) {
			var items int
			artists, err := newClient(&items).Artists()
			if err != nil {
				t.Fatalf("Artists() error: %v", err)
			}
			want := []provider.ArtistInfo{
				{ID: "artist-2", Name: "John Coltrane", AlbumCount: 1},
				{ID: "artist-1", Name: "Miles Davis", AlbumCount: 2},
				{ID: "name:sonny rollins", Name: "Sonny Rollins", AlbumCount: 1},
			}
			if !slices.Equal(artists, want) {
				t.Fatalf("Artists() = %+v, want %+v", artists, want)
			}
		})
	}
}

func TestArtistAlbums(t *testing.T) {
	tests := []struct {
		artistID string
		want     []string
	}{
		{"artist-1", []string{"Bitches Brew", "Kind of Blue"}},
		{"name:sonny rollins", []string{"Saxophone Colossus"}},
		{"", []string{"A Love Supreme", "Bitches Brew", "Kind of Blue", "Saxophone Colossus", "Untitled"}},
		{"artist-unknown", []string{}},
	}
	for dialect, newClient := range catalogClients(t) {
		for _, tt := range tests {
			t.Run(dialect+"/"+tt.artistID, func(t *testing.T) {
				var items int
				albums, err := newClient(&items).ArtistAlbums(tt.artistID)
				if err != nil {
					t.Fatalf("ArtistAlbums() error: %v", err)
				}
				if got := albumNames(albums); !slices.Equal(got, tt.want) {
					t.Fatalf("ArtistAlbums(%q) = %v, want %v", tt.artistID, got, tt.want)
				}
				for _, a := range albums {
					if a.ArtistID == "" && a.Artist != "" {
						t.Fatalf("album %q has no canonical artist id", a.Name)
					}
				}
			})
		}
	}
}

func TestAlbumList(t *testing.T) {
	tests := []struct {
		sortType     string
		offset, size int
		want         []string
	}{
		{SortAlbumsByName, 0, 0, []string{"A Love Supreme", "Bitches Brew", "Kind of Blue", "Saxophone Colossus", "Untitled"}},
		{SortAlbumsByArtist, 0, 0, []string{"Untitled", "A Love Supreme", "Bitches Brew", "Kind of Blue", "Saxophone Colossus"}},
		{SortAlbumsByYear, 0, 0, []string{"Bitches Brew", "A Love Supreme", "Kind of Blue", "Saxophone Colossus", "Untitled"}},
		{"unknown", 0, 0, []string{"A Love Supreme", "Bitches Brew", "Kind of Blue", "Saxophone Colossus", "Untitled"}},
		{SortAlbumsByName, 1, 2, []string{"Bitches Brew", "Kind of Blue"}},
		{SortAlbumsByName, 4, 10, []string{"Untitled"}},
		{SortAlbumsByName, -3, 1, []string{"A Love Supreme"}},
		{SortAlbumsByName, 5, 10, []string{}},
	}
	for dialect, newClient := range catalogClients(t) {
		for _, tt := range tests {
			name := dialect + "/" + tt.sortType + "/" + strconv.Itoa(tt.offset) + "+" + strconv.Itoa(tt.size)
			t.Run(name, func(t *testing.T) {
				var items int
				albums, err := newClient(&items).AlbumList(tt.sortType, tt.offset, tt.size)
				if err != nil {
					t.Fatalf("AlbumList() error: %v", err)
				}
				if got := albumNames(albums); !slices.Equal(got, tt.want) {
					t.Fatalf("AlbumList(%q, %d, %d) = %v, want %v", tt.sortType, tt.offset, tt.size, got, tt.want)
				}
			})
		}
	}
}

func TestAlbumsCacheUntilClearCache(t *testing.T) {
	for dialect, newClient := range catalogClients(t) {
		t.Run(dialect, func(t *testing.T) {
			var items int
			c := newClient(&items)
			for range 2 {
				if _, err := c.Albums(); err != nil {
					t.Fatalf("Albums() error: %v", err)
				}
			}
			if items != 1 {
				t.Fatalf("/Items requests = %d after 2 calls, want 1", items)
			}
			c.ClearCache()
			if _, err := c.Albums(); err != nil {
				t.Fatalf("Albums() error: %v", err)
			}
			if items != 2 {
				t.Fatalf("/Items requests = %d after ClearCache, want 2", items)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		wantLimit string
	}{
		{"explicit limit", 10, "10"},
		{"default limit", 0, "50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mock(NewEmbyClient("https://emby.example.com", "tok", "user-1", "", ""), func(req *http.Request) (*http.Response, error) {
				q := req.URL.Query()
				if req.URL.Path != "/Items" || q.Get("searchTerm") != "so what" || q.Get("includeItemTypes") != "Audio" || q.Get("recursive") != "true" {
					t.Fatalf("unexpected request %s?%s", req.URL.Path, req.URL.RawQuery)
				}
				if got := q.Get("limit"); got != tt.wantLimit {
					t.Fatalf("limit = %q, want %s", got, tt.wantLimit)
				}
				return jsonResponse(`{"Items":[
					{"Id":"track-1","Name":"So What","Album":"Kind of Blue","Artists":["Miles Davis"],"RunTimeTicks":5650000000},
					{"Id":"track-2","Name":"So What (live)","ArtistItems":[{"Id":"artist-1","Name":"Miles Davis Quintet"}]}
				]}`), nil
			})
			tracks, err := c.Search("so what", tt.limit)
			if err != nil {
				t.Fatalf("Search() error: %v", err)
			}
			want := []Track{
				{ID: "track-1", Name: "So What", Artist: "Miles Davis", Album: "Kind of Blue", DurationSecs: 565},
				{ID: "track-2", Name: "So What (live)", Artist: "Miles Davis Quintet"},
			}
			if !slices.Equal(tracks, want) {
				t.Fatalf("Search() = %+v, want %+v", tracks, want)
			}
		})
	}
}
