package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

type perfArtworkProvider struct {
	commandsTestProvider
	mu    sync.Mutex
	url   string
	found bool
	calls int
}

func (p *perfArtworkProvider) ArtworkFor(playlist.Track) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.url, p.found
}

func (p *perfArtworkProvider) setArtwork(url string, found bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.url = url
	p.found = found
}

func (p *perfArtworkProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type perfStreamBytesEngine struct {
	playbackFakeEngine
	downloaded int64
	total      int64
}

func (e *perfStreamBytesEngine) StreamBytes() (int64, int64) {
	return e.downloaded, e.total
}

type perfPlaybackStateProvider struct {
	commandsTestProvider
	hasState      bool
	hasStateCalls int
	states        map[string]provider.PlaybackState
}

func (p *perfPlaybackStateProvider) HasPlaybackState() bool {
	p.hasStateCalls++
	return p.hasState
}

func (p *perfPlaybackStateProvider) PlaybackState(track playlist.Track) (provider.PlaybackState, bool) {
	state, ok := p.states[track.Path]
	return state, ok
}

type perfBlockingLyricsSource struct {
	commandsTestProvider
	started  chan struct{}
	canceled chan struct{}
}

func (s *perfBlockingLyricsSource) TrackLyrics(ctx context.Context, _ playlist.Track) ([]lyrics.Line, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-ctx.Done()
	select {
	case <-s.canceled:
	default:
		close(s.canceled)
	}
	return nil, ctx.Err()
}

type perfCancelAlbumBrowser struct {
	commandsTestProvider
	firstPageStarted chan struct{}
	releaseFirstPage chan struct{}
	calls            atomic.Int32
}

func (p *perfCancelAlbumBrowser) AlbumList(_ string, offset, size int) ([]provider.AlbumInfo, error) {
	n := p.calls.Add(1)
	if n == 1 {
		close(p.firstPageStarted)
		<-p.releaseFirstPage
	}
	page := make([]provider.AlbumInfo, size)
	for i := range page {
		page[i] = provider.AlbumInfo{ID: fmt.Sprintf("a-%d", offset+i), Name: fmt.Sprintf("Album %d", offset+i)}
	}
	return page, nil
}

func (*perfCancelAlbumBrowser) AlbumSortTypes() []provider.SortType { return nil }
func (*perfCancelAlbumBrowser) DefaultAlbumSort() string            { return "" }

type perfCancelArtistBrowser struct {
	commandsTestProvider
	firstAlbumStarted chan struct{}
	releaseFirstAlbum chan struct{}
	albumCalls        atomic.Int32
}

func (*perfCancelArtistBrowser) Artists() ([]provider.ArtistInfo, error) { return nil, nil }

func (*perfCancelArtistBrowser) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{
		{ID: "alb-1", Name: "Album 1"},
		{ID: "alb-2", Name: "Album 2"},
		{ID: "alb-3", Name: "Album 3"},
	}, nil
}

func (p *perfCancelArtistBrowser) AlbumTracks(albumID string) ([]playlist.Track, error) {
	n := p.albumCalls.Add(1)
	if n == 1 {
		close(p.firstAlbumStarted)
		<-p.releaseFirstAlbum
	}
	return []playlist.Track{{Path: "/music/" + albumID + ".flac", Title: albumID}}, nil
}

type perfLargeCatalogProvider struct {
	commandsTestProvider
	playlists []playlist.PlaylistInfo
	added     int
}

func (p *perfLargeCatalogProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return p.playlists, nil
}

func (p *perfLargeCatalogProvider) LoadCatalogPage(int, int) (int, error) {
	return p.added, nil
}

type perfMultiPlaylistProvider struct {
	commandsTestProvider
	lists  map[string][]playlist.Track
	albums map[string][]playlist.Track
}

func (p *perfMultiPlaylistProvider) Tracks(id string) ([]playlist.Track, error) {
	return p.lists[id], nil
}

func (p *perfMultiPlaylistProvider) AlbumTracks(id string) ([]playlist.Track, error) {
	return p.albums[id], nil
}

func TestRuntimeFingerprintAndPluginStateUsePlaylistMeta(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*playlist.Playlist)
		wantIndex int
		wantTotal int
		wantQueue int
		wantShuf  bool
		wantRep   string
		wantNext  bool
		wantPath  string
	}{
		{
			name:      "initial playlist",
			mutate:    func(*playlist.Playlist) {},
			wantIndex: 0,
			wantTotal: 3,
			wantQueue: 0,
			wantRep:   "Off",
			wantNext:  true,
			wantPath:  "/music/a.flac",
		},
		{
			name: "queued and repeat all on last track",
			mutate: func(pl *playlist.Playlist) {
				pl.SetIndex(2)
				pl.Queue(0)
				pl.SetRepeat(playlist.RepeatAll)
			},
			wantIndex: 2,
			wantTotal: 3,
			wantQueue: 1,
			wantRep:   "All",
			wantNext:  true,
			wantPath:  "/music/c.flac",
		},
		{
			name: "last track with repeat off has no next",
			mutate: func(pl *playlist.Playlist) {
				pl.SetIndex(2)
			},
			wantIndex: 2,
			wantTotal: 3,
			wantQueue: 0,
			wantRep:   "Off",
			wantNext:  false,
			wantPath:  "/music/c.flac",
		},
		{
			name: "shuffle enabled",
			mutate: func(pl *playlist.Playlist) {
				pl.ToggleShuffle()
			},
			wantIndex: 0,
			wantTotal: 3,
			wantQueue: 0,
			wantShuf:  true,
			wantRep:   "Off",
			wantNext:  true,
			wantPath:  "/music/a.flac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{playing: true}
			m := newPluginStateModel(engine,
				playlist.Track{Path: "/music/a.flac", Title: "A", ProviderMeta: map[string]string{"id": "1"}},
				playlist.Track{Path: "/music/b.flac", Title: "B"},
				playlist.Track{Path: "/music/c.flac", Title: "C"},
			)
			tt.mutate(m.playlist)

			fp := m.runtimeFingerprint()
			if fp.playlistRevision != m.playlist.Revision() ||
				fp.index != tt.wantIndex ||
				fp.total != tt.wantTotal ||
				fp.playNextTotal != tt.wantQueue ||
				fp.shuffle != tt.wantShuf ||
				fp.repeat != tt.wantRep ||
				fp.logicalPath != tt.wantPath {
				t.Fatalf("runtimeFingerprint() = %+v, want index=%d total=%d queue=%d shuf=%t rep=%q path=%q",
					fp, tt.wantIndex, tt.wantTotal, tt.wantQueue, tt.wantShuf, tt.wantRep, tt.wantPath)
			}

			m.publishPluginState()
			ps := m.PluginStateLoader()()
			if ps.Index != tt.wantIndex ||
				ps.Count != tt.wantTotal ||
				ps.Shuffle != tt.wantShuf ||
				ps.Repeat != tt.wantRep ||
				ps.HasNext != tt.wantNext {
				t.Fatalf("PluginState = %+v, want index=%d count=%d shuf=%t rep=%q next=%t",
					ps, tt.wantIndex, tt.wantTotal, tt.wantShuf, tt.wantRep, tt.wantNext)
			}
		})
	}
}

