package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// TestResolveTrackDisplayStreamTitle covers the ICY override a radio stream
// needs: its playlist entry only knows the station name. Audiobookshelf tracks
// are also Stream: true, so the no-metadata case matters for them too --
// streamTitle is cleared on every track change, and their own title must stand.
func TestResolveTrackDisplayStreamTitle(t *testing.T) {
	tests := []struct {
		name        string
		streamTitle string
		track       playlist.Track
		wantArtist  string
		wantTitle   string
	}{
		{
			name:        "icy artist and title split on separator",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "NCS Trap Stream", Stream: true},
			wantArtist:  "Tycho",
			wantTitle:   "Awake",
		},
		{
			name:        "icy value without separator becomes the title",
			streamTitle: "Morning Session",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Morning Session",
		},
		{
			// "Artist - " cuts to an empty title; keep the stored one rather than
			// blanking it or showing the broken tag.
			name:        "icy value with an empty title keeps the station name",
			streamTitle: "Tycho - ",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
		},
		{
			name:        "icy value with an empty artist keeps the station name",
			streamTitle: " - Awake",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
		},
		{
			name:        "icy parts are trimmed",
			streamTitle: "Tycho  -  Awake ",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantArtist:  "Tycho",
			wantTitle:   "Awake",
		},
		{
			name:      "no icy metadata keeps the station name",
			track:     playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle: "Lofi Stream",
		},
		{
			// Library providers (Navidrome, Plex, Jellyfin, Emby, Qobuz, NetEase,
			// Audiobookshelf) are Stream: true too, so they take the same branch
			// as radio and must keep their own title and author.
			name: "audiobook stream without icy keeps its own metadata",
			track: playlist.Track{
				Title:  "Tortilla Face",
				Artist: "Bobby Lee & Andrew Santino",
				Album:  "Bad Friends",
				Stream: true,
			},
			wantArtist: "Bobby Lee & Andrew Santino",
			wantTitle:  "Tortilla Face",
		},
		{
			// A stale ICY title must never bleed onto a non-stream track.
			name:        "spotify track ignores a leftover stream title",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "Alien Boy", Artist: "Oliver Tree"},
			wantArtist:  "Oliver Tree",
			wantTitle:   "Alien Boy",
		},
		{
			// " - " in a real track title is not an ICY separator to split on.
			name:       "spotify remix title keeps its separator",
			track:      playlist.Track{Title: "Counting - Taiki Nulight Remix", Artist: "Hamdi"},
			wantArtist: "Hamdi",
			wantTitle:  "Counting - Taiki Nulight Remix",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{streamTitle: tc.streamTitle}
			artist, title := m.resolveTrackDisplay(tc.track)
			if artist != tc.wantArtist || title != tc.wantTitle {
				t.Errorf("resolveTrackDisplay() = (%q, %q), want (%q, %q)",
					artist, title, tc.wantArtist, tc.wantTitle)
			}
		})
	}
}

// Every surface splits an ICY stream title with one rule: both parts are
// trimmed, and both must be set.
func TestSplitStreamTitle(t *testing.T) {
	tests := []struct {
		in                    string
		wantArtist, wantTitle string
		wantOK                bool
	}{
		{"Tycho - Awake", "Tycho", "Awake", true},
		{" Tycho  -  Awake ", "Tycho", "Awake", true},
		{"Tycho - Awake - Live", "Tycho", "Awake - Live", true},
		{"Tycho - ", "", "", false},
		{" - Awake", "", "", false},
		{"   -   ", "", "", false},
		{"Morning Session", "", "", false},
		{"Tycho-Awake", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			artist, title, ok := splitStreamTitle(tt.in)
			if artist != tt.wantArtist || title != tt.wantTitle || ok != tt.wantOK {
				t.Fatalf("splitStreamTitle(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.in, artist, title, ok, tt.wantArtist, tt.wantTitle, tt.wantOK)
			}
		})
	}
}

