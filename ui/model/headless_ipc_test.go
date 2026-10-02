package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// The tests in this file drive a headless Model with no WindowSizeMsg. They
// replace the tests of the headless daemon that the Model took over.

// libraryTestProvider serves one playlist, a search, artists and albums.
type libraryTestProvider struct{ commandsTestProvider }

func (libraryTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "mix", Name: "Mix", TrackCount: 2}}, nil
}
func (libraryTestProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/one.flac", Title: "One"}, {Path: "/two.flac", Title: "Two"}}, nil
}
func (libraryTestProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/result.flac", Title: "Result"}}, nil
}
func (libraryTestProvider) Artists() ([]provider.ArtistInfo, error) {
	return []provider.ArtistInfo{{ID: "artist", Name: "Artist", AlbumCount: 1}}, nil
}
func (libraryTestProvider) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album", Artist: "Artist", TrackCount: 2}}, nil
}
func (libraryTestProvider) AlbumList(string, int, int) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album"}}, nil
}
func (libraryTestProvider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "name", Label: "By name"}}
}
func (libraryTestProvider) DefaultAlbumSort() string { return "name" }
func (libraryTestProvider) AlbumTracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/album.flac", Title: "Album track"}}, nil
}

// stationCatalogProvider is a station catalog with a search and favorites.
type stationCatalogProvider struct {
	commandsTestProvider
	searching bool
	favorite  string
}

func (p *stationCatalogProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "c:station", Name: "Station"}}, nil
}
func (p *stationCatalogProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://radio.example/stream", Title: "Station", Stream: true}}, nil
}
func (p *stationCatalogProvider) SearchCatalog(string) (int, error) {
	p.searching = true
	return 1, nil
}
func (*stationCatalogProvider) LoadCatalogPage(int, int) (int, error) { return 1, nil }
func (p *stationCatalogProvider) ClearSearch()                        { p.searching = false }
func (p *stationCatalogProvider) IsSearching() bool                   { return p.searching }
func (*stationCatalogProvider) IDPrefix(string) string                { return "c" }
func (*stationCatalogProvider) IsFavoritableID(id string) bool        { return id == "c:station" }
func (p *stationCatalogProvider) ToggleFavorite(id string) (bool, string, error) {
	p.favorite = id
	return true, "Station", nil
}

func TestHeadlessStateSnapshot(t *testing.T) {
	engine := &headlessEngine{}
	engine.playing, engine.seekable = true, true
	engine.position, engine.duration = 12*time.Second, 3*time.Minute
	m := newHeadlessModel(t, engine, nil,
		playlist.Track{Path: "https://example.com/one", Title: "One", Stream: true},
		playlist.Track{Path: "https://example.com/two", Title: "Two", Stream: true},
	)
	m.playlist.Queue(1)
	m.SetIPCBroker(ipc.NewBroker())
	m.publishIPCRuntimeState()

	reply := make(chan V2RequestResult, 1)
	updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "state.get"}, Reply: reply})
	m = updated.(Model)
	result := <-reply
	if result.Error != nil || result.Result.Snapshot == nil {
		t.Fatalf("state.get = %+v", result)
	}
	snapshot := result.Result.Snapshot
	if snapshot.Revision != m.ipcRuntime.revision || snapshot.Revision == 0 || snapshot.PlaylistRevision != m.playlist.Revision() {
		t.Fatalf("revisions = %d/%d, want %d/%d", snapshot.Revision, snapshot.PlaylistRevision, m.ipcRuntime.revision, m.playlist.Revision())
	}
	if snapshot.State != "playing" || !snapshot.Seekable || snapshot.Position != 12 || snapshot.Duration != 180 || snapshot.Total != 2 || snapshot.PlayNextTotal != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Track == nil || snapshot.LogicalTrack == nil || snapshot.Track.Path != "https://example.com/one" || snapshot.LogicalTrack.Path != "https://example.com/one" {
		t.Fatalf("tracks = %+v / %+v", snapshot.Track, snapshot.LogicalTrack)
	}
}

