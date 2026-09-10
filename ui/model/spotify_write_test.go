package model

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// fakeSpotifyProvider implements every wave-2 capability interface so tests
// can exercise the UI side against the frozen contracts.
type fakeSpotifyProvider struct {
	name  string
	lists []playlist.PlaylistInfo
	err   error

	allTracks []playlist.Track
	pageLimit int      // >0 clamps TracksPage responses (simulates server page size)
	pageCalls []string // "id:offset:limit"

	searchTracks []playlist.Track
	searchAll    provider.SearchResults

	albumTracks  map[string][]playlist.Track
	artistAlbums map[string][]provider.AlbumInfo
	artistTop    map[string][]playlist.Track
	artistsList  []provider.ArtistInfo

	liked               []string
	likeResult          bool
	removedFrom         []string // "playlistID:position"
	renames             [][2]string
	followedArtists     []string
	unfollowedArtists   []string
	followedPlaylists   []string
	unfollowedPlaylists []string
	addedTracks         []string // playlistID per single add
	batchAdds           []string // playlistID per batch add
	created             []string
}

func (f *fakeSpotifyProvider) Name() string { return f.name }

func (f *fakeSpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return append([]playlist.PlaylistInfo(nil), f.lists...), f.err
}

func (f *fakeSpotifyProvider) Tracks(string) ([]playlist.Track, error) {
	return append([]playlist.Track(nil), f.allTracks...), f.err
}

func (f *fakeSpotifyProvider) TracksPage(id string, offset, limit int) ([]playlist.Track, int, error) {
	f.pageCalls = append(f.pageCalls, id+":"+strconv.Itoa(offset)+":"+strconv.Itoa(limit))
	if f.pageLimit > 0 && limit > f.pageLimit {
		limit = f.pageLimit
	}
	if offset < 0 || offset >= len(f.allTracks) {
		return nil, len(f.allTracks), f.err
	}
	end := min(offset+limit, len(f.allTracks))
	return f.allTracks[offset:end], len(f.allTracks), f.err
}

func (f *fakeSpotifyProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return append([]playlist.Track(nil), f.searchTracks...), f.err
}

func (f *fakeSpotifyProvider) SearchAll(context.Context, string, int) (provider.SearchResults, error) {
	return f.searchAll, f.err
}

func (f *fakeSpotifyProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return f.albumTracks[albumID], f.err
}

func (f *fakeSpotifyProvider) Artists() ([]provider.ArtistInfo, error) {
	return f.artistsList, f.err
}

func (f *fakeSpotifyProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	return f.artistAlbums[artistID], f.err
}

func (f *fakeSpotifyProvider) ArtistTopTracks(artistID string) ([]playlist.Track, error) {
	return f.artistTop[artistID], f.err
}

func (f *fakeSpotifyProvider) ToggleTrackLike(_ context.Context, track playlist.Track) (bool, error) {
	f.liked = append(f.liked, track.Path)
	return f.likeResult, f.err
}

func (f *fakeSpotifyProvider) FollowPlaylistByID(_ context.Context, playlistID string) error {
	f.followedPlaylists = append(f.followedPlaylists, playlistID)
	return f.err
}

func (f *fakeSpotifyProvider) UnfollowPlaylistByID(_ context.Context, playlistID string) error {
	f.unfollowedPlaylists = append(f.unfollowedPlaylists, playlistID)
	filtered := f.lists[:0]
	for _, pl := range f.lists {
		if pl.ID != playlistID {
			filtered = append(filtered, pl)
		}
	}
	f.lists = filtered
	return f.err
}

func (f *fakeSpotifyProvider) FollowArtist(_ context.Context, artistID string) error {
	f.followedArtists = append(f.followedArtists, artistID)
	return f.err
}

func (f *fakeSpotifyProvider) UnfollowArtist(_ context.Context, artistID string) error {
	f.unfollowedArtists = append(f.unfollowedArtists, artistID)
	return f.err
}

func (f *fakeSpotifyProvider) RemoveTrackFromPlaylist(_ context.Context, playlistID string, position int) error {
	f.removedFrom = append(f.removedFrom, playlistID+":"+strconv.Itoa(position))
	return f.err
}