// The lyrics lookup, the media controls and the terminal title read the same
// artist and title from one stream title.
func TestStreamTitleSurfacesAgree(t *testing.T) {
	track := playlist.Track{Title: "Lofi Stream", Stream: true, Path: "https://radio.example/stream"}
	for _, streamTitle := range []string{"Tycho - Awake", " Tycho  -  Awake ", "Tycho - ", " - Awake", "Morning Session"} {
		t.Run(streamTitle, func(t *testing.T) {
			m := Model{
				streamTitle:        streamTitle,
				player:             &playbackFakeEngine{playing: true},
				playingTrack:       track,
				playingTrackActive: true,
			}
			displayArtist, displayTitle := m.resolveTrackDisplay(track)
			terminal := terminalTitleValuesForTrack(track, streamTitle, true, false)
			if terminal.artist != displayArtist || terminal.title != displayTitle {
				t.Fatalf("terminal title (%q, %q), media controls (%q, %q), want the same",
					terminal.artist, terminal.title, displayArtist, displayTitle)
			}
			artist, title, ok := splitStreamTitle(streamTitle)
			lyricsArtist, lyricsTitle := m.lyricsArtistTitle()
			if ok && (lyricsArtist != artist || lyricsTitle != title) {
				t.Fatalf("lyrics query (%q, %q), want the split (%q, %q)", lyricsArtist, lyricsTitle, artist, title)
			}
			if !ok && (lyricsArtist != track.Artist || lyricsTitle != track.Title) {
				t.Fatalf("lyrics query (%q, %q), want the track (%q, %q)", lyricsArtist, lyricsTitle, track.Artist, track.Title)
			}
		})
	}
}

