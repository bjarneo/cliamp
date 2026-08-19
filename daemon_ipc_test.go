package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui/model"
)

type daemonTestProvider struct{}

func (daemonTestProvider) Name() string { return "Test" }
func (daemonTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "mix", Name: "Mix", TrackCount: 2}}, nil
}
func (daemonTestProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/one.flac", Title: "One"}, {Path: "/two.flac", Title: "Two"}}, nil
}
func (daemonTestProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/result.flac", Title: "Result"}}, nil
}
func (daemonTestProvider) Artists() ([]provider.ArtistInfo, error) {
	return []provider.ArtistInfo{{ID: "artist", Name: "Artist", AlbumCount: 1}}, nil
}
func (daemonTestProvider) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album", Artist: "Artist", TrackCount: 2}}, nil
}
func (daemonTestProvider) AlbumList(string, int, int) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album"}}, nil
}
func (daemonTestProvider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "name", Label: "By name"}}
}
func (daemonTestProvider) DefaultAlbumSort() string { return "name" }
func (daemonTestProvider) AlbumTracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/album.flac", Title: "Album track"}}, nil
}

type daemonCatalogProvider struct {
	searching bool
	favorite  string
}

func (p *daemonCatalogProvider) Name() string { return "Catalog" }
func (p *daemonCatalogProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "c:station", Name: "Station"}}, nil
}
func (p *daemonCatalogProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://radio.example/stream", Title: "Station", Stream: true}}, nil
}
func (p *daemonCatalogProvider) SearchCatalog(string) (int, error)   { p.searching = true; return 1, nil }
func (*daemonCatalogProvider) LoadCatalogPage(int, int) (int, error) { return 1, nil }
func (p *daemonCatalogProvider) ClearSearch()                        { p.searching = false }
func (p *daemonCatalogProvider) IsSearching() bool                   { return p.searching }
func (*daemonCatalogProvider) IDPrefix(string) string                { return "c" }
func (*daemonCatalogProvider) IsFavoritableID(id string) bool        { return id == "c:station" }
func (p *daemonCatalogProvider) ToggleFavorite(id string) (bool, string, error) {
	p.favorite = id
	return true, "Station", nil
}

type daemonWritableProvider struct {
	created, renamed, deleted, added, bookmarked string
	removed                                      int
}

func (*daemonWritableProvider) Name() string                                { return "Writable" }
func (*daemonWritableProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (*daemonWritableProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (p *daemonWritableProvider) CreatePlaylist(_ context.Context, name string) (string, error) {
	p.created = name
	return name, nil
}
func (p *daemonWritableProvider) RenamePlaylist(oldName, newName string) error {
	p.renamed = oldName + ":" + newName
	return nil
}
func (p *daemonWritableProvider) DeletePlaylist(name string) error {
	p.deleted = name
	return nil
}
func (p *daemonWritableProvider) RemoveTrack(_ string, index int) error {
	p.removed = index
	return nil
}
func (p *daemonWritableProvider) AddTrackToPlaylist(_ context.Context, name string, track playlist.Track) error {
	p.added = name + ":" + track.Path
	return nil
}
func (*daemonWritableProvider) SetBookmark(string, int) error { return nil }
func (p *daemonWritableProvider) SetBookmarkByPath(name, path string) error {
	p.bookmarked = name + ":" + path
	return nil
}

func TestDaemonLibraryRequests(t *testing.T) {
	d := &daemon{providers: []model.ProviderEntry{{Key: "test", Name: "Test", Provider: daemonTestProvider{}}}}

	providerReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.list", Reply: providerReply})
	if got := <-providerReply; len(got.Providers) != 1 || !got.Providers[0].Searchable || !got.Providers[0].BrowseArtists || !got.Providers[0].BrowseAlbums {
		t.Fatalf("providers = %#v", got.Providers)
	}

	playlistReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.playlists", Provider: "test", Reply: playlistReply})
	if got := <-playlistReply; len(got.Playlists) != 1 || got.Playlists[0].ID != "mix" {
		t.Fatalf("playlists = %#v", got.Playlists)
	}

	searchReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.search", Provider: "test", Query: "result", Reply: searchReply})
	if got := <-searchReply; len(got.Tracks) != 1 || got.Tracks[0].Title != "Result" {
		t.Fatalf("tracks = %#v", got.Tracks)
	}

	artistReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.artists", Provider: "test", Reply: artistReply})
	if got := <-artistReply; len(got.Artists) != 1 || got.Artists[0].Name != "Artist" {
		t.Fatalf("artists = %#v", got.Artists)
	}

	albumReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.albums", Provider: "test", Reply: albumReply})
	if got := <-albumReply; len(got.Albums) != 1 || len(got.Sorts) != 1 || got.Albums[0].Name != "Album" {
		t.Fatalf("albums = %#v, sorts = %#v", got.Albums, got.Sorts)
	}

	trackReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.album_tracks", Provider: "test", Album: "album", Reply: trackReply})
	if got := <-trackReply; len(got.Tracks) != 1 || got.Tracks[0].Title != "Album track" {
		t.Fatalf("album tracks = %#v", got.Tracks)
	}
}

func TestDaemonQueueListIncludesMetadata(t *testing.T) {
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/one.flac", Title: "One", Album: "Album", DurationSecs: 60},
		playlist.Track{Path: "/two.flac", Title: "Two"},
	)
	pl.Queue(1)
	d := &daemon{playlist: pl}

	response := d.queueResponse()
	if len(response.Tracks) != 2 || response.Tracks[0].Album != "Album" || response.Tracks[1].QueuePosition != 1 {
		t.Fatalf("queue response = %#v", response.Tracks)
	}
}

func TestTrackInfoConversion(t *testing.T) {
	track := playlist.Track{Path: "https://example.com/stream", Title: "Stream", Artist: "Artist", Realtime: true, Bookmark: true}
	info := trackInfo(track, 3, 2)
	converted := trackFromInfo(info)
	if info.Index != 3 || info.QueuePosition != 2 || converted.Path != track.Path || !converted.Stream || !converted.Realtime || !converted.Bookmark {
		t.Fatalf("conversion lost metadata: info=%#v converted=%#v", info, converted)
	}
}

func TestSearchProviderAdaptsCatalogSearch(t *testing.T) {
	provider := &daemonCatalogProvider{}
	tracks, err := searchProvider(provider, "station", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Station" || provider.searching {
		t.Fatalf("catalog search = %#v, searching=%v", tracks, provider.searching)
	}
}

func TestDaemonTogglesProviderFavorite(t *testing.T) {
	provider := &daemonCatalogProvider{}
	d := &daemon{providers: []model.ProviderEntry{{Key: "radio", Name: "Radio", Provider: provider}}}
	reply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.favorite", Provider: "radio", Playlist: "c:station", Reply: reply})
	if response := <-reply; !response.OK || provider.favorite != "c:station" {
		t.Fatalf("favorite response=%#v provider=%#v", response, provider)
	}
	listsReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.playlists", Provider: "radio", Reply: listsReply})
	if response := <-listsReply; len(response.Playlists) != 1 || !response.Playlists[0].Favoritable {
		t.Fatalf("playlist capabilities=%#v", response.Playlists)
	}
	catalogReply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "provider.catalog", Provider: "radio", Limit: 50, Reply: catalogReply})
	if response := <-catalogReply; response.Total != 1 || len(response.Playlists) != 1 {
		t.Fatalf("catalog response=%#v", response)
	}
}