// A job reports its result and the snapshot of the state that it committed.
func TestHeadlessVolumeJobCarriesSnapshot(t *testing.T) {
	engine := &headlessEngine{playbackFakeEngine: playbackFakeEngine{volume: -6}}
	m := newHeadlessModel(t, engine, nil, playlist.Track{Path: "/music/one.flac", Title: "One"})

	msg := v2Request(t, "volume", ipc.Request{Value: -18})
	m.Update(msg)
	job, ok := msg.Jobs.Get(msg.JobID)
	if !ok || job.State != ipc.JobSucceeded {
		t.Fatalf("job = %+v, found %v", job, ok)
	}
	if engine.volume != -18 || job.Snapshot == nil || job.Snapshot.Volume != -18 {
		t.Fatalf("volume = %v, job snapshot = %+v; want -18", engine.volume, job.Snapshot)
	}
}

func TestHeadlessQueueAndPlayNextAreSeparate(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil)

	if response := runV2(t, &m, "queue", ipc.Request{Path: "https://example.com/queued"}); !response.OK || m.playlist.Len() != 1 || m.playlist.QueueLen() != 0 {
		t.Fatalf("queue = %+v, playlist %d, play-next %d", response, m.playlist.Len(), m.playlist.QueueLen())
	}
	if response := runV2(t, &m, "queue.enqueue", ipc.Request{Index: 0}); !response.OK || m.playlist.Len() != 1 || m.playlist.QueueLen() != 1 {
		t.Fatalf("queue.enqueue = %+v, playlist %d, play-next %d", response, m.playlist.Len(), m.playlist.QueueLen())
	}
	playNext := runV2(t, &m, "playnext.list", ipc.Request{})
	if len(playNext.Tracks) != 1 || playNext.Total != 1 || playNext.Tracks[0].QueuePosition != 1 {
		t.Fatalf("playnext.list = %+v", playNext)
	}
	live := runV2(t, &m, "queue.list", ipc.Request{})
	if len(live.Tracks) != 1 || live.Total != 1 || live.Tracks[0].Path != playNext.Tracks[0].Path {
		t.Fatalf("queue.list = %+v", live)
	}
}

// The scripted playlist recipe of docs/headless.md: queue appends to an idle
// daemon and starts nothing. play then starts the first queued track.
func TestHeadlessScriptedQueueStartsWithPlay(t *testing.T) {
	engine := &headlessEngine{}
	m := newHeadlessModel(t, engine, nil)
	for _, path := range []string{"/music/one.flac", "/music/two.flac"} {
		if response := runV2(t, &m, "queue", ipc.Request{Path: path}); !response.OK {
			t.Fatalf("queue %s = %+v", path, response)
		}
	}
	if len(engine.playCalls) != 0 {
		t.Fatalf("queue started %v, want nothing before play", engine.playCalls)
	}
	if response := runV2(t, &m, "play", ipc.Request{}); !response.OK {
		t.Fatalf("play = %+v", response)
	}
	if len(engine.playCalls) != 1 || engine.playCalls[0] != "/music/one.flac" {
		t.Fatalf("play started %v, want /music/one.flac", engine.playCalls)
	}
}

func TestHeadlessQueueListIncludesMetadata(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil,
		playlist.Track{Path: "/one.flac", Title: "One", Album: "Album", DurationSecs: 60},
		playlist.Track{Path: "/two.flac", Title: "Two"},
	)
	m.playlist.Queue(1)

	response := runV2(t, &m, "queue.list", ipc.Request{})
	if len(response.Tracks) != 2 || response.Tracks[0].Album != "Album" || response.Tracks[0].DurationSecs != 60 || response.Tracks[1].QueuePosition != 1 {
		t.Fatalf("queue.list = %+v", response.Tracks)
	}
}

// Media-control messages apply in the order that they arrive.
func TestHeadlessMediaControlsApplyInOrder(t *testing.T) {
	engine := &headlessEngine{playbackFakeEngine: playbackFakeEngine{volume: -6}}
	m := newHeadlessModel(t, engine, nil)
	for _, want := range []float64{-10, -20} {
		updated, _ := m.Update(playback.SetVolumeMsg{VolumeDB: want})
		m = updated.(Model)
		if engine.volume != want {
			t.Fatalf("volume = %v, want %v", engine.volume, want)
		}
	}
}