func (f *fakeSpotifyProvider) RenamePlaylistByID(_ context.Context, playlistID, newName string) error {
	f.renames = append(f.renames, [2]string{playlistID, newName})
	return f.err
}

func (f *fakeSpotifyProvider) AddTrackToPlaylist(_ context.Context, playlistID string, _ playlist.Track) error {
	f.addedTracks = append(f.addedTracks, playlistID)
	return f.err
}

func (f *fakeSpotifyProvider) AddTracksToPlaylist(_ context.Context, playlistID string, tracks []playlist.Track) (int, int, error) {
	f.batchAdds = append(f.batchAdds, playlistID)
	return len(tracks), 0, f.err
}

func (f *fakeSpotifyProvider) CreatePlaylist(_ context.Context, name string) (string, error) {
	f.created = append(f.created, name)
	return "new-" + name, f.err
}

func (f *fakeSpotifyProvider) URISchemes() []string { return []string{"spotify"} }

func (f *fakeSpotifyProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, nil
}

// trackSearchProvider implements only Searcher + TrackLiker + CustomStreamer,
// exercising the plain single-list search path.
type trackSearchProvider struct {
	commandsTestProvider
	tracks []playlist.Track
	liked  []string
}

func (p *trackSearchProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return p.tracks, nil
}

func (p *trackSearchProvider) ToggleTrackLike(_ context.Context, track playlist.Track) (bool, error) {
	p.liked = append(p.liked, track.Path)
	return true, nil
}

func (p *trackSearchProvider) URISchemes() []string { return []string{"spotify"} }

func (p *trackSearchProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, nil
}

func newSpotifyTestModel(fake *fakeSpotifyProvider) Model {
	player := &playbackFakeEngine{}
	return Model{
		player:        player,
		playlist:      playlist.New(),
		localProvider: commandsTestProvider{name: "Local"},
		provider:      fake,
		providers:     []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: fake}},
		vis:           ui.NewVisualizer(float64(player.SampleRate())),
	}
}

func spotifyTracks(n int) []playlist.Track {
	tracks := make([]playlist.Track, n)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: "spotify:track:" + strconv.Itoa(i), Title: "T" + strconv.Itoa(i), Artist: "A"}
	}
	return tracks
}

// loadFakePlaylist drives the full paginated load of a fake playlist into the
// queue and returns the model with every outstanding message applied.
func loadFakePlaylist(t *testing.T, m Model, fake *fakeSpotifyProvider, playlistID string) Model {
	t.Helper()
	cmd := m.fetchProviderTracks(playlistID)
	if cmd == nil {
		t.Fatal("fetchProviderTracks returned nil command")
	}
	updated, next := m.Update(cmd())
	m = updated.(Model)
	for next != nil {
		updated, next = m.Update(next())
		m = updated.(Model)
	}
	return m
}

func TestLikeTrackFromQueueUsesOwningProvider(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", likeResult: true}
	m := newSpotifyTestModel(fake)
	m.playlist.Add(spotifyTracks(2)...)
	m.plCursor = 1

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "*"})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("* returned nil command; want like toggle")
	}
	if _, ok := cmd().(trackLikeToggledMsg); !ok {
		t.Fatal("like command produced wrong message type")
	}
	msg := trackLikeToggledMsg{liked: true, gen: m.requests.like}
	updated, _ = m.Update(msg)
	m = updated.(Model)

	if len(fake.liked) != 1 || fake.liked[0] != "spotify:track:1" {
		t.Fatalf("liked = %v, want [spotify:track:1]", fake.liked)
	}
	if m.status.text != "Added to liked tracks" {
		t.Fatalf("status = %q, want liked toast", m.status.text)
	}

	// Unlike flips the toast.
	updated, _ = m.Update(trackLikeToggledMsg{liked: false, gen: m.requests.like})
	m = updated.(Model)
	if m.status.text != "Removed from liked tracks" {
		t.Fatalf("status = %q, want unliked toast", m.status.text)
	}
}