func TestDaemonPlaylistMutations(t *testing.T) {
	provider := &daemonWritableProvider{removed: -1}
	d := &daemon{providers: []model.ProviderEntry{{Key: "local", Name: "Local", Provider: provider}}}
	track := ipc.TrackInfo{Path: "/song.flac"}
	requests := []ipc.LibraryRequestMsg{
		{Op: "playlist.create", Provider: "local", Playlist: "Mix"},
		{Op: "playlist.rename", Provider: "local", Playlist: "Mix", NewName: "New"},
		{Op: "playlist.delete", Provider: "local", Playlist: "Old"},
		{Op: "playlist.remove", Provider: "local", Playlist: "Mix", Index: 3},
		{Op: "playlist.add", Provider: "local", Playlist: "Mix", Track: &track},
		{Op: "playlist.bookmark", Provider: "local", Playlist: "Mix", Track: &track},
	}
	for _, request := range requests {
		request.Reply = make(chan ipc.Response, 1)
		d.handleLibrary(request)
		if response := <-request.Reply; !response.OK {
			t.Fatalf("%s failed: %s", request.Op, response.Error)
		}
	}
	if provider.created != "Mix" || provider.renamed != "Mix:New" || provider.deleted != "Old" || provider.removed != 3 || provider.added != "Mix:/song.flac" || provider.bookmarked != "Mix:/song.flac" {
		t.Fatalf("mutations were not forwarded: %#v", provider)
	}
}

func TestDaemonClearsHistory(t *testing.T) {
	store := history.NewAt(t.TempDir() + "/history.toml")
	if err := store.Record(playlist.Track{Path: "/song.flac"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	d := &daemon{historyStore: store}
	reply := make(chan ipc.Response, 1)
	d.handleHistory(ipc.HistoryRequestMsg{Op: "history.clear", Reply: reply})
	if response := <-reply; !response.OK {
		t.Fatal(response.Error)
	}
	entries, err := store.Recent(0)
	if err != nil || len(entries) != 0 {
		t.Fatalf("history after clear = %#v, err=%v", entries, err)
	}
}

// The ipc package returns a bare sentinel; the CLI wording is added here.
func TestUserIPCErrorRendersNotRunning(t *testing.T) {
	rendered := userIPCError(fmt.Errorf("dial: %w", ipc.ErrNotRunning))
	want := fmt.Sprintf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	if rendered.Error() != want {
		t.Errorf("rendered = %q, want %q", rendered.Error(), want)
	}

	other := errors.New("connect: permission denied")
	if got := userIPCError(other); got != other {
		t.Errorf("unrelated error rewritten to %v", got)
	}
}