// play on a paused live station connects again to the station that plays. It
// does not resume the stale buffer, and it does not advance.
func TestHeadlessPlayRestartsLiveStation(t *testing.T) {
	engine := &headlessEngine{}
	engine.playing, engine.paused, engine.live = true, true, true
	m := newHeadlessModel(t, engine, nil,
		playlist.Track{Path: "https://radio.example.com/one", Title: "One", Stream: true},
		playlist.Track{Path: "https://radio.example.com/two", Title: "Two", Stream: true},
	)

	if response := runV2(t, &m, "play", ipc.Request{}); !response.OK {
		t.Fatalf("play = %+v", response)
	}
	if got := m.playlist.Index(); got != 0 {
		t.Fatalf("playlist index = %d, want the current station 0", got)
	}
	if engine.stopCalls != 1 || !m.buffering || m.playingTrack.Path != "https://radio.example.com/one" {
		t.Fatalf("stops = %d, buffering %v, track %q; want a new connection to station one", engine.stopCalls, m.buffering, m.playingTrack.Path)
	}
}

// A drained live stream connects again in place. A yt-dlp recording with a
// stale live flag has a duration, so its drain advances.
func TestHeadlessDrainedLiveStream(t *testing.T) {
	ytdlLive := playlist.Track{Path: "https://music.youtube.com/watch?v=live1", Title: "Live", Stream: true, Realtime: true}
	ytdlNext := playlist.Track{Path: "https://music.youtube.com/watch?v=next1", Title: "Next", Stream: true, DurationSecs: 100}
	for _, tc := range []struct {
		name          string
		tracks        []playlist.Track
		runtimeLive   bool
		duration      time.Duration
		wantIndex     int
		wantReconnect bool
	}{
		{
			name: "radio station",
			tracks: []playlist.Track{
				{Path: "https://radio.example.com/one", Title: "One", Stream: true},
				{Path: "https://radio.example.com/two", Title: "Two", Stream: true},
			},
			runtimeLive:   true,
			wantReconnect: true,
		},
		{name: "yt-dlp stream that is still live", tracks: []playlist.Track{ytdlLive, ytdlNext}, wantReconnect: true},
		{name: "yt-dlp recording that ended", tracks: []playlist.Track{ytdlLive, ytdlNext}, duration: 90 * time.Minute, wantIndex: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{}
			engine.playing, engine.drained, engine.live, engine.duration = true, true, tc.runtimeLive, tc.duration
			m := newHeadlessModel(t, engine, nil, tc.tracks...)

			updated, _ := m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			if got := m.playlist.Index(); got != tc.wantIndex {
				t.Fatalf("playlist index = %d, want %d", got, tc.wantIndex)
			}
			if got := !m.reconnect.at.IsZero(); got != tc.wantReconnect {
				t.Fatalf("reconnect scheduled = %v, want %v", got, tc.wantReconnect)
			}
		})
	}
}

// The snapshot splits ICY metadata into artist and title and names the
// station, the same way the TUI shows it.
func TestHeadlessStreamTitleFields(t *testing.T) {
	tests := []struct {
		name        string
		streamTitle string
		track       playlist.Track
		wantTitle   string
		wantArtist  string
		wantStation string
		wantStream  string
	}{
		{
			name:        "artist and title split on separator",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Path: "https://example.com/ncs", Title: "NCS Trap Stream", Stream: true},
			wantTitle:   "Awake",
			wantArtist:  "Tycho",
			wantStation: "NCS Trap Stream",
			wantStream:  "Tycho - Awake",
		},
		{
			name:        "empty title after the separator keeps the station",
			streamTitle: "Tycho - ",
			track:       playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
			wantStream:  "Tycho - ",
		},
		{
			name:        "title-only metadata becomes the title",
			streamTitle: "Morning Session",
			track:       playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle:   "Morning Session",
			wantStation: "Lofi Stream",
			wantStream:  "Morning Session",
		},
		{
			name:      "no metadata leaves the entry untouched",
			track:     playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle: "Lofi Stream",
		},
		{
			name:        "non-stream track is never rewritten",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Path: "/music/alien-boy.flac", Title: "Alien Boy", Artist: "Oliver Tree"},
			wantTitle:   "Alien Boy",
			wantArtist:  "Oliver Tree",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{playbackFakeEngine: playbackFakeEngine{streamTitle: tc.streamTitle}}
			engine.playing = true
			m := newHeadlessModel(t, engine, nil, tc.track)

			// The tick reads the ICY title from the player.
			updated, _ := m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			info := m.runtimeSnapshot().Track
			if info == nil {
				t.Fatal("snapshot has no track")
			}
			for _, f := range []struct{ field, got, want string }{
				{"Title", info.Title, tc.wantTitle},
				{"Artist", info.Artist, tc.wantArtist},
				{"Station", info.Station, tc.wantStation},
				{"StreamTitle", info.StreamTitle, tc.wantStream},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}

func TestHeadlessLibraryRequests(t *testing.T) {
	providers := []provider.Entry{{Key: "test", Name: "Test", Provider: libraryTestProvider{commandsTestProvider{name: "Test"}}}}
	for _, tc := range []struct {
		op     string
		params ipc.Request
		ok     func(ipc.Response) bool
	}{
		{"provider.list", ipc.Request{}, func(r ipc.Response) bool {
			return len(r.Providers) == 1 && r.Providers[0].Searchable && r.Providers[0].BrowseArtists && r.Providers[0].BrowseAlbums
		}},
		{"provider.playlists", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Playlists) == 1 && r.Playlists[0].ID == "mix" && r.Playlists[0].Provider == "test"
		}},
		{"provider.search", ipc.Request{Provider: "test", Query: "result"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 1 && r.Tracks[0].Title == "Result"
		}},
		{"provider.artists", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Artists) == 1 && r.Artists[0].Name == "Artist"
		}},
		{"provider.artist_albums", ipc.Request{Provider: "test", Artist: "artist"}, func(r ipc.Response) bool {
			return len(r.Albums) == 1 && r.Albums[0].Artist == "Artist"
		}},
		{"provider.albums", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Albums) == 1 && len(r.Sorts) == 1 && r.Albums[0].Name == "Album"
		}},
		{"provider.album_tracks", ipc.Request{Provider: "test", Album: "album"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 1 && r.Tracks[0].Title == "Album track"
		}},
		{"provider.tracks", ipc.Request{Provider: "test", Playlist: "mix"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 2 && r.Total == 2 && r.Playlist == "mix"
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			m := newHeadlessModel(t, &headlessEngine{}, providers)
			if response := runV2(t, &m, tc.op, tc.params); !response.OK || !tc.ok(response) {
				t.Fatalf("%s = %+v", tc.op, response)
			}
		})
	}
}