func TestArtworkURLCache(t *testing.T) {
	t.Run("non-empty cover hits cache across plugin state and notifications", func(t *testing.T) {
		prov := &perfArtworkProvider{
			commandsTestProvider: commandsTestProvider{name: "ArtProv"},
			url:                  "file:///cover-a.jpg",
			found:                true,
		}
		trackA := playlist.Track{Path: "/music/a.flac", Title: "A"}
		trackB := playlist.Track{Path: "/music/b.flac", Title: "B"}
		engine := &playbackFakeEngine{playing: true}
		m := newPluginStateModel(engine, trackA, trackB)
		m.provider = prov
		m.providers = []provider.Entry{{Key: "art", Name: "ArtProv", Provider: prov}}
		m.notifier = &fakeNotifier{}
		m.setPlaybackTrack(trackA)

		for range 4 {
			updated, _ := m.Update(pluginStateTestMsg{})
			m = updated.(Model)
		}
		if got := prov.callCount(); got != 1 {
			t.Fatalf("ArtworkFor calls = %d, want 1", got)
		}
		if got := m.PluginStateLoader()().Track.ArtURL; got != "file:///cover-a.jpg" {
			t.Fatalf("PluginState ArtURL = %q, want file:///cover-a.jpg", got)
		}

		// A new track path invalidates the cache.
		prov.setArtwork("file:///cover-b.jpg", true)
		m.setPlaybackTrack(trackB)
		updated, _ := m.Update(pluginStateTestMsg{})
		m = updated.(Model)
		if got := prov.callCount(); got != 2 {
			t.Fatalf("ArtworkFor calls after track change = %d, want 2", got)
		}
		if got := m.PluginStateLoader()().Track.ArtURL; got != "file:///cover-b.jpg" {
			t.Fatalf("PluginState ArtURL = %q, want file:///cover-b.jpg", got)
		}
	})

	t.Run("empty cover does not cache and populates once available", func(t *testing.T) {
		prov := &perfArtworkProvider{
			commandsTestProvider: commandsTestProvider{name: "Telegram"},
			url:                  "",
			found:                false,
		}
		track := playlist.Track{Path: "telegram://1/2", Title: "Voice"}
		engine := &playbackFakeEngine{playing: true}
		m := newPluginStateModel(engine, track)
		m.provider = prov
		m.providers = []provider.Entry{{Key: "tg", Name: "Telegram", Provider: prov}}
		m.setPlaybackTrack(track)

		updated, _ := m.Update(pluginStateTestMsg{})
		m = updated.(Model)
		if got := m.PluginStateLoader()().Track.ArtURL; got != "" {
			t.Fatalf("ArtURL before download = %q, want empty", got)
		}

		prov.setArtwork("file:///tg-cover.jpg", true)
		updated, _ = m.Update(pluginStateTestMsg{})
		m = updated.(Model)
		callsAfterPopulate := prov.callCount()
		if got := m.PluginStateLoader()().Track.ArtURL; got != "file:///tg-cover.jpg" {
			t.Fatalf("ArtURL after download = %q, want file:///tg-cover.jpg", got)
		}

		updated, _ = m.Update(pluginStateTestMsg{})
		m = updated.(Model)
		if got := prov.callCount(); got != callsAfterPopulate {
			t.Fatalf("ArtworkFor calls = %d, want cached %d", got, callsAfterPopulate)
		}
	})

	t.Run("provider and album art key changes miss cache", func(t *testing.T) {
		prov1 := &perfArtworkProvider{
			commandsTestProvider: commandsTestProvider{name: "Prov1"},
			url:                  "file:///p1.jpg",
			found:                true,
		}
		prov2 := &perfArtworkProvider{
			commandsTestProvider: commandsTestProvider{name: "Prov2"},
			url:                  "file:///p2.jpg",
			found:                true,
		}
		track := playlist.Track{Path: "/music/song.flac", Title: "Song"}
		m := Model{
			provider: prov1,
			providers: []provider.Entry{
				{Key: "p1", Name: "Prov1", Provider: prov1},
				{Key: "p2", Name: "Prov2", Provider: prov2},
			},
		}
		if got := m.artworkURL(track); got != "file:///p1.jpg" {
			t.Fatalf("artworkURL() = %q, want file:///p1.jpg", got)
		}
		m.provider = prov2
		if got := m.artworkURL(track); got != "file:///p2.jpg" {
			t.Fatalf("artworkURL() after provider change = %q, want file:///p2.jpg", got)
		}
		track.AlbumArtURL = "file:///embedded.jpg"
		prov2.setArtwork("", false)
		if got := m.artworkURL(track); got != "file:///embedded.jpg" {
			t.Fatalf("artworkURL() after AlbumArtURL change = %q, want file:///embedded.jpg", got)
		}
	})
}

func TestAsyncRefreshEmbeddedMetadata(t *testing.T) {
	t.Run("dispatches only for local paths missing embedded tags", func(t *testing.T) {
		tests := []struct {
			name    string
			track   playlist.Track
			wantCmd bool
		}{
			{name: "local missing both", track: playlist.Track{Path: "/music/a.mp3", Title: "A"}, wantCmd: true},
			{name: "local missing art", track: playlist.Track{Path: "/music/a.mp3", EmbeddedLyrics: "hi"}, wantCmd: true},
			{name: "local missing lyrics", track: playlist.Track{Path: "/music/a.mp3", AlbumArtURL: "file:///a.jpg"}, wantCmd: true},
			{name: "local already populated", track: playlist.Track{Path: "/music/a.mp3", EmbeddedLyrics: "hi", AlbumArtURL: "file:///a.jpg"}, wantCmd: false},
			{name: "stream URL", track: playlist.Track{Path: "https://example.com/a.mp3", Stream: true}, wantCmd: false},
			{name: "ssh path", track: playlist.Track{Path: "ssh://host/music/a.mp3"}, wantCmd: false},
			{name: "empty path", track: playlist.Track{Title: "Local"}, wantCmd: false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				m := Model{player: &playbackFakeEngine{}, playlist: playlist.New()}
				_, cmd := m.beginPlaybackTrack(tt.track)
				hasRefresh := false
				if cmd != nil {
					pending := []tea.Cmd{cmd}
					for len(pending) > 0 {
						next := pending[0]
						pending = pending[1:]
						if next == nil {
							continue
						}
						switch msg := next().(type) {
						case embeddedMetaRefreshedMsg:
							hasRefresh = true
						case tea.BatchMsg:
							pending = append(pending, msg...)
						}
					}
				}
				if hasRefresh != tt.wantCmd {
					t.Fatalf("embedded refresh command = %t, want %t", hasRefresh, tt.wantCmd)
				}
			})
		}
	})

	t.Run("merges refreshed tags and updates waiting lyrics overlay", func(t *testing.T) {
		tests := []struct {
			name            string
			initial         playlist.Track
			lyricsVisible   bool
			lyricsLoading   bool
			lyricsQuery     string
			lyricsLines     []lyrics.Line
			msgGenDelta     int
			msgPath         string
			refreshed       playlist.Track
			wantArt         string
			wantLyrics      string
			wantArtist      string
			wantLyricsFetch bool
		}{
			{
				name:          "merges tags and triggers lyrics when waiting with empty query",
				initial:       playlist.Track{Path: "/music/song.mp3"},
				lyricsVisible: true,
				msgPath:       "/music/song.mp3",
				refreshed: playlist.Track{
					Path:           "/music/song.mp3",
					Title:          "Song",
					Artist:         "Artist",
					Album:          "Album",
					EmbeddedLyrics: "[00:01.00]Hello",
					AlbumArtURL:    "file:///art.jpg",
				},
				wantArt:         "file:///art.jpg",
				wantLyrics:      "[00:01.00]Hello",
				wantArtist:      "Artist",
				wantLyricsFetch: true,
			},
			{
				name:          "retriggers lyrics when embedded lyrics arrive while loading",
				initial:       playlist.Track{Path: "/music/song.mp3", Artist: "Artist", Title: "Song"},
				lyricsVisible: true,
				lyricsLoading: true,
				lyricsQuery:   "Artist\nSong",
				msgPath:       "/music/song.mp3",
				refreshed: playlist.Track{
					Path:           "/music/song.mp3",
					EmbeddedLyrics: "Line one",
					AlbumArtURL:    "file:///art.jpg",
				},
				wantArt:         "file:///art.jpg",
				wantLyrics:      "Line one",
				wantArtist:      "Artist",
				wantLyricsFetch: true,
			},
			{
				name:          "keeps loaded lyrics when overlay already has lines",
				initial:       playlist.Track{Path: "/music/song.mp3", Artist: "Artist", Title: "Song"},
				lyricsVisible: true,
				lyricsLoading: false,
				lyricsQuery:   "Artist\nSong",
				lyricsLines:   []lyrics.Line{{Text: "Existing"}},
				msgPath:       "/music/song.mp3",
				refreshed: playlist.Track{
					Path:           "/music/song.mp3",
					EmbeddedLyrics: "Embedded",
					AlbumArtURL:    "file:///art.jpg",
				},
				wantArt:         "file:///art.jpg",
				wantLyrics:      "Embedded",
				wantArtist:      "Artist",
				wantLyricsFetch: false,
			},
			{
				name:        "stale stream generation is ignored",
				initial:     playlist.Track{Path: "/music/song.mp3", Artist: "Keep"},
				msgGenDelta: -1,
				msgPath:     "/music/song.mp3",
				refreshed: playlist.Track{
					Path:           "/music/song.mp3",
					EmbeddedLyrics: "Stale",
					AlbumArtURL:    "file:///stale.jpg",
				},
				wantArt:    "",
				wantLyrics: "",
				wantArtist: "Keep",
			},
			{
				name:    "mismatched path is ignored",
				initial: playlist.Track{Path: "/music/song.mp3", Artist: "Keep"},
				msgPath: "/music/other.mp3",
				refreshed: playlist.Track{
					Path:           "/music/other.mp3",
					EmbeddedLyrics: "Other",
					AlbumArtURL:    "file:///other.jpg",
				},
				wantArt:    "",
				wantLyrics: "",
				wantArtist: "Keep",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				m := Model{
					player:   &playbackFakeEngine{playing: true},
					playlist: playlist.New(),
					lyrics: lyricsState{
						visible: tt.lyricsVisible,
						loading: tt.lyricsLoading,
						query:   tt.lyricsQuery,
						lines:   tt.lyricsLines,
					},
				}
				m.requests.stream = 5
				m.setPlaybackTrack(tt.initial)

				gen := uint64(int(m.requests.stream) + tt.msgGenDelta)
				updated, cmd := m.Update(embeddedMetaRefreshedMsg{gen: gen, path: tt.msgPath, track: tt.refreshed})
				m = updated.(Model)

				if m.playingTrack.AlbumArtURL != tt.wantArt ||
					m.playingTrack.EmbeddedLyrics != tt.wantLyrics ||
					m.playingTrack.Artist != tt.wantArtist {
					t.Fatalf("playingTrack = %+v, want art=%q lyrics=%q artist=%q",
						m.playingTrack, tt.wantArt, tt.wantLyrics, tt.wantArtist)
				}
				if (cmd != nil) != tt.wantLyricsFetch {
					t.Fatalf("lyrics fetch cmd present = %t, want %t", cmd != nil, tt.wantLyricsFetch)
				}
				if tt.wantLyricsFetch {
					var loaded lyricsLoadedMsg
					found := false
					pending := []tea.Cmd{cmd}
					for len(pending) > 0 && !found {
						next := pending[0]
						pending = pending[1:]
						if next == nil {
							continue
						}
						switch res := next().(type) {
						case lyricsLoadedMsg:
							loaded = res
							found = true
						case tea.BatchMsg:
							pending = append(pending, res...)
						}
					}
					if !found || loaded.err != nil || len(loaded.lines) == 0 {
						t.Fatalf("lyrics fetch found=%t err=%v lines=%d, want loaded embedded lyrics", found, loaded.err, len(loaded.lines))
					}
				}
			})
		}
	})
}