func TestLikeTrackFromSearchResults(t *testing.T) {
	sp := &trackSearchProvider{
		commandsTestProvider: commandsTestProvider{name: "Spotify"},
		tracks:               spotifyTracks(1),
	}
	m := newSpotifyTestModel(&fakeSpotifyProvider{name: "Spotify"})
	m.provider = sp
	m.providers = []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: sp}}

	m.openProviderSearchWith(sp)
	m.spotSearch.query = "boot"
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter returned nil search command")
	}
	resultsMsg, ok := cmd().(spotSearchResultsMsg)
	if !ok {
		t.Fatalf("search produced %T", cmd())
	}
	updated, _ = m.Update(resultsMsg)
	m = updated.(Model)
	if m.spotSearch.screen != spotSearchResults || m.spotSearch.multi {
		t.Fatalf("screen = %d multi = %v; want plain results", m.spotSearch.screen, m.spotSearch.multi)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Text: "S"})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("S returned nil command on search results")
	}
	if _, ok := cmd().(trackLikeToggledMsg); !ok {
		t.Fatal("S produced wrong message type")
	}
	updated, _ = m.Update(trackLikeToggledMsg{liked: true, gen: m.requests.like})
	m = updated.(Model)
	if len(sp.liked) != 1 || sp.liked[0] != "spotify:track:0" {
		t.Fatalf("liked = %v", sp.liked)
	}
	if !m.spotSearch.visible {
		t.Fatal("search overlay should stay open after like")
	}
}

func TestRemoteXRemoveFromProviderPlaylist(t *testing.T) {
	tests := []struct {
		name        string
		shuffle     bool
		mirrorLen   int // providerQueueLen override; 0 keeps the loaded length
		cursor      int
		wantRemoved string // expected fake.removedFrom entry
		wantToast   string // expected refusal toast substring
		wantLen     int    // queue length after handling the completion msg
	}{
		{name: "removes by position", cursor: 1, wantRemoved: "pl1:1", wantLen: 4},
		{name: "shuffled queue refuses", shuffle: true, cursor: 1, wantToast: "Unshuffle", wantLen: 5},
		{name: "row beyond loaded mirror refuses", mirrorLen: 2, cursor: 3, wantToast: "not part", wantLen: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(5)}
			m := newSpotifyTestModel(fake)
			m = loadFakePlaylist(t, m, fake, "pl1")
			m.activeProviderPlaylistID = "pl1"
			m.providerQueueLen = tt.mirrorLen
			if tt.mirrorLen == 0 {
				m.providerQueueLen = m.playlist.Len()
			}
			if tt.shuffle {
				m.playlist.ToggleShuffle()
			}
			m.plCursor = tt.cursor

			updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
			m = updated.(Model)
			if tt.wantToast != "" {
				if cmd != nil {
					t.Fatalf("x returned command %T; want refusal", cmd())
				}
				if !strings.Contains(m.status.text, tt.wantToast) {
					t.Fatalf("status = %q, want containing %q", m.status.text, tt.wantToast)
				}
				if len(fake.removedFrom) != 0 {
					t.Fatalf("removedFrom = %v; want no provider call", fake.removedFrom)
				}
				if m.playlist.Len() != tt.wantLen {
					t.Fatalf("queue len = %d, want %d", m.playlist.Len(), tt.wantLen)
				}
				return
			}
			msg, ok := cmd().(remoteTrackRemovedMsg)
			if !ok {
				t.Fatalf("x produced %T; want remoteTrackRemovedMsg", cmd())
			}
			updated, _ = m.Update(msg)
			m = updated.(Model)
			if len(fake.removedFrom) != 1 || fake.removedFrom[0] != tt.wantRemoved {
				t.Fatalf("removedFrom = %v, want [%s]", fake.removedFrom, tt.wantRemoved)
			}
			if m.playlist.Len() != tt.wantLen {
				t.Fatalf("queue len = %d, want %d", m.playlist.Len(), tt.wantLen)
			}
			if m.providerQueueLen != tt.wantLen {
				t.Fatalf("providerQueueLen = %d, want %d", m.providerQueueLen, tt.wantLen)
			}
		})
	}
}