// A job.cancel request or an IPC server shutdown cancels the request context,
// which stops the resolve. A live request resolves the URL.
func TestIPCURLUsesRequestContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	local := filepath.Join(t.TempDir(), "one.mp3")
	if err := os.WriteFile(local, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		ctx        context.Context
		url        string
		wantErr    error
		wantTracks int
	}{
		{name: "canceled request", ctx: canceled, url: "http://127.0.0.1:1/stream", wantErr: context.Canceled},
		{name: "live request", ctx: context.Background(), url: local, wantTracks: 1},
		{name: "no context", url: local, wantTracks: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Model
			msg := m.handleIPCURL(ipcURLRequest{URL: tt.url, Context: tt.ctx, Reply: make(chan ipc.Response, 1)})()
			result, ok := msg.(ipcURLLoadResult)
			if !ok {
				t.Fatalf("message = %T, want ipcURLLoadResult", msg)
			}
			if !errors.Is(result.err, tt.wantErr) || len(result.tracks) != tt.wantTracks {
				t.Fatalf("result = %d tracks, error %v; want %d tracks, error %v", len(result.tracks), result.err, tt.wantTracks, tt.wantErr)
			}
		})
	}
}

// A job.cancel request or an IPC server shutdown cancels the request context,
// which stops the yt-dlp download of a save. A live request saves the track.
func TestIPCSaveUsesRequestContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	const saved = `echo '{"_filename":"/music/Artist - Song.m4a"}'`
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		script  string // body of the fake yt-dlp
		ctx     func(t *testing.T) context.Context
		wantErr string // "" when the save succeeds
	}{
		{name: "canceled request", script: saved, ctx: func(*testing.T) context.Context { return canceled }, wantErr: "context canceled"},
		{name: "cancel during the download", script: "exec sleep 30", ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx
		}, wantErr: "context canceled"},
		{name: "live request", script: saved, ctx: func(*testing.T) context.Context { return context.Background() }},
		{name: "no context", script: saved, ctx: func(*testing.T) context.Context { return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte("#!/bin/sh\n"+tt.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			tracks := playlist.New()
			tracks.Add(playlist.Track{Path: "https://www.youtube.com/watch?v=abc", Title: "Song"})
			m := Model{playlist: tracks}
			m.SetDownloadsDirectory(t.TempDir())
			reply := make(chan ipc.Response, 1)

			start := time.Now()
			cmd := m.handleIPCSave(ipcSaveRequest{Context: tt.ctx(t), Reply: reply})
			if cmd == nil {
				t.Fatal("handleIPCSave returned no command")
			}
			cmd()
			response := <-reply

			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("save returned after %v, want it to stop at the cancel", elapsed)
			}
			if tt.wantErr != "" {
				if response.OK || !strings.Contains(response.Error, tt.wantErr) {
					t.Fatalf("response = %+v, want an error with %q", response, tt.wantErr)
				}
				return
			}
			if !response.OK || response.Output != "/music/Artist - Song.m4a" {
				t.Fatalf("response = %+v, want the saved path", response)
			}
		})
	}
}