func TestSettingsPaneFocusAreasMatchesSettingsPaneRows(t *testing.T) {
	for _, providerCount := range []int{1, 2} {
		for _, withNet := range []bool{false, true} {
			for budget := 0; budget <= settingsPaneMaxRows+2; budget++ {
				t.Run(fmt.Sprintf("providers=%d/net=%t/budget=%d", providerCount, withNet, budget), func(t *testing.T) {
					m := newColumnTestModel(100, 30)
					m.providers = m.providers[:providerCount]
					if withNet {
						m.player = &perfStreamBytesEngine{downloaded: 2048, total: 4096}
					}
					m.recomputeLayout()

					var want []focusArea
					for _, row := range m.settingsPaneRows(budget) {
						if row.focus != focusPlaylist {
							want = append(want, row.focus)
						}
					}
					got := m.settingsPaneFocusAreas(budget)
					if !slices.Equal(got, want) {
						t.Fatalf("settingsPaneFocusAreas(%d) = %v, want %v", budget, got, want)
					}
				})
			}
		}
	}
}

func TestPlaybackHeaderBadgeFitsMatchesRender(t *testing.T) {
	for _, shuffled := range []bool{false, true} {
		for _, repeat := range []playlist.RepeatMode{playlist.RepeatOff, playlist.RepeatAll, playlist.RepeatOne} {
			for panelWidth := 0; panelWidth <= 80; panelWidth++ {
				pl := playlist.New()
				pl.Replace([]playlist.Track{
					{Path: "/music/a.flac", Title: "A"},
					{Path: "/music/b.flac", Title: "B"},
				})
				if shuffled {
					pl.ToggleShuffle()
				}
				pl.SetRepeat(repeat)

				m := Model{
					playlist: pl,
					layout:   frameLayout{tier: layoutCompact, panelWidth: panelWidth},
				}
				for _, area := range []focusArea{focusShuffle, focusRepeat} {
					probe := m
					probe.focus = area
					want := strings.Contains(ansi.Strip(probe.renderPlaybackHeader()), "["+area.label()+" ")
					got := m.playbackHeaderBadgeFits(area)
					if got != want {
						t.Fatalf("width=%d shuffled=%t repeat=%s area=%s: fits=%t, render=%t header=%q",
							panelWidth, shuffled, repeat, area.label(), got, want, ansi.Strip(probe.renderPlaybackHeader()))
					}
				}
			}
		}
	}
}

func TestRenderPlaylistSingleReporterPassAndWidthParity(t *testing.T) {
	tests := []struct {
		name       string
		panelWidth int
		queued     bool
		favorited  bool
		played     bool
		playing    bool
		selected   bool
		track      playlist.Track
	}{
		{
			name:       "plain ASCII track with duration",
			panelWidth: 74,
			track:      playlist.Track{Path: "/music/one.flac", Title: "Song One", Artist: "Artist", Album: "Album", DurationSecs: 215},
		},
		{
			name:       "selected playing queued favorited played track",
			panelWidth: 80,
			queued:     true,
			favorited:  true,
			played:     true,
			playing:    true,
			selected:   true,
			track:      playlist.Track{Path: "/music/two.flac", Title: "Song Two", Artist: "Band", Album: "Record", DurationSecs: 3725},
		},
		{
			name:       "wide CJK title and album",
			panelWidth: 68,
			queued:     true,
			favorited:  true,
			played:     true,
			track:      playlist.Track{Path: "/music/cjk.flac", Title: "東京ナイトドライブ", Artist: "山下達郎", Album: "夜のアルバム", DurationSecs: 245},
		},
		{
			name:       "unplayable track with duration",
			panelWidth: 76,
			track:      playlist.Track{Path: "/music/missing.flac", Title: "Missing Track", Unplayable: true, DurationSecs: 180},
		},
		{
			name:       "narrow panel truncates cleanly",
			panelWidth: 44,
			favorited:  true,
			track:      playlist.Track{Path: "/music/long.flac", Title: "A Very Long Track Title That Needs Truncation", Album: "Long Album", DurationSecs: 95},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(tt.track)
			if tt.queued {
				pl.Queue(0)
			}
			reporter := &perfPlaybackStateProvider{
				commandsTestProvider: commandsTestProvider{name: "Podcasts"},
				hasState:             tt.played,
				states: map[string]provider.PlaybackState{
					tt.track.Path: {Played: tt.played},
				},
			}
			m := Model{
				player:    &playbackFakeEngine{playing: tt.playing},
				playlist:  pl,
				provider:  reporter,
				providers: []provider.Entry{{Key: "pod", Name: "Podcasts", Provider: reporter}},
				focus:     focusPlaylist,
				plVisible: 4,
				layout:    frameLayout{panelWidth: tt.panelWidth},
			}
			if !tt.selected {
				m.focus = focusVolume
			}
			if tt.favorited {
				m.favSet = map[string]struct{}{tt.track.Path: {}}
			}

			out := m.renderPlaylist()
			if reporter.hasStateCalls != 1 {
				t.Fatalf("HasPlaybackState calls = %d, want 1", reporter.hasStateCalls)
			}
			firstLine := strings.Split(out, "\n")[0]
			if got := lipgloss.Width(firstLine); got != tt.panelWidth {
				t.Fatalf("rendered row width = %d, want %d: %q", got, tt.panelWidth, ansi.Strip(firstLine))
			}
		})
	}
}

