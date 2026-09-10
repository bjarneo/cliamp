package model

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func TestFetchProviderTracksPagesLargePlaylists(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(5)}
	fake.pageLimit = 2
	m := newSpotifyTestModel(fake)

	cmd := m.fetchProviderTracks("pl1")
	if cmd == nil {
		t.Fatal("fetchProviderTracks returned nil")
	}
	first := cmd().(tracksLoadedMsg)
	if !first.paged || first.total != 5 || len(first.tracks) != 2 {
		t.Fatalf("first page = %+v; want paged 2 of 5", first)
	}

	updated, next := m.Update(first)
	m = updated.(Model)
	if m.playlist.Len() != 2 || m.providerQueueLen != 2 {
		t.Fatalf("queue len = %d mirror = %d; want 2/2", m.playlist.Len(), m.providerQueueLen)
	}
	if !m.trackPaging.active || !m.trackPaging.loading {
		t.Fatalf("trackPaging = %+v; want active and loading", m.trackPaging)
	}
	if next == nil {
		t.Fatal("no follow-up page command")
	}

	// Pages keep flowing until the total is reached.
	msg := next().(tracksAppendedMsg)
	if msg.offset != 2 || len(msg.tracks) != 2 {
		t.Fatalf("second page = %+v", msg)
	}
	updated, next = m.Update(msg)
	m = updated.(Model)
	if m.playlist.Len() != 4 {
		t.Fatalf("queue len = %d, want 4", m.playlist.Len())
	}

	msg = next().(tracksAppendedMsg)
	updated, next = m.Update(msg)
	m = updated.(Model)
	if m.playlist.Len() != 5 || next != nil || m.trackPaging.active {
		t.Fatalf("final state: len = %d next = %v paging = %+v", m.playlist.Len(), next != nil, m.trackPaging)
	}
	if got := strings.Join(fake.pageCalls, " "); got != "pl1:0:200 pl1:2:200 pl1:4:200" {
		t.Fatalf("pageCalls = %q", got)
	}
}

func TestTracksAppendMutationGuard(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(m *Model)
		wantLen    int  // queue length after the append message is applied
		wantActive bool // whether incremental paging should still be armed
	}{
		// One page of 2 appended to the initial 2; one page still to go.
		{name: "intact queue appends", wantLen: 4, wantActive: true},
		{
			// The stale append is dropped; the newer load's own
			// fetchProviderTracks resets the paging state.
			name:       "newer load discards stale append",
			mutate:     func(m *Model) { m.requests.tracks++ },
			wantLen:    2,
			wantActive: true,
		},
		{
			name:       "user removal stops paging",
			mutate:     func(m *Model) { m.playlist.Remove(0) },
			wantLen:    1,
			wantActive: false,
		},
		{
			name:       "queue replacement stops paging",
			mutate:     func(m *Model) { m.playlist.Replace([]playlist.Track{{Path: "/a.mp3"}}) },
			wantLen:    1,
			wantActive: false,
		},
		{
			name: "manual additions stop paging",
			mutate: func(m *Model) {
				m.playlist.Add(playlist.Track{Path: "/b.mp3"})
			},
			wantLen:    3,
			wantActive: false,
		},
		{
			name: "tail-identity mismatch stops paging",
			mutate: func(m *Model) {
				tracks := m.playlist.Tracks()
				tracks[1].Path = "spotify:track:hijack"
				m.playlist.Replace(tracks)
			},
			wantLen:    2,
			wantActive: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(5)}
			fake.pageLimit = 2
			m := newSpotifyTestModel(fake)

			cmd := m.fetchProviderTracks("pl1")
			first := cmd().(tracksLoadedMsg)
			updated, next := m.Update(first)
			m = updated.(Model)
			if next == nil {
				t.Fatal("expected a follow-up page")
			}
			msg := next().(tracksAppendedMsg)

			if tt.mutate != nil {
				tt.mutate(&m)
			}
			updated, _ = m.Update(msg)
			m = updated.(Model)
			if m.playlist.Len() != tt.wantLen {
				t.Fatalf("queue len = %d, want %d", m.playlist.Len(), tt.wantLen)
			}
			if m.trackPaging.active != tt.wantActive {
				t.Fatalf("paging active = %v, want %v (%+v)", m.trackPaging.active, tt.wantActive, m.trackPaging)
			}
		})
	}
}

func TestFetchProviderTracksNonPagerUnchanged(t *testing.T) {
	local := commandsTestProvider{name: "Local"}
	player := &playbackFakeEngine{}
	m := Model{
		player:        player,
		playlist:      playlist.New(),
		localProvider: local,
		provider:      local,
		vis:           ui.NewVisualizer(float64(player.SampleRate())),
	}
	cmd := m.fetchProviderTracks("mix")
	msg := cmd().(tracksLoadedMsg)
	if msg.paged || msg.total != 0 {
		t.Fatalf("non-pager load = %+v; want paged=false", msg)
	}
	updated, next := m.Update(msg)
	m = updated.(Model)
	if next != nil || m.trackPaging.active {
		t.Fatalf("non-pager load started paging: next=%v paging=%+v", next != nil, m.trackPaging)
	}
}

func TestTrackPagingLoadingIndicator(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", allTracks: spotifyTracks(3)}
	fake.pageLimit = 1
	m := newSpotifyTestModel(fake)
	cmd := m.fetchProviderTracks("pl1")
	first := cmd().(tracksLoadedMsg)
	updated, _ := m.Update(first)
	m = updated.(Model)

	m.plVisible = 12
	m.focus = focusPlaylist
	view := m.renderPlaylist()
	if !strings.Contains(view, "Loading more tracks") {
		t.Fatalf("playlist view missing loading indicator:\n%s", view)
	}
}

func TestNavArtistLabelHidesZeroAlbumCount(t *testing.T) {
	tests := []struct {
		artist provider.ArtistInfo
		want   string
	}{
		{provider.ArtistInfo{Name: "Fleetwood Mac", AlbumCount: 0}, "Fleetwood Mac"},
		{provider.ArtistInfo{Name: "ABBA", AlbumCount: 9}, "ABBA (9 albums)"},
		{provider.ArtistInfo{Name: "Nobody"}, "Nobody"},
	}
	for _, tt := range tests {
		if got := navArtistLabel(tt.artist); got != tt.want {
			t.Errorf("navArtistLabel(%+v) = %q, want %q", tt.artist, got, tt.want)
		}
	}
}