func TestRemoteXFallsBackToQueueRemovalForForeignTracks(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(2)}
	m := newSpotifyTestModel(fake)
	m.playlist.Add(playlist.Track{Path: "/home/me/a.mp3", Title: "Local"})
	m.activeProviderPlaylistID = "pl1"
	m.providerQueueLen = 2 // stale mirror
	m.plCursor = 0

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("x returned %T; want local fallback", cmd())
	}
	if len(fake.removedFrom) != 0 {
		t.Fatalf("removedFrom = %v; want no remote call for local track", fake.removedFrom)
	}
	if m.playlist.Len() != 0 {
		t.Fatalf("queue len = %d, want local removal", m.playlist.Len())
	}
}

func TestRemoteXAfterNavQueueReplacement(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(5)}
	m := newSpotifyTestModel(fake)
	m = loadFakePlaylist(t, m, fake, "pl1")
	m.activeProviderPlaylistID = "pl1"
	if m.providerQueueLen != 5 {
		t.Fatalf("providerQueueLen = %d, want 5 after load", m.providerQueueLen)
	}

	// Replace the queue from the nav browser (R on a track list).
	m.navBrowser.tracks = spotifyTracks(3)
	m.replacePlaylistFromNav()
	if m.activeProviderPlaylistID != "" || m.providerQueueLen != 0 || m.providerQueueLastPath != "" {
		t.Fatalf("mirror = (%q, %d, %q); want reset", m.activeProviderPlaylistID, m.providerQueueLen, m.providerQueueLastPath)
	}

	m.plCursor = 0
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("x returned %T; want local fallback after queue replacement", cmd())
	}
	if len(fake.removedFrom) != 0 {
		t.Fatalf("removedFrom = %v; want no remote remove", fake.removedFrom)
	}
	if m.playlist.Len() != 2 {
		t.Fatalf("queue len = %d, want local removal down to 2", m.playlist.Len())
	}
}

func TestRemoteXTailMismatchFallsBackToLocal(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(3)}
	m := newSpotifyTestModel(fake)
	m = loadFakePlaylist(t, m, fake, "pl1")
	m.activeProviderPlaylistID = "pl1"

	// Simulate a queue replacement that kept the same length but different
	// tracks (the mirror counters alone cannot detect it; the tail check must).
	m.playlist.Replace([]playlist.Track{
		{Path: "spotify:track:9", Title: "Other"},
		{Path: "spotify:track:8", Title: "Other"},
		{Path: "spotify:track:7", Title: "Other"},
	})
	m.plCursor = 0

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("x returned %T; want local fallback on tail mismatch", cmd())
	}
	if len(fake.removedFrom) != 0 {
		t.Fatalf("removedFrom = %v; want no remote remove", fake.removedFrom)
	}
	if m.playlist.Len() != 2 {
		t.Fatalf("queue len = %d, want local removal", m.playlist.Len())
	}
}

func TestOverlappingRemoteRemovesBothLand(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(5)}
	m := newSpotifyTestModel(fake)
	m = loadFakePlaylist(t, m, fake, "pl1")
	m.activeProviderPlaylistID = "pl1"

	// Two removes issued before either completion arrives; the second bumps
	// the mutation generation, so the first completion is generation-stale.
	m.plCursor = 0
	updated, cmd1 := m.Update(tea.KeyPressMsg{Text: "x"})
	m = updated.(Model)
	m.plCursor = 1
	updated, cmd2 := m.Update(tea.KeyPressMsg{Text: "x"})
	m = updated.(Model)

	msg2 := cmd2().(remoteTrackRemovedMsg)
	msg1 := cmd1().(remoteTrackRemovedMsg)

	// Completions arrive out of order: the newer one applies first.
	updated, _ = m.Update(msg2)
	m = updated.(Model)
	updated, _ = m.Update(msg1)
	m = updated.(Model)

	if m.playlist.Len() != 3 || m.providerQueueLen != 3 {
		t.Fatalf("queue len = %d mirror = %d; want both removals applied (3/3)", m.playlist.Len(), m.providerQueueLen)
	}
	if paths := trackPathsOf(m.playlist.Tracks()); paths[0] != "spotify:track:2" {
		t.Fatalf("first remaining track = %v, want spotify:track:2", paths[0])
	}
}