func TestIPCTrackInfoConversion(t *testing.T) {
	track := playlist.Track{Path: "https://example.com/stream", Title: "Stream", Artist: "Artist", Realtime: true, Restricted: true}
	info := ipcTrackInfo(track, 3, 2, false)
	converted := ipcTrackFromInfo(info)
	if info.Index != 3 || info.QueuePosition != 2 || !info.Restricted || converted.Path != track.Path || !converted.Stream || !converted.Realtime || !converted.Restricted {
		t.Fatalf("conversion lost metadata: info=%+v converted=%+v", info, converted)
	}
}

// countingCatalogProvider is a station catalog that counts the catalog
// searches and clears that change the search of its pane.
type countingCatalogProvider struct {
	stationCatalogProvider
	catalogSearches int
	clears          int
	searches        []string
}

func (p *countingCatalogProvider) SearchCatalog(query string) (int, error) {
	p.catalogSearches++
	return p.stationCatalogProvider.SearchCatalog(query)
}
func (p *countingCatalogProvider) ClearSearch() {
	p.clears++
	p.stationCatalogProvider.ClearSearch()
}

// statelessStationProvider is a station catalog that can also search with no
// pane state, as the radio provider does with SearchStations.
type statelessStationProvider struct{ *countingCatalogProvider }

func (p statelessStationProvider) SearchStations(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	p.searches = append(p.searches, fmt.Sprintf("stations %s/%d", query, limit))
	return []playlist.Track{{Path: "https://radio.example/jazz", Title: "Jazz", Stream: true}}, nil
}

// trackSearchCatalogProvider is a catalog that also searches tracks, as the
// podcast provider does with SearchTracks.
type trackSearchCatalogProvider struct{ *countingCatalogProvider }

func (p trackSearchCatalogProvider) SearchTracks(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	p.searches = append(p.searches, fmt.Sprintf("tracks %s/%d", query, limit))
	return []playlist.Track{{Path: "https://pod.example/jazz.mp3", Title: "Jazz"}}, nil
}

// An IPC search uses SearchTracks or SearchStations and leaves the pane search
// as it is: the provider keeps its search rows and the pane keeps its query. A
// provider with only a catalog search gets an error and no catalog search.
func TestIPCProviderSearchKeepsThePaneSearch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wrap     func(*countingCatalogProvider) playlist.Provider
		searches []string
		wantErr  string
	}{
		{name: "SearchTracks", wrap: func(c *countingCatalogProvider) playlist.Provider {
			return trackSearchCatalogProvider{c}
		}, searches: []string{"tracks jazz/5"}},
		{name: "SearchStations", wrap: func(c *countingCatalogProvider) playlist.Provider {
			return statelessStationProvider{c}
		}, searches: []string{"stations jazz/5"}},
		{name: "catalog search only", wrap: func(c *countingCatalogProvider) playlist.Provider {
			return c
		}, wantErr: "provider does not support search"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counts := &countingCatalogProvider{stationCatalogProvider: stationCatalogProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}}}
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "radio", Name: "Radio", Provider: tc.wrap(counts)}})
			counts.searching = true
			m.provSearch.query = "rock"
			m.provPane.lists = []playlist.PlaylistInfo{{ID: "s:0", Name: "Rock FM"}}

			list := runV2(t, &m, "provider.list", ipc.Request{})
			if len(list.Providers) != 1 || list.Providers[0].Searchable != (tc.wantErr == "") {
				t.Fatalf("provider.list = %+v, want searchable %v", list.Providers, tc.wantErr == "")
			}
			response := runV2(t, &m, "provider.search", ipc.Request{Provider: "radio", Query: "jazz", Limit: 5})
			if tc.wantErr != "" {
				if response.OK || !strings.Contains(response.Error, tc.wantErr) {
					t.Fatalf("provider.search = %+v, want error %q", response, tc.wantErr)
				}
			} else if !response.OK || len(response.Tracks) != 1 || response.Tracks[0].Title != "Jazz" {
				t.Fatalf("provider.search = %+v", response)
			}
			if !slices.Equal(counts.searches, tc.searches) {
				t.Fatalf("searches = %v, want %v", counts.searches, tc.searches)
			}
			if counts.catalogSearches != 0 || counts.clears != 0 || !counts.IsSearching() {
				t.Fatalf("pane search changed: %d catalog searches, %d clears, searching %v", counts.catalogSearches, counts.clears, counts.IsSearching())
			}
			if m.provSearch.query != "rock" || len(m.provPane.lists) != 1 || m.provPane.lists[0].ID != "s:0" {
				t.Fatalf("pane state = %q %+v, want the rock search rows", m.provSearch.query, m.provPane.lists)
			}
		})
	}
}