func TestLyricsCancellation(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(t *testing.T, m Model) Model
	}{
		{
			name: "closing lyrics overlay cancels lookup",
			cancel: func(_ *testing.T, m Model) Model {
				updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				return updated.(Model)
			},
		},
		{
			name: "starting another track cancels lookup",
			cancel: func(_ *testing.T, m Model) Model {
				m.lyrics.visible = false
				m.beginPlaybackTrack(playlist.Track{Path: "https://example.com/next.mp3", Title: "Next", Stream: true})
				return m
			},
		},
		{
			name: "new lyrics fetch cancels previous lookup",
			cancel: func(_ *testing.T, m Model) Model {
				m.lyrics.loading = false
				_ = m.retryLyrics()
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &perfBlockingLyricsSource{
				commandsTestProvider: commandsTestProvider{name: "Spotify"},
				started:              make(chan struct{}),
				canceled:             make(chan struct{}),
			}
			track := playlist.Track{Path: "spotify:track:1", Title: "Song", Artist: "Artist"}
			pl := playlist.New()
			pl.Add(track)
			m := New(&playbackFakeEngine{playing: true}, pl, []provider.Entry{{Key: "spotify", Name: "Spotify", Provider: source}}, "spotify", nil, nil, nil, nil, nil, nil)
			m.lyrics.visible = true

			cmd := m.retryLyrics()
			if cmd == nil {
				t.Fatal("retryLyrics() = nil, want fetch command")
			}
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()

			select {
			case <-source.started:
			case <-time.After(3 * time.Second):
				t.Fatal("lyrics lookup did not start")
			}

			// Pass Model by value to verify cancellation works across value copies.
			_ = tt.cancel(t, m)

			select {
			case <-source.canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("lyrics lookup context was not canceled")
			}
			select {
			case msg := <-done:
				loaded, ok := msg.(lyricsLoadedMsg)
				if !ok || !errors.Is(loaded.err, context.Canceled) {
					t.Fatalf("lyrics result = %T %+v, want context.Canceled", msg, msg)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("lyrics command did not exit after cancel")
			}
		})
	}
}

func TestNavBatchCancellation(t *testing.T) {
	t.Run("remaining albums fetch stops between pages on cancel", func(t *testing.T) {
		prov := &perfCancelAlbumBrowser{
			commandsTestProvider: commandsTestProvider{name: "Navidrome"},
			firstPageStarted:     make(chan struct{}),
			releaseFirstPage:     make(chan struct{}),
		}
		m := New(&playbackFakeEngine{}, playlist.New(), []provider.Entry{{Key: "nav", Name: "Navidrome", Provider: prov}}, "nav", nil, nil, nil, nil, nil, nil)
		m.navBrowser = navBrowserState{
			prov:      prov,
			visible:   true,
			mode:      navBrowseModeByAlbum,
			screen:    navBrowseScreenList,
			albums:    make([]provider.AlbumInfo, navAlbumPageSize),
			albumDone: false,
		}

		cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Text: "/"})
		if cmd == nil {
			t.Fatal("album search did not start remaining-pages command")
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()

		select {
		case <-prov.firstPageStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("first album page did not start")
		}

		// Cancel via a value copy of Model to verify shared cancelSlot behavior.
		mCopy := m
		mCopy.cancelNavRequests()
		close(prov.releaseFirstPage)

		select {
		case msg := <-done:
			loaded, ok := msg.(navAlbumsLoadedMsg)
			if !ok || !errors.Is(loaded.err, context.Canceled) {
				t.Fatalf("albums result = %T %+v, want context.Canceled", msg, msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("remaining albums command did not stop after cancel")
		}
		if got := prov.calls.Load(); got != 1 {
			t.Fatalf("AlbumList calls = %d, want 1", got)
		}
	})

	t.Run("artist all tracks fetch stops between albums on close", func(t *testing.T) {
		prov := &perfCancelArtistBrowser{
			commandsTestProvider: commandsTestProvider{name: "Navidrome"},
			firstAlbumStarted:    make(chan struct{}),
			releaseFirstAlbum:    make(chan struct{}),
		}
		m := New(&playbackFakeEngine{}, playlist.New(), []provider.Entry{{Key: "nav", Name: "Navidrome", Provider: prov}}, "nav", nil, nil, nil, nil, nil, nil)
		m.navBrowser = navBrowserState{
			prov:    prov,
			visible: true,
			mode:    navBrowseModeByArtist,
			screen:  navBrowseScreenList,
			artists: []provider.ArtistInfo{{ID: "art-1", Name: "Artist 1"}},
		}

		cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("artist selection did not start track batch command")
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()

		select {
		case <-prov.firstAlbumStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("first album track fetch did not start")
		}

		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		m = updated.(Model)
		close(prov.releaseFirstAlbum)

		select {
		case msg := <-done:
			loaded, ok := msg.(navTracksLoadedMsg)
			if !ok || !errors.Is(loaded.err, context.Canceled) {
				t.Fatalf("tracks result = %T %+v, want context.Canceled", msg, msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("artist tracks command did not stop after cancel")
		}
		if got := prov.albumCalls.Load(); got != 1 {
			t.Fatalf("AlbumTracks calls = %d, want 1", got)
		}
	})
}

func TestIPCConcurrentProviderLoadGenerationAndInlineCommit(t *testing.T) {
	prov := &perfMultiPlaylistProvider{
		commandsTestProvider: commandsTestProvider{name: "Remote"},
		lists: map[string][]playlist.Track{
			"slow": {{Path: "/music/slow.flac", Title: "Slow"}},
			"fast": {{Path: "/music/fast.flac", Title: "Fast"}},
		},
		albums: map[string][]playlist.Track{
			"alb-slow": {{Path: "/music/alb-slow.flac", Title: "Album Slow"}},
			"alb-fast": {{Path: "/music/alb-fast.flac", Title: "Album Fast"}},
		},
	}

	tests := []struct {
		name     string
		op       string
		slowReq  ipc.Request
		fastReq  ipc.Request
		wantPath string
	}{
		{
			name:     "concurrent provider.load keeps newer load",
			op:       "provider.load",
			slowReq:  ipc.Request{Provider: "remote", Playlist: "slow"},
			fastReq:  ipc.Request{Provider: "remote", Playlist: "fast"},
			wantPath: "/music/fast.flac",
		},
		{
			name:     "concurrent provider.load_album keeps newer load",
			op:       "provider.load_album",
			slowReq:  ipc.Request{Provider: "remote", Album: "alb-slow"},
			fastReq:  ipc.Request{Provider: "remote", Album: "alb-fast"},
			wantPath: "/music/alb-fast.flac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "remote", Name: "Remote", Provider: prov}})
			m.SetIPCBroker(ipc.NewBroker())

			slowReq := v2Request(t, tt.op, tt.slowReq)
			fastReq := v2Request(t, tt.op, tt.fastReq)

			updated, slowCmd := m.Update(slowReq)
			m = updated.(Model)
			updated, fastCmd := m.Update(fastReq)
			m = updated.(Model)

			fastMsg := fastCmd()
			slowMsg := slowCmd()

			// Apply newer load first and confirm inline completion with matching snapshot.
			updated, _ = m.Update(fastMsg)
			m = updated.(Model)
			fastJob, _ := fastReq.Jobs.Get(fastReq.JobID)
			if fastJob.State != ipc.JobSucceeded {
				t.Fatalf("fast job = %+v, want succeeded inline", fastJob)
			}
			if fastJob.Snapshot == nil || fastJob.Snapshot.LogicalTrack == nil || fastJob.Snapshot.LogicalTrack.Path != tt.wantPath {
				t.Fatalf("fast job snapshot = %+v, want logical track %q", fastJob.Snapshot, tt.wantPath)
			}

			// The slower older load lands second and fails with a conflict.
			updated, _ = m.Update(slowMsg)
			m = updated.(Model)
			slowJob, _ := slowReq.Jobs.Get(slowReq.JobID)
			if slowJob.State != ipc.JobFailed || slowJob.Error == nil || slowJob.Error.Code != ipc.V2ErrorCodeConflict {
				t.Fatalf("slow job = %+v, want conflict error", slowJob)
			}
			if cur, _ := m.playlist.Current(); cur.Path != tt.wantPath {
				t.Fatalf("playlist current = %q, want %q", cur.Path, tt.wantPath)
			}
		})
	}
}

func TestIPCURLInlineCommitSnapshot(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "added.mp3")
	if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newHeadlessModel(t, &headlessEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}, nil,
		playlist.Track{Path: "/music/existing.flac", Title: "Existing"})
	m.SetIPCBroker(ipc.NewBroker())

	req := v2Request(t, "url.load", ipc.Request{Path: filePath, Play: true})
	updated, cmd := m.Update(req)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("url.load returned nil command")
	}
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(Model)

	job, _ := req.Jobs.Get(req.JobID)
	if job.State != ipc.JobSucceeded {
		t.Fatalf("url.load job = %+v, want succeeded inline", job)
	}
	if job.Snapshot == nil || job.Snapshot.Total != 2 || job.Snapshot.Index != 1 {
		t.Fatalf("url.load snapshot = %+v, want total=2 index=1", job.Snapshot)
	}
}