// TestIPCTrackInfoKeepsAlbum pins the show/book name a podcast or audiobook
// carries in Album: the stream branch rewrites Title and Artist, so Album has to
// come through ipcTrackInfo untouched for a client to render "Bad Friends".
func TestIPCTrackInfoKeepsAlbum(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
	}{
		{
			name: "audiobook keeps the show name",
			track: playlist.Track{
				Title:  "Tortilla Face",
				Artist: "Bobby Lee & Andrew Santino",
				Album:  "Bad Friends",
				Path:   "http://abs.example/api/items/x/file/y",
				Stream: true,
			},
		},
		{
			name: "spotify keeps the album",
			track: playlist.Track{
				Title:  "Alien Boy",
				Artist: "Oliver Tree",
				Album:  "Ugly is Beautiful",
				Path:   "spotify:track:abc123",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := ipcTrackInfo(tc.track, 0, 0, false)
			for _, f := range []struct{ field, got, want string }{
				{"Title", info.Title, tc.track.Title},
				{"Artist", info.Artist, tc.track.Artist},
				{"Album", info.Album, tc.track.Album},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}

// A plugin override sits between the two title sources: it wins over a resolver
// title and yields to inline ICY metadata. A plugin can update or clear it.
func TestPluginStreamTitle(t *testing.T) {
	station := playlist.Track{Title: "Station", Path: "https://radio.example.com/live", Stream: true}
	song := playlist.Track{Title: "Song", Path: "song.mp3"}
	tests := []struct {
		name       string
		track      playlist.Track
		engine     playbackFakeEngine // playing unless stopped is set
		stopped    bool
		titles     []string // sent to the handler in order
		tick       func(*playbackFakeEngine)
		wantShown  string
		wantPlugin string
	}{
		{
			name:       "a stream without metadata takes the override",
			track:      station,
			titles:     []string{"Artist - One"},
			wantShown:  "Artist - One",
			wantPlugin: "Artist - One",
		},
		{
			name:       "a plugin updates its override",
			track:      station,
			titles:     []string{"Artist - One", "Artist - Two"},
			wantShown:  "Artist - Two",
			wantPlugin: "Artist - Two",
		},
		{
			name:   "a plugin clears its override",
			track:  station,
			titles: []string{"Artist - One", ""},
		},
		{
			name:       "the override wins over a resolver title",
			track:      station,
			engine:     playbackFakeEngine{streamTitle: "Resolver - Song"},
			titles:     []string{"Artist - One"},
			wantShown:  "Artist - One",
			wantPlugin: "Artist - One",
		},
		{
			name:      "clearing the override shows the resolver title",
			track:     station,
			engine:    playbackFakeEngine{streamTitle: "Resolver - Song"},
			titles:    []string{"Artist - One", ""},
			wantShown: "Resolver - Song",
		},
		{
			name:      "inline ICY metadata refuses an override",
			track:     station,
			engine:    playbackFakeEngine{streamTitle: "Icy - Song", streamTitleICY: true},
			titles:    []string{"Artist - One"},
			wantShown: "Icy - Song",
		},
		{
			name:   "later ICY metadata replaces the override",
			track:  station,
			titles: []string{"Artist - One"},
			tick: func(e *playbackFakeEngine) {
				e.streamTitle, e.streamTitleICY = "Icy - Song", true
			},
			wantShown: "Icy - Song",
		},
		{
			name:       "a later resolver title keeps the override",
			track:      station,
			titles:     []string{"Artist - One"},
			tick:       func(e *playbackFakeEngine) { e.streamTitle = "Resolver - Song" },
			wantShown:  "Artist - One",
			wantPlugin: "Artist - One",
		},
		{
			name:    "a stopped player ignores the override",
			track:   station,
			stopped: true,
			titles:  []string{"Artist - One"},
		},
		{
			name:   "a non-stream ignores the override",
			track:  song,
			titles: []string{"Artist - One"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			engine.playing = !tt.stopped
			m := newPluginStateModel(&engine, tt.track)
			m.setPlaybackTrack(tt.track)
			m.tickStreamTitle()
			for _, title := range tt.titles {
				m.handlePluginStreamTitle(PluginStreamTitleMsg{Title: title})
			}
			if tt.tick != nil {
				tt.tick(&engine)
				m.tickStreamTitle()
			}
			if got := m.effectiveStreamTitle(); got != tt.wantShown {
				t.Errorf("effectiveStreamTitle() = %q, want %q", got, tt.wantShown)
			}
			if m.pluginStreamTitle != tt.wantPlugin {
				t.Errorf("pluginStreamTitle = %q, want %q", m.pluginStreamTitle, tt.wantPlugin)
			}
		})
	}
}

// Stopping forgets the override, so it cannot show on the stopped view or on
// the next stream.
func TestStopPlaybackClearsPluginStreamTitle(t *testing.T) {
	station := playlist.Track{Title: "Station", Path: "https://radio.example.com/live", Stream: true}
	engine := &playbackFakeEngine{playing: true}
	m := newPluginStateModel(engine, station)
	m.setPlaybackTrack(station)
	m.handlePluginStreamTitle(PluginStreamTitleMsg{Title: "Artist - One"})
	if m.pluginStreamTitle == "" {
		t.Fatal("override not set before the stop")
	}
	m.stopPlayback()
	if m.pluginStreamTitle != "" {
		t.Errorf("pluginStreamTitle after stop = %q, want empty", m.pluginStreamTitle)
	}
}

// cliamp.player.stream_title() reads the raw title, so a plugin can tell its
// own override from the station's metadata. The track fields show the override.
func TestPluginStateStreamTitleIsRaw(t *testing.T) {
	station := playlist.Track{Title: "Station", Path: "https://radio.example.com/live", Stream: true}
	m := newPluginStateModel(&playbackFakeEngine{playing: true, streamTitle: "Resolver - Song"}, station)
	m.setPlaybackTrack(station)
	m.tickStreamTitle()
	m.handlePluginStreamTitle(PluginStreamTitleMsg{Title: "Artist - One"})
	m.publishPluginState()
	state := m.PluginStateLoader()()
	if state.StreamTitle != "Resolver - Song" {
		t.Errorf("StreamTitle = %q, want the raw title", state.StreamTitle)
	}
	if state.Track.Artist != "Artist" || state.Track.Title != "One" {
		t.Errorf("Track = (%q, %q), want the override", state.Track.Artist, state.Track.Title)
	}
}