// The IPC search of the radio provider must take the SearchStations path. A
// changed method set would make every IPC radio search fail.
var _ stationSearcher = (*radio.Provider)(nil)

// An IPC search on the radio provider runs SearchStations under the request
// context and keeps the pane search rows. The context is cancelled, so no
// request leaves the process. A request that ignores the context fails with a
// dial error through the proxy.
func TestIPCRadioSearchKeepsThePaneSearch(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	prov := radio.New(radio.Options{Country: radio.CountryDeclined})
	prov.SetSearchResults([]radio.CatalogStation{{Name: "Rock FM", URL: "https://rock.example/stream"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ipcSearchProvider(ctx, prov, "jazz", 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("ipcSearchProvider error = %v, want the cancelled request context", err)
	}
	lists, err := prov.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	if !prov.IsSearching() || len(lists) != 1 || lists[0].Name != "Rock FM" {
		t.Fatalf("pane rows = %+v, searching %v; want the Rock FM search row", lists, prov.IsSearching())
	}
}

func TestHeadlessProviderFavoriteAndCatalog(t *testing.T) {
	prov := &stationCatalogProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "radio", Name: "Radio", Provider: prov}})

	if response := runV2(t, &m, "provider.favorite", ipc.Request{Provider: "radio", Playlist: "c:station"}); !response.OK || prov.favorite != "c:station" {
		t.Fatalf("provider.favorite = %+v, provider favorite %q", response, prov.favorite)
	}
	if response := runV2(t, &m, "provider.playlists", ipc.Request{Provider: "radio"}); len(response.Playlists) != 1 || !response.Playlists[0].Favoritable {
		t.Fatalf("provider.playlists = %+v", response)
	}
	if response := runV2(t, &m, "provider.catalog", ipc.Request{Provider: "radio", Limit: 50}); response.Total != 1 || len(response.Playlists) != 1 {
		t.Fatalf("provider.catalog = %+v", response)
	}
}

// favoriteRowsProvider lists rows that the provider marks, as the radio and
// podcast providers mark favorite stations and subscribed shows.
type favoriteRowsProvider struct{ commandsTestProvider }

func (favoriteRowsProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{
		{ID: "f:https://radio.example/a", Name: "Marked", Favorite: true},
		{ID: "f:https://radio.example/b", Name: "Prefix only"},
		{ID: "mix", Name: "Marked without a prefix", Favorite: true},
	}, nil
}

// provider.playlists reports the favorite mark that the provider sets on the
// row. It does not read the f: prefix of the ID.
func TestIPCPlaylistsReportTheProviderFavoriteMark(t *testing.T) {
	prov := favoriteRowsProvider{commandsTestProvider{name: "Rows"}}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "rows", Name: "Rows", Provider: prov}})
	response := runV2(t, &m, "provider.playlists", ipc.Request{Provider: "rows"})
	if !response.OK || len(response.Playlists) != 3 {
		t.Fatalf("provider.playlists = %+v", response)
	}
	for i, want := range []bool{true, false, true} {
		if got := response.Playlists[i].Favorite; got != want {
			t.Errorf("row %q favorite = %v, want %v", response.Playlists[i].Name, got, want)
		}
	}
}