func trackPathsOf(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, t := range tracks {
		paths[i] = t.Path
	}
	return paths
}

func TestProviderUnfollowConfirmOwnedVsFollowed(t *testing.T) {
	tests := []struct {
		name       string
		cursor     int
		wantLabel  string
		wantStatus string
	}{
		{name: "owned deletes", cursor: 0, wantLabel: "Delete", wantStatus: `Deleted "Mine"`},
		{name: "followed unfollows", cursor: 1, wantLabel: "Unfollow", wantStatus: `Unfollowed "Theirs"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSpotifyProvider{name: "Spotify", lists: []playlist.PlaylistInfo{
				{ID: "pl1", Name: "Mine", Owned: true},
				{ID: "pl2", Name: "Theirs"},
			}}
			m := newSpotifyTestModel(fake)
			m.focus = focusProvider
			m.providerLists = fake.lists
			m.provCursor = tt.cursor
			m.plVisible = 12

			m.handleKey(tea.KeyPressMsg{Text: "D"})
			if !m.provConfirm.active {
				t.Fatal("D did not arm confirmation")
			}
			view := m.renderProviderList()
			if !strings.Contains(view, tt.wantLabel) {
				t.Fatalf("confirm view %q missing %q label", view, tt.wantLabel)
			}

			updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = updated.(Model)
			msg, ok := cmd().(playlistUnfollowedMsg)
			if !ok {
				t.Fatalf("enter produced %T; want playlistUnfollowedMsg", cmd())
			}
			updated, refresh := m.Update(msg)
			m = updated.(Model)
			// The list refresh is async now; apply its completion too.
			if refresh != nil {
				updated, _ = m.Update(refresh())
				m = updated.(Model)
			}

			wantID := "pl1"
			if tt.cursor == 1 {
				wantID = "pl2"
			}
			if len(fake.unfollowedPlaylists) != 1 || fake.unfollowedPlaylists[0] != wantID {
				t.Fatalf("unfollowedPlaylists = %v, want [%s]", fake.unfollowedPlaylists, wantID)
			}
			if !strings.Contains(m.status.text, tt.wantStatus) {
				t.Fatalf("status = %q, want %q", m.status.text, tt.wantStatus)
			}
			if len(m.providerLists) != 1 {
				t.Fatalf("providerLists len = %d, want refreshed list of 1", len(m.providerLists))
			}
			if m.provCursor >= len(m.providerLists) {
				t.Fatalf("provCursor = %d out of range for %d lists", m.provCursor, len(m.providerLists))
			}
		})
	}
}

func TestProviderUnfollowConfirmCancel(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", lists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Mine"}}}
	m := newSpotifyTestModel(fake)
	m.focus = focusProvider
	m.providerLists = fake.lists

	m.handleKey(tea.KeyPressMsg{Text: "D"})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if cmd != nil || m.provConfirm.active {
		t.Fatal("Esc should cancel the confirmation without a provider call")
	}
	if len(fake.unfollowedPlaylists) != 0 {
		t.Fatalf("unfollowedPlaylists = %v; want none", fake.unfollowedPlaylists)
	}
}

func TestProviderUnfollowSkipsSyntheticRows(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", lists: []playlist.PlaylistInfo{
		{ID: "YOUR MUSIC", Name: "Your Music"},
		{ID: "pl1", Name: "Mine", Owned: true},
	}}
	m := newSpotifyTestModel(fake)
	m.focus = focusProvider
	m.providerLists = fake.lists
	m.provCursor = 0 // synthetic Library row

	m.handleKey(tea.KeyPressMsg{Text: "D"})
	if m.provConfirm.active {
		t.Fatal("D armed confirmation on a synthetic Library row")
	}

	// The registry gates D off synthetic rows and keeps it on real ones.
	for _, c := range commandRegistry {
		if c.Mode != commandModeProvider || c.Enabled == nil || len(c.Keys) != 1 || c.Keys[0] != "D" {
			continue
		}
		if c.Enabled(m) {
			t.Fatal("D should be disabled on a synthetic Library row")
		}
		m.provCursor = 1
		if !c.Enabled(m) {
			t.Fatal("D should stay enabled on a real playlist row")
		}
	}
}

func TestProviderRenameFlow(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", lists: []playlist.PlaylistInfo{
		{ID: "pl1", Name: "Mine", Owned: true},
		{ID: "pl2", Name: "Theirs"},
	}}
	m := newSpotifyTestModel(fake)
	m.focus = focusProvider
	m.providerLists = fake.lists

	// Non-owned rows refuse the rename.
	m.provCursor = 1
	m.handleKey(tea.KeyPressMsg{Text: "r"})
	if m.provRename.active {
		t.Fatal("rename armed for non-owned playlist")
	}
	if !strings.Contains(m.status.text, "own") {
		t.Fatalf("status = %q, want ownership hint", m.status.text)
	}

	m.provCursor = 0
	m.handleKey(tea.KeyPressMsg{Text: "r"})
	if !m.provRename.active || m.provRename.oldName != "Mine" {
		t.Fatalf("rename state = %+v; want armed with prefilled name", m.provRename)
	}
	// Editing is captured by the rename input, not the pane navigation.
	m.handleKey(tea.KeyPressMsg{Text: "2"})
	if m.provRename.name != "Mine2" {
		t.Fatalf("rename name = %q, want Mine2", m.provRename.name)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg, ok := cmd().(playlistRenamedMsg)
	if !ok {
		t.Fatalf("enter produced %T; want playlistRenamedMsg", cmd())
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.renames) != 1 || fake.renames[0] != [2]string{"pl1", "Mine2"} {
		t.Fatalf("renames = %v", fake.renames)
	}
	if !strings.Contains(m.status.text, "Mine2") {
		t.Fatalf("status = %q, want rename toast", m.status.text)
	}
	if m.provRename.active {
		t.Fatal("rename input should close after commit")
	}
}

func TestFollowArtistFromNavBrowser(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", artistsList: []provider.ArtistInfo{
		{ID: "ar1", Name: "Fleetwood Mac", AlbumCount: 0},
	}}
	m := newSpotifyTestModel(fake)
	m.navBrowser = navBrowserState{
		prov:    fake,
		visible: true,
		mode:    navBrowseModeByArtist,
		screen:  navBrowseScreenList,
		artists: fake.artistsList,
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	msg, ok := cmd().(artistFollowedMsg)
	if !ok {
		t.Fatalf("f produced %T; want artistFollowedMsg", cmd())
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.followedArtists) != 1 || fake.followedArtists[0] != "ar1" {
		t.Fatalf("followedArtists = %v", fake.followedArtists)
	}
	if m.status.text != "Following Fleetwood Mac" {
		t.Fatalf("status = %q", m.status.text)
	}

	// Second press unfollows (session-local toggle state).
	updated, cmd = m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	msg, ok = cmd().(artistFollowedMsg)
	if !ok || msg.follow {
		t.Fatalf("second f produced %+v; want unfollow", msg)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.unfollowedArtists) != 1 || fake.unfollowedArtists[0] != "ar1" {
		t.Fatalf("unfollowedArtists = %v", fake.unfollowedArtists)
	}
}

func TestRegistryCoversNewWriteKeys(t *testing.T) {
	tests := []struct {
		mode commandMode
		key  string
	}{
		{commandModeMain, "*"},
		{commandModeProvider, "D"},
		{commandModeProvider, "r"},
		{commandModeNavBrowser, "*"},
		{commandModeNavBrowser, "f"},
		{commandModeSpotSearch, "S"},
		{commandModeSpotSearch, "f"},
		{commandModeSpotSearch, "tab"},
		{commandModeSpotSearch, "shift+tab"},
		{commandModeSpotSearch, "left"},
		{commandModeSpotSearch, "right"},
	}
	for _, tt := range tests {
		found := false
		for _, c := range commandRegistry {
			if c.Mode&tt.mode == 0 {
				continue
			}
			for _, k := range c.Keys {
				if k == tt.key {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("commandRegistry has no %q entry for mode %d", tt.key, tt.mode)
		}
	}
}