func TestIPCResponseItemCap(t *testing.T) {
	const totalItems = 250
	tracks := make([]playlist.Track, totalItems)
	playlists := make([]playlist.PlaylistInfo, totalItems)
	for i := range totalItems {
		tracks[i] = playlist.Track{Path: fmt.Sprintf("/music/%03d.flac", i), Title: fmt.Sprintf("Track %d", i)}
		playlists[i] = playlist.PlaylistInfo{ID: fmt.Sprintf("c:%d", i), Name: fmt.Sprintf("Station %d", i)}
	}

	t.Run("provider.load caps tracks at 200 and preserves total", func(t *testing.T) {
		prov := fixedTracksProvider{commandsTestProvider{name: "Big"}, tracks}
		m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "big", Name: "Big", Provider: prov}})
		resp := runV2(t, &m, "provider.load", ipc.Request{Provider: "big", Playlist: "huge"})
		if !resp.OK || len(resp.Tracks) != maxIPCResponseTracks || resp.Total != totalItems {
			t.Fatalf("provider.load = ok:%t len:%d total:%d, want ok:true len:%d total:%d",
				resp.OK, len(resp.Tracks), resp.Total, maxIPCResponseTracks, totalItems)
		}
		if m.playlist.Len() != totalItems {
			t.Fatalf("playlist len = %d, want all %d tracks loaded", m.playlist.Len(), totalItems)
		}
	})

	t.Run("url.load caps tracks at 200 and preserves total", func(t *testing.T) {
		m := newHeadlessModel(t, &headlessEngine{}, nil)
		jobs := ipc.NewJobStore()
		t.Cleanup(jobs.CancelAll)
		job, err := jobs.Create("url.load")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jobs.Start(job.ID); err != nil {
			t.Fatal(err)
		}
		updated, _ := m.Update(ipcURLLoadResult{
			request: ipcURLRequest{Jobs: jobs, JobID: job.ID},
			tracks:  tracks,
		})
		m = updated.(Model)
		done, _ := jobs.Get(job.ID)
		var resp ipc.Response
		if err := json.Unmarshal(done.Result, &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.OK || len(resp.Tracks) != maxIPCResponseTracks || resp.Total != totalItems {
			t.Fatalf("url.load = ok:%t len:%d total:%d, want ok:true len:%d total:%d",
				resp.OK, len(resp.Tracks), resp.Total, maxIPCResponseTracks, totalItems)
		}
		if m.playlist.Len() != totalItems {
			t.Fatalf("playlist len = %d, want all %d tracks appended", m.playlist.Len(), totalItems)
		}
	})

	t.Run("provider.catalog caps playlists at 200 and preserves added count", func(t *testing.T) {
		prov := &perfLargeCatalogProvider{
			commandsTestProvider: commandsTestProvider{name: "Radio"},
			playlists:            playlists,
			added:                50,
		}
		m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "radio", Name: "Radio", Provider: prov}})
		resp := runV2(t, &m, "provider.catalog", ipc.Request{Provider: "radio", Limit: 50})
		if !resp.OK || len(resp.Playlists) != maxIPCResponseTracks || resp.Total != 50 {
			t.Fatalf("provider.catalog = ok:%t len:%d total:%d, want ok:true len:%d total:50",
				resp.OK, len(resp.Playlists), resp.Total, maxIPCResponseTracks)
		}
	})
}

func TestV2ThemeUsesCachedThemesBeforeDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { applyThemeAll(theme.Default()) })

	cached := theme.Theme{Name: "InMemoryOnly"}
	tests := []struct {
		name      string
		themes    []theme.Theme
		reqName   string
		wantOK    bool
		wantTheme string
	}{
		{
			name:      "known cached theme succeeds without reloading disk",
			themes:    []theme.Theme{cached},
			reqName:   "InMemoryOnly",
			wantOK:    true,
			wantTheme: "InMemoryOnly",
		},
		{
			name:      "missing theme falls back to LoadAll for built-in theme",
			themes:    nil,
			reqName:   "dracula",
			wantOK:    true,
			wantTheme: "Dracula",
		},
		{
			name:    "unknown theme returns not found",
			themes:  []theme.Theme{cached},
			reqName: "does-not-exist",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				player:   &playbackFakeEngine{},
				playlist: playlist.New(),
				vis:      ui.NewVisualizer(44100),
				themes:   tt.themes,
				themeIdx: -1,
			}
			resp := runV2(t, &m, "theme", ipc.Request{Name: tt.reqName})
			if resp.OK != tt.wantOK {
				t.Fatalf("theme %q response = %+v, want ok=%t", tt.reqName, resp, tt.wantOK)
			}
			if tt.wantOK && !strings.EqualFold(m.ThemeName(), tt.wantTheme) {
				t.Fatalf("ThemeName() = %q, want %q", m.ThemeName(), tt.wantTheme)
			}
		})
	}
}

func TestBrowsePeekingPreservesMainAlbumHeaders(t *testing.T) {
	cohesive := []playlist.Track{
		{Path: "/music/a1.flac", Title: "A1", Album: "Album A"},
		{Path: "/music/a2.flac", Title: "A2", Album: "Album A"},
		{Path: "/music/a3.flac", Title: "A3", Album: "Album A"},
	}
	fragmented := []playlist.Track{
		{Path: "/music/x1.flac", Title: "X1", Album: "Mix 1"},
		{Path: "/music/x2.flac", Title: "X2", Album: "Mix 2"},
		{Path: "/music/x3.flac", Title: "X3", Album: "Mix 3"},
	}

	tests := []struct {
		name            string
		mainTracks      []playlist.Track
		manualToggle    bool
		peekTracks      []playlist.Track
		wantMainHeaders bool
		wantPeekHeaders bool
	}{
		{
			name:            "peeking fragmented list keeps main cohesive headers",
			mainTracks:      cohesive,
			peekTracks:      fragmented,
			wantMainHeaders: true,
			wantPeekHeaders: false,
		},
		{
			name:            "peeking cohesive list keeps main manual override off",
			mainTracks:      cohesive,
			manualToggle:    true,
			peekTracks:      cohesive,
			wantMainHeaders: false,
			wantPeekHeaders: true,
		},
		{
			name:            "peeking cohesive list keeps main fragmented headers off",
			mainTracks:      fragmented,
			peekTracks:      cohesive,
			wantMainHeaders: false,
			wantPeekHeaders: true,
		},
	}

	for _, tt := range tests {
		t.Run("navBrowser/"+tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Replace(tt.mainTracks)
			m := Model{
				player:    &playbackFakeEngine{},
				playlist:  pl,
				layout:    frameLayout{panelWidth: 80},
				plVisible: 12,
			}
			m.setHeaderStateFromTracks(tt.mainTracks)
			if tt.manualToggle {
				m.toggleAlbumHeadersManual()
			}
			wantManual := m.headerManual
			wantLastAlbum := m.headerLastAlbum
			wantSegments := m.headerSegments
			wantTracks := m.headerTracks

			m.navBrowser = navBrowserState{
				prov:    commandsTestProvider{name: "Navidrome"},
				visible: true,
				mode:    navBrowseModeByAlbum,
				screen:  navBrowseScreenList,
			}
			m.requests.nav = 1
			m.handleNavTracksLoaded(navTracksLoadedMsg{tracks: tt.peekTracks, gen: 1})

			if m.navBrowser.showAlbumHeaders != tt.wantPeekHeaders {
				t.Fatalf("navBrowser.showAlbumHeaders = %t, want %t", m.navBrowser.showAlbumHeaders, tt.wantPeekHeaders)
			}
			if m.showAlbumHeaders != tt.wantMainHeaders ||
				m.headerManual != wantManual ||
				m.headerLastAlbum != wantLastAlbum ||
				m.headerSegments != wantSegments ||
				m.headerTracks != wantTracks {
				t.Fatalf("main header state changed: show=%t manual=%t last=%q seg=%d tracks=%d",
					m.showAlbumHeaders, m.headerManual, m.headerLastAlbum, m.headerSegments, m.headerTracks)
			}

			body := ansi.Strip(m.renderNavTrackBody(10))
			if strings.Contains(body, "──") != tt.wantPeekHeaders {
				t.Fatalf("nav render headers mismatch for wantPeekHeaders=%t:\n%s", tt.wantPeekHeaders, body)
			}

			// Ctrl+H inside nav flips only navBrowser.showAlbumHeaders.
			m.handleNavTrackListKey(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
			if m.navBrowser.showAlbumHeaders == tt.wantPeekHeaders {
				t.Fatal("ctrl+h did not toggle navBrowser.showAlbumHeaders")
			}
			if m.showAlbumHeaders != tt.wantMainHeaders || m.headerManual != wantManual {
				t.Fatal("ctrl+h in nav changed main playlist album header state")
			}
		})

		t.Run("plManager/"+tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Replace(tt.mainTracks)
			local := fixedTracksProvider{commandsTestProvider{name: "Local"}, tt.peekTracks}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      pl,
				localProvider: local,
				layout:        frameLayout{panelWidth: 80},
				plVisible:     12,
			}
			m.setHeaderStateFromTracks(tt.mainTracks)
			if tt.manualToggle {
				m.toggleAlbumHeadersManual()
			}
			wantManual := m.headerManual
			wantLastAlbum := m.headerLastAlbum
			wantSegments := m.headerSegments
			wantTracks := m.headerTracks

			m.plManager.visible = true
			m.plMgrEnterTrackList("Peek")

			if m.plManager.showAlbumHeaders != tt.wantPeekHeaders {
				t.Fatalf("plManager.showAlbumHeaders = %t, want %t", m.plManager.showAlbumHeaders, tt.wantPeekHeaders)
			}
			if m.showAlbumHeaders != tt.wantMainHeaders ||
				m.headerManual != wantManual ||
				m.headerLastAlbum != wantLastAlbum ||
				m.headerSegments != wantSegments ||
				m.headerTracks != wantTracks {
				t.Fatalf("main header state changed: show=%t manual=%t last=%q seg=%d tracks=%d",
					m.showAlbumHeaders, m.headerManual, m.headerLastAlbum, m.headerSegments, m.headerTracks)
			}

			body := ansi.Strip(m.renderPlMgrTracksBody())
			if strings.Contains(body, "──") != tt.wantPeekHeaders {
				t.Fatalf("plManager render headers mismatch for wantPeekHeaders=%t:\n%s", tt.wantPeekHeaders, body)
			}

			// Ctrl+H inside plManager flips only plManager.showAlbumHeaders.
			m.handlePlMgrTracksKey(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
			if m.plManager.showAlbumHeaders == tt.wantPeekHeaders {
				t.Fatal("ctrl+h did not toggle plManager.showAlbumHeaders")
			}
			if m.showAlbumHeaders != tt.wantMainHeaders || m.headerManual != wantManual {
				t.Fatal("ctrl+h in plManager changed main playlist album header state")
			}
		})
	}
}