func TestHeadlessPlaylistMutations(t *testing.T) {
	prov := &writableTestProvider{commandsTestProvider: commandsTestProvider{name: "Writable"}, removed: -1}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "local", Name: "Local", Provider: prov}})
	track := ipc.TrackInfo{Path: "/song.flac"}
	for _, tc := range []struct {
		op     string
		params ipc.Request
	}{
		{"playlist.create", ipc.Request{Provider: "local", Playlist: "Mix"}},
		{"playlist.rename", ipc.Request{Provider: "local", Playlist: "Mix", NewName: "New"}},
		{"playlist.delete", ipc.Request{Provider: "local", Playlist: "Old"}},
		{"playlist.remove", ipc.Request{Provider: "local", Playlist: "Mix", Index: 3}},
		{"playlist.add", ipc.Request{Provider: "local", Playlist: "Mix", Track: &track}},
	} {
		if response := runV2(t, &m, tc.op, tc.params); !response.OK {
			t.Fatalf("%s = %+v", tc.op, response)
		}
	}
	if prov.created != "Mix" || prov.renamed != "Mix:New" || prov.deleted != "Old" || prov.removed != 3 || !slices.Equal(prov.added, []string{"Mix:/song.flac"}) {
		t.Fatalf("writes were not forwarded: %+v", prov)
	}
}

// playlist.bookmark toggles the ♥ favorite and copies the change to the
// provider that owns the track. That provider ends with the last state.
func TestHeadlessBookmarkSyncsOwningProvider(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		wantCalls bool
	}{
		{name: "local file", path: "/song.flac"},
		{name: "provider track", path: "fake:track:1", wantCalls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
			fake := &fakeTrackFavoriter{commandsTestProvider: commandsTestProvider{name: "Fake"}, prefix: "fake:"}
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{
				{Key: "local", Name: "Local", Provider: store},
				{Key: "fake", Name: "Fake", Provider: fake},
			})
			m.localProvider, m.favStore = store, store.useFavorites(t)
			track := ipc.TrackInfo{Path: tc.path, Title: "Song"}

			for _, want := range []bool{true, false} {
				if response := runV2(t, &m, "playlist.bookmark", ipc.Request{Provider: "local", Playlist: "Mix", Track: &track}); !response.OK {
					t.Fatalf("playlist.bookmark = %+v", response)
				}
				if got := store.favs.IsFavorited(tc.path); got != want {
					t.Fatalf("favorited = %v, want %v", got, want)
				}
			}
			if !tc.wantCalls {
				time.Sleep(50 * time.Millisecond)
				if calls := fake.recorded(); len(calls) != 0 {
					t.Fatalf("provider calls = %v, want none", calls)
				}
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				calls := fake.recorded()
				if len(calls) > 0 && calls[len(calls)-1] == "remove "+tc.path {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("provider calls = %v, want the last call to remove %s", calls, tc.path)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestHeadlessClearsHistory(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil)
	store := history.New()
	if err := store.Record(playlist.Track{Path: "/song.flac"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if response := runV2(t, &m, "history", ipc.Request{}); len(response.History) != 1 {
		t.Fatalf("history = %+v, want 1 entry", response)
	}
	if response := runV2(t, &m, "history.clear", ipc.Request{}); !response.OK {
		t.Fatalf("history.clear = %+v", response)
	}
	if entries, err := store.Recent(0); err != nil || len(entries) != 0 {
		t.Fatalf("history after clear = %+v, err %v", entries, err)
	}
}

// IPC history.clear refreshes the surfaces that list Recently Played, as a
// history write does: the playlist manager rows and its open track list.
func TestIPCHistoryClearRefreshesTheManager(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil)
	store := history.New()
	for _, path := range []string{"/one.flac", "/two.flac"} {
		if err := store.Record(playlist.Track{Path: path}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	lp := local.New(nil, store)
	m.localProvider, m.historyStore = lp, store
	m.openPlaylistManager()
	m.plMgrEnterTrackList(history.PlaylistName)
	if len(m.plManager.tracks) != 2 {
		t.Fatalf("manager tracks = %d, want 2 before the clear", len(m.plManager.tracks))
	}

	if response := runV2(t, &m, "history.clear", ipc.Request{}); !response.OK {
		t.Fatalf("history.clear = %+v", response)
	}
	if len(m.plManager.tracks) != 0 {
		t.Fatalf("manager tracks = %d after the clear, want 0", len(m.plManager.tracks))
	}
	for _, pl := range m.plManager.playlists {
		if pl.Name == history.PlaylistName && pl.TrackCount != 0 {
			t.Fatalf("manager row %q counts %d tracks after the clear, want 0", pl.Name, pl.TrackCount)
		}
	}
}