type perfCountingEngine struct {
	playbackFakeEngine
	streamErr         error
	posDurCalls       int
	durationCalls     int
	seekableCalls     int
	streamErrCalls    int
	stereoSampleCalls int
}

func (e *perfCountingEngine) PositionAndDuration() (time.Duration, time.Duration) {
	e.posDurCalls++
	return e.position, e.duration
}

func (e *perfCountingEngine) Duration() time.Duration {
	e.durationCalls++
	return e.duration
}

func (e *perfCountingEngine) Seekable() bool {
	e.seekableCalls++
	return e.seekable
}

func (e *perfCountingEngine) StreamErr() error {
	e.streamErrCalls++
	return e.streamErr
}

func (e *perfCountingEngine) StereoSamplesInto(dst [][2]float64) int {
	e.stereoSampleCalls++
	if len(dst) == 0 {
		return 0
	}
	dst[0] = [2]float64{0.5, -0.5}
	return 1
}

func TestTerminalTextASCIIFastPath(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		zeroAlloc bool
	}{
		{name: "empty", in: "", want: "", zeroAlloc: true},
		{name: "printable ASCII", in: "Artist - Song Title (2026) [FLAC]", want: "Artist - Song Title (2026) [FLAC]", zeroAlloc: true},
		{name: "ANSI escape stripped", in: "\x1b[31mRed\x1b[0m Text", want: "Red Text"},
		{name: "newlines and tabs normalized", in: "Line1\nLine2\r\tEnd", want: "Line1 Line2  End"},
		{name: "control chars dropped", in: "Safe\x00\x07\x7fText", want: "SafeText"},
		{name: "non-ASCII UTF-8 preserved", in: "山下達郎 - 東京ナイトドライブ", want: "山下達郎 - 東京ナイトドライブ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalText(tt.in); got != tt.want {
				t.Fatalf("terminalText(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if tt.zeroAlloc {
				allocs := testing.AllocsPerRun(50, func() {
					if got := terminalText(tt.in); got != tt.want {
						t.Fatalf("terminalText(%q) = %q, want %q", tt.in, got, tt.want)
					}
				})
				if allocs != 0 {
					t.Fatalf("terminalText(%q) allocs = %v, want 0", tt.in, allocs)
				}
			}
		})
	}
}

func TestRenderFramedParityAndBounds(t *testing.T) {
	tests := []struct {
		name           string
		width, height  int
		padH, padV     int
		content        string
		wantTopBlanks  int
		wantFirstBlank bool
	}{
		{
			name:          "centered frame matches lipgloss top padding",
			width:         80,
			height:        20,
			padH:          3,
			padV:          1,
			content:       "line1\nline2\nline3\nline4",
			wantTopBlanks: (20 - (4 + 2)) / 2,
		},
		{
			name:          "exact height has no centering blanks",
			width:         80,
			height:        6,
			padH:          2,
			padV:          1,
			content:       "a\nb\nc\nd",
			wantTopBlanks: 0,
		},
		{
			name:          "zero horizontal padding still marks vertical padding row",
			width:         40,
			height:        10,
			padH:          0,
			padV:          1,
			content:       "one\ntwo",
			wantTopBlanks: (10 - 4) / 2,
		},
		{
			name:          "overflow lines stop at terminal height and truncate wide lines",
			width:         40,
			height:        5,
			padH:          2,
			padV:          1,
			content:       strings.Repeat("界", 30) + "\n2\n3\n4\n5\n6",
			wantTopBlanks: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				width:  tt.width,
				height: tt.height,
				layout: frameLayout{
					frameWidth: tt.width,
					panelWidth: max(1, tt.width-2*tt.padH),
					paddingH:   tt.padH,
					paddingV:   tt.padV,
				},
			}
			out := m.renderFramed(tt.content)
			lines := strings.Split(out, "\n")
			if len(lines) > tt.height {
				t.Fatalf("renderFramed height = %d, want <= %d", len(lines), tt.height)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > tt.width {
					t.Fatalf("line %d width = %d, want <= %d: %q", i, w, tt.width, line)
				}
			}
			gotBlanks := 0
			for _, line := range lines {
				if line != "" {
					break
				}
				gotBlanks++
			}
			if gotBlanks != tt.wantTopBlanks {
				t.Fatalf("leading blanks = %d, want %d\n%q", gotBlanks, tt.wantTopBlanks, out)
			}
		})
	}
}

func TestTitleIntroFrameMatchesPaddedSlice(t *testing.T) {
	intro := []rune("Hello, 世界!")
	for _, viewport := range []int{0, 4, 10, 18} {
		maxOff := titleIntroMaxOffset(viewport, len(intro))
		for offset := -2; offset <= maxOff+2; offset++ {
			got := titleIntroFrame(offset, viewport, intro)
			if viewport == 0 {
				if got != "" {
					t.Fatalf("titleIntroFrame(%d, 0) = %q, want empty", offset, got)
				}
				continue
			}
			clamped := min(max(offset, 0), maxOff)
			padded := make([]rune, 0, 2*viewport+len(intro))
			for range viewport {
				padded = append(padded, ' ')
			}
			padded = append(padded, intro...)
			for range viewport {
				padded = append(padded, ' ')
			}
			want := string(padded[clamped : clamped+viewport])
			if got != want {
				t.Fatalf("titleIntroFrame(%d, %d) = %q, want %q", offset, viewport, got, want)
			}
		}
	}
}

func TestRenderCachesHitInvalidateAndHandleNil(t *testing.T) {
	t.Run("zero-value model with nil caches renders safely", func(t *testing.T) {
		pl := playlist.New()
		pl.Add(playlist.Track{Path: "/music/a.flac", Title: "Song A"})
		m := Model{
			player:   &playbackFakeEngine{playing: true},
			playlist: pl,
			vis:      ui.NewVisualizer(44100),
			width:    100,
			height:   30,
		}
		m.recomputeLayout()
		view := m.View()
		if view.Content == "" {
			t.Fatal("View() returned empty content for zero-value caches")
		}
	})

	t.Run("help, playlist, settings, and title caches hit and invalidate", func(t *testing.T) {
		pl := playlist.New()
		for i := range 20 {
			pl.Add(playlist.Track{
				Path:         fmt.Sprintf("/music/%02d.flac", i),
				Title:        fmt.Sprintf("Track %d", i+1),
				Artist:       "Artist",
				Album:        "Album",
				DurationSecs: 200,
			})
		}
		engine := &playbackFakeEngine{playing: true, seekable: true, duration: 200 * time.Second}
		providers := []provider.Entry{
			{Key: "local", Name: "Local", Provider: &commandsTestProvider{name: "Local"}},
			{Key: "radio", Name: "Radio", Provider: &commandsTestProvider{name: "Radio"}},
		}
		m := New(engine, pl, providers, "local", nil, nil, nil, []theme.Theme{{Name: "Dracula", Accent: "#bd93f9"}}, nil, nil)
		m.termTitle.introActive = false
		m.width, m.height = 120, 36
		m.recomputeLayout()
		m.setPlaybackTrack(pl.Tracks()[0])

		// Populate all four caches.
		firstHelp := m.renderHelp()
		firstPlaylist := m.renderPlaylist()
		firstSettings := m.renderSettingsBuiltin(m.effectivePlaylistVisible())
		firstTitle := m.cachedWindowTitle()

		if !m.helpCache.valid || !m.playlistCache.valid || !m.settingsCache.valid || !m.titleCache.valid {
			t.Fatal("expected all four render caches to populate")
		}

		// Sentinel value proves subsequent calls return the cached string.
		m.helpCache.value = "CACHED_HELP"
		if got := m.renderHelp(); got != "CACHED_HELP" {
			t.Fatalf("helpCache missed on identical state: got %q", got)
		}
		m.simplified = true
		if got := m.renderHelp(); got == "CACHED_HELP" {
			t.Fatal("helpCache served stale value after simplified toggle")
		}
		m.simplified = false
		if got := m.renderHelp(); got != firstHelp {
			t.Fatalf("help mismatch after restore: got %q, want %q", got, firstHelp)
		}

		m.playlistCache.value = "CACHED_PLAYLIST"
		if got := m.renderPlaylist(); got != "CACHED_PLAYLIST" {
			t.Fatalf("playlistCache missed on identical state: got %q", got)
		}
		m.plCursor = 1
		if got := m.renderPlaylist(); got == "CACHED_PLAYLIST" || got == firstPlaylist {
			t.Fatal("playlistCache served stale value after cursor move")
		}
		m.plCursor = 0

		m.settingsCache.value = "CACHED_SETTINGS"
		if got := m.renderSettingsBuiltin(m.effectivePlaylistVisible()); got != "CACHED_SETTINGS" {
			t.Fatalf("settingsCache missed on identical state: got %q", got)
		}
		engine.volume = -3
		if got := m.renderSettingsBuiltin(m.effectivePlaylistVisible()); got == "CACHED_SETTINGS" || got == firstSettings {
			t.Fatal("settingsCache served stale value after volume change")
		}
		engine.volume = 0

		m.titleCache.value = "CACHED_TITLE"
		if got := m.cachedWindowTitle(); got != "CACHED_TITLE" {
			t.Fatalf("titleCache missed on identical state: got %q", got)
		}
		engine.paused = true
		if got := m.cachedWindowTitle(); got == "CACHED_TITLE" || got == firstTitle {
			t.Fatal("titleCache served stale value after pause change")
		}
	})

	t.Run("playlist cache bypasses when PlaybackStateReporter has active state", func(t *testing.T) {
		pl := playlist.New()
		track := playlist.Track{Path: "/pod/ep1.mp3", Title: "Episode 1"}
		pl.Add(track)
		reporter := &perfPlaybackStateProvider{
			commandsTestProvider: commandsTestProvider{name: "Podcasts"},
			hasState:             true,
			states:               map[string]provider.PlaybackState{track.Path: {Position: 30 * time.Second}},
		}
		m := New(&playbackFakeEngine{}, pl, []provider.Entry{{Key: "pod", Name: "Podcasts", Provider: reporter}}, "pod", nil, nil, nil, nil, nil, nil)
		m.width, m.height = 100, 30
		m.recomputeLayout()

		before := ansi.Strip(m.renderPlaylist())
		if !strings.Contains(before, partialMarker) {
			t.Fatalf("initial render missing partial marker: %q", before)
		}
		reporter.states[track.Path] = provider.PlaybackState{Played: true}
		after := ansi.Strip(m.renderPlaylist())
		if !strings.Contains(after, playedMarker) {
			t.Fatalf("updated render missing played marker: %q", after)
		}
	})
}

func TestProviderPaneNonSectionedListWithSectionHeadersScrolls(t *testing.T) {
	prov := commandsTestProvider{name: "Plain"}
	lists := []playlist.PlaylistInfo{
		{ID: "1", Name: "Item 1", Section: "Alpha"},
		{ID: "2", Name: "Item 2", Section: "Alpha"},
		{ID: "3", Name: "Item 3", Section: "Beta"},
		{ID: "4", Name: "Item 4", Section: "Beta"},
		{ID: "5", Name: "Item 5", Section: "Gamma"},
		{ID: "6", Name: "Item 6", Section: "Gamma"},
	}

	m := Model{
		provider:  prov,
		focus:     focusProvider,
		plVisible: 4,
		layout:    frameLayout{panelWidth: 60},
	}
	m.replaceProviderLists(lists)
	if !m.provPane.hasSections || !m.provPane.sectionsCached {
		t.Fatalf("replaceProviderLists did not cache hasSections: %+v", m.provPane)
	}

	for cursor := range lists {
		m.provPane.cursor = cursor
		out := ansi.Strip(m.renderProviderList())
		wantRow := "> " + lists[cursor].Name
		if !strings.Contains(out, wantRow) {
			t.Fatalf("cursor %d (%s) scrolled out of view:\n%s", cursor, wantRow, out)
		}
	}
}

func TestCommandRegistryModeGroupingMatchesFullScan(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	for bit := commandModeMain; bit <= commandModeFullVis; bit <<= 1 {
		want := 0
		for _, cmd := range commandRegistry {
			if cmd.ContextHelp && cmd.Mode&bit != 0 {
				want++
			}
		}
		got := contextHelpByMode[bit]
		if len(got) != want {
			t.Fatalf("mode %#x grouped %d context help commands, want %d", uint64(bit), len(got), want)
		}
	}
}

func TestCachedClockSeekableStreamErrAndLiveCheck(t *testing.T) {
	engine := &perfCountingEngine{
		playbackFakeEngine: playbackFakeEngine{
			playing:  true,
			seekable: true,
			position: 45 * time.Second,
			duration: 180 * time.Second,
		},
		streamErr: errors.New("dropped"),
	}
	pl := playlist.New()
	ytTrack := playlist.Track{Path: "https://www.youtube.com/watch?v=abc", Title: "VOD", Stream: true, Realtime: true}
	pl.Add(ytTrack)

	m := New(engine, pl, nil, "", nil, nil, nil, nil, nil, nil)
	m.setPlaybackTrack(ytTrack)

	// Non-tick cachedClock miss populates clock, seekable, and stream error.
	pos, dur := m.cachedClock()
	if pos != 45*time.Second || dur != 180*time.Second || !m.clockFresh {
		t.Fatalf("cachedClock() = (%v, %v) fresh=%t, want (45s, 180s) fresh=true", pos, dur, m.clockFresh)
	}
	if engine.posDurCalls != 1 || engine.seekableCalls != 1 || engine.streamErrCalls != 1 {
		t.Fatalf("engine calls after first miss = posDur:%d seekable:%d streamErr:%d, want 1/1/1",
			engine.posDurCalls, engine.seekableCalls, engine.streamErrCalls)
	}

	// Second read hits cache and currentPlaybackIsLive uses cachedDur.
	_, _ = m.cachedClock()
	_ = m.playerSeekable()
	_ = m.playerStreamErr()
	if live := m.currentPlaybackIsLive(ytTrack); live {
		t.Fatal("currentPlaybackIsLive() = true for finite cached duration, want false")
	}
	if engine.posDurCalls != 1 || engine.durationCalls != 0 || engine.seekableCalls != 1 || engine.streamErrCalls != 1 {
		t.Fatalf("cache hit called engine unexpectedly: posDur:%d dur:%d seekable:%d streamErr:%d",
			engine.posDurCalls, engine.durationCalls, engine.seekableCalls, engine.streamErrCalls)
	}

	// tickSampleClock refreshes all cached fields once.
	m.clockFresh = false
	m.tickSampleClock()
	if engine.posDurCalls != 2 || engine.seekableCalls != 2 || engine.streamErrCalls != 2 {
		t.Fatalf("tickSampleClock calls = posDur:%d seekable:%d streamErr:%d, want 2/2/2",
			engine.posDurCalls, engine.seekableCalls, engine.streamErrCalls)
	}
}

func TestCompleteV2JobComputesSingleRuntimeSnapshot(t *testing.T) {
	engine := &perfCountingEngine{
		playbackFakeEngine: playbackFakeEngine{
			playing:  true,
			seekable: true,
			position: 10 * time.Second,
			duration: 120 * time.Second,
		},
	}
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "/music/a.flac", Title: "A"})
	m := New(engine, pl, nil, "", nil, nil, nil, nil, nil, nil)
	m.SetIPCBroker(ipc.NewBroker())

	jobs := ipc.NewJobStore()
	t.Cleanup(jobs.CancelAll)
	job, err := jobs.Create("volume")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(job.ID); err != nil {
		t.Fatal(err)
	}

	m.tickSampleClock()
	beforePosDur := engine.posDurCalls
	m.completeV2Job(jobs, job.ID, ipc.Response{OK: true})

	// runtimeSnapshot calls playbackClock once. A duplicate snapshot calls it twice.
	if got := engine.posDurCalls - beforePosDur; got != 1 {
		t.Fatalf("PositionAndDuration calls in completeV2Job = %d, want 1", got)
	}
	done, ok := jobs.Get(job.ID)
	if !ok || done.State != ipc.JobSucceeded || done.Snapshot == nil || done.Snapshot.Revision != 1 {
		t.Fatalf("completed job = %+v, want succeeded with revision 1", done)
	}
}

func TestPaneGenTracksMutationsInLayoutAndTick(t *testing.T) {
	engine := &playbackFakeEngine{playing: true}
	m := New(engine, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
	panes := ui.NewPaneSet()
	m.SetPaneSet(panes)
	m.width, m.height = 100, 30
	m.recomputeLayout()

	if m.layoutKey.paneGen != 0 || m.vis.Cols != m.layout.panelWidth {
		t.Fatalf("initial layout paneGen=%d cols=%d, want 0 and %d", m.layoutKey.paneGen, m.vis.Cols, m.layout.panelWidth)
	}
	m.tickVisualizer(time.Unix(1, 0))
	if !m.panePruned || m.lastPrunedPaneGen != 0 {
		t.Fatalf("after first tick: pruned=%t gen=%d, want true and 0", m.panePruned, m.lastPrunedPaneGen)
	}

	if err := panes.Add(string(ui.RegionVisualizer), "extra", 1); err != nil {
		t.Fatal(err)
	}
	m.recomputeLayout()
	if m.layoutKey.paneGen != panes.Generation() {
		t.Fatalf("layout paneGen = %d, want %d", m.layoutKey.paneGen, panes.Generation())
	}
	if m.vis.Cols >= m.layout.panelWidth {
		t.Fatalf("builtin vis cols = %d, want narrowed below %d after pane add", m.vis.Cols, m.layout.panelWidth)
	}
	m.tickVisualizer(time.Unix(2, 0))
	if m.lastPrunedPaneGen != panes.Generation() {
		t.Fatalf("lastPrunedPaneGen = %d, want %d", m.lastPrunedPaneGen, panes.Generation())
	}
}

func TestWindowListFuncParityAndBoundedCalls(t *testing.T) {
	items := make([]string, 50)
	for i := range items {
		items[i] = fmt.Sprintf("Item %02d", i)
	}
	tests := []struct {
		name           string
		cursor, scroll int
		budget         int
		wantCalls      int
	}{
		{name: "top window", cursor: 2, scroll: 0, budget: 5, wantCalls: 5},
		{name: "mid window", cursor: 22, scroll: 20, budget: 6, wantCalls: 6},
		{name: "tail window padded", cursor: 49, scroll: 47, budget: 8, wantCalls: 3},
		{name: "zero budget", cursor: 0, scroll: 0, budget: 0, wantCalls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got := windowListFunc(len(items), tt.cursor, tt.scroll, tt.budget, func(i int) string {
				calls++
				return items[i]
			})
			want := windowList(items, tt.cursor, tt.scroll, tt.budget)
			if got != want {
				t.Fatalf("windowListFunc mismatch:\ngot:  %q\nwant: %q", got, want)
			}
			if calls != tt.wantCalls {
				t.Fatalf("label calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestLuaSelectPrecomputesWidthOnOpen(t *testing.T) {
	m := New(&playbackFakeEngine{}, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
	m.width, m.height = 80, 24
	m.recomputeLayout()

	updated, _ := m.Update(LuaSelectMsg{
		Title:   "Choose",
		Options: []string{"Short", "A Much Longer Option Name"},
	})
	m = updated.(Model)
	if m.luaSelect == nil || !m.luaSelect.widthComputed {
		t.Fatal("luaSelect did not precompute width on LuaSelectMsg")
	}
	if want := lipgloss.Width("A Much Longer Option Name"); m.luaSelect.maxOptionWidth != want {
		t.Fatalf("maxOptionWidth = %d, want %d", m.luaSelect.maxOptionWidth, want)
	}
	if box := m.renderLuaSelect(); !strings.Contains(ansi.Strip(box), "A Much Longer Option Name") {
		t.Fatalf("renderLuaSelect() = %q, missing longest option", ansi.Strip(box))
	}
}

func TestStereoSamplesIntoEarlyReturnPassthrough(t *testing.T) {
	engine := &perfCountingEngine{
		playbackFakeEngine: playbackFakeEngine{playing: true, mono: false, volume: 0},
	}
	m := New(engine, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
	m.vis.Mode = ui.VisStereo
	m.visVolumeLinked = false

	dst := make([][2]float64, 1)
	n := m.visualizerTickContext(time.Now()).StereoSamplesInto(dst)
	if n != 1 || dst[0] != [2]float64{0.5, -0.5} {
		t.Fatalf("StereoSamplesInto = (%d, %v), want (1, [0.5 -0.5])", n, dst[0])
	}
}

func newBenchmarkPlaybackModel(trackCount int) Model {
	pl := playlist.New()
	tracks := make([]playlist.Track, trackCount)
	for i := range trackCount {
		tracks[i] = playlist.Track{
			Path:         fmt.Sprintf("/music/album-%02d/track-%03d.flac", i/10, i),
			Title:        fmt.Sprintf("Track Title %03d", i+1),
			Artist:       fmt.Sprintf("Artist %02d", i/10+1),
			Album:        fmt.Sprintf("Album %02d", i/10+1),
			DurationSecs: 215,
			ProviderMeta: map[string]string{"id": fmt.Sprintf("track-%d", i)},
		}
	}
	pl.Replace(tracks)
	engine := &samplingFakeEngine{
		playbackFakeEngine: &playbackFakeEngine{
			playing:  true,
			seekable: true,
			position: 63 * time.Second,
			duration: 215 * time.Second,
		},
	}
	providers := []provider.Entry{
		{Key: "local", Name: "Local", Provider: &commandsTestProvider{name: "Local"}},
		{Key: "radio", Name: "Radio", Provider: &commandsTestProvider{name: "Radio"}},
	}
	m := New(engine, pl, providers, "local", nil, nil, nil, nil, nil, nil)
	m.SetPaneSet(ui.NewPaneSet())
	m.SetIPCBroker(ipc.NewBroker())
	m.termTitle.introActive = false
	m.vis.Mode = ui.VisBars
	m.width, m.height = 120, 40
	m.recomputeLayout()
	m.setPlaybackTrack(tracks[0])
	m.tickSampleClock()
	m.tickVisualizer(time.Unix(1, 0))
	return m
}

func BenchmarkModelView(b *testing.B) {
	b.Run("two_column_200_tracks", func(b *testing.B) {
		m := newBenchmarkPlaybackModel(200)
		// Warm caches before measuring steady-state frame rendering.
		_ = m.View()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = m.View()
		}
	})

	b.Run("pure_full_vis", func(b *testing.B) {
		m := newBenchmarkPlaybackModel(200)
		m.fullVis = true
		m.pureVis = true
		m.recomputeLayout()
		m.tickVisualizer(time.Unix(2, 0))
		_ = m.View()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = m.View()
		}
	})
}

func BenchmarkModelUpdateTick(b *testing.B) {
	m := newBenchmarkPlaybackModel(200)
	now := time.Unix(10, 0)
	// Warm initial IPC state and layout.
	next, _ := m.Update(tickMsg(now))
	m = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ = m.Update(tickMsg(now.Add(time.Duration(i+1) * ui.TickFast)))
		m = next.(Model)
	}
}
