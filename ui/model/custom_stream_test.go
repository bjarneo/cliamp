package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// customStreamTestProvider claims spotify: URIs like the Spotify provider.
type customStreamTestProvider struct {
	commandsTestProvider
}

func (customStreamTestProvider) URISchemes() []string { return []string{"spotify:"} }

func (customStreamTestProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, errors.New("not used")
}

func (customStreamTestProvider) Authenticate() error { return nil }

// sourceResolverEngine reports a play-time source resolver for each prefix,
// as player.Player does after main registers the Qobuz and Tidal resolvers.
type sourceResolverEngine struct {
	*playbackFakeEngine
	prefixes []string
}

func (e sourceResolverEngine) HasSourceResolver(path string) bool {
	for _, prefix := range e.prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func newCustomStreamModel(player *playbackFakeEngine) Model {
	prov := customStreamTestProvider{commandsTestProvider{name: "Spotify"}}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Song", Path: "spotify:track:abc", DurationSecs: 200},
		{Title: "Local", Path: "local.mp3", DurationSecs: 100},
	})
	p.SetIndex(0)
	m := Model{
		player:    player,
		playlist:  p,
		provider:  prov,
		providers: []provider.Entry{{Key: "spotify", Name: "Spotify", Provider: prov}},
		vis:       ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.SetVisualizer("none")
	return m
}

// streamPlayedFrom runs cmd and returns the streamPlayedMsg it produces.
func streamPlayedFrom(t *testing.T, cmd tea.Cmd) streamPlayedMsg {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case streamPlayedMsg:
			return msg
		case tea.BatchMsg:
			pending = append(pending, msg...)
		}
	}
	t.Fatal("command produced no streamPlayedMsg")
	return streamPlayedMsg{}
}

// TestPlayTrackStartsSlowSourcesOffUpdate checks that a start that can wait
// on the network or on ffmpeg runs in a command, and a native local file
// starts in Update.
func TestPlayTrackStartsSlowSourcesOffUpdate(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantAsync bool
	}{
		{name: "spotify track", path: "spotify:track:abc", wantAsync: true},
		// A file that an older version wrote reloads a qobuz:// track
		// without the stream flag. The resolver still opens it over the
		// network.
		{name: "qobuz track from a saved list", path: "qobuz://track/42", wantAsync: true},
		// The player routes a yt-dlp page to the yt-dlp chain inside the
		// same start as every other source.
		{name: "yt-dlp page", path: "https://www.youtube.com/watch?v=abc", wantAsync: true},
		// ffmpeg decodes an m4a file after an ffprobe run, which a slow
		// mount can stall.
		{name: "local ffmpeg format", path: "/music/local.m4a", wantAsync: true},
		{name: "local file", path: "local.mp3", wantAsync: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{}
			m := newCustomStreamModel(player)
			m.player = sourceResolverEngine{player, []string{"qobuz://track/"}}

			cmd := m.playTrack(playlist.Track{Title: "Song", Path: tt.path, DurationSecs: 200})

			if m.buffering != tt.wantAsync {
				t.Fatalf("buffering = %v, want %v", m.buffering, tt.wantAsync)
			}
			if gotSync := len(player.playCalls) > 0; gotSync == tt.wantAsync {
				t.Fatalf("play calls inside playTrack = %v, want sync start %v", player.playCalls, !tt.wantAsync)
			}
			if !tt.wantAsync {
				return
			}

			msg := streamPlayedFrom(t, cmd)
			if msg.path != tt.path || len(player.playCalls) != 1 {
				t.Fatalf("async start = %+v, play calls = %v", msg, player.playCalls)
			}
			updated, _ := m.Update(msg)
			m = updated.(Model)
			if m.buffering || m.err != nil {
				t.Fatalf("after start: buffering = %v, err = %v", m.buffering, m.err)
			}
		})
	}
}

// TestPlayTrackResumeSeeksOnlyWhenStartMissedHint checks that a start at the
// resume hint spends the hint without a second seek, that a yt-dlp page,
// which starts at 0, resumes through a seek command, and that a native local
// file whose start seek failed seeks in place.
func TestPlayTrackResumeSeeksOnlyWhenStartMissedHint(t *testing.T) {
	tests := []struct {
		name           string
		track          playlist.Track
		ytdl           bool          // the player starts the page at 0 and seeks by restart
		startMissed    bool          // the start seek failed, so the file plays from 0
		lag            time.Duration // how far the track plays on before Update takes the start
		wantAsync      bool
		wantSeeks      []time.Duration // Seek calls in Update
		wantResumeSeek bool
	}{
		// ffmpeg already starts at the hint, and a second seek would start
		// a new ffmpeg in Update.
		{name: "local ffmpeg file", track: playlist.Track{Title: "Book", Path: "/books/book.m4b"}, lag: 200 * time.Millisecond, wantAsync: true},
		// The decoder plays on past the hint while the start message
		// waits, and that is no reason to seek back.
		{name: "local ffmpeg file, late start message", track: playlist.Track{Title: "Book", Path: "/books/book.m4b"}, lag: 2 * time.Second, wantAsync: true},
		{name: "yt-dlp page", track: playlist.Track{Title: "Show", Path: "https://www.mixcloud.com/creator/show/", Stream: true}, ytdl: true, lag: 200 * time.Millisecond, wantAsync: true, wantResumeSeek: true},
		{name: "native local file", track: playlist.Track{Title: "Song", Path: "/music/song.mp3"}},
		{name: "native local file, start seek failed", track: playlist.Track{Title: "Song", Path: "/music/song.mp3"}, startMissed: true, wantSeeks: []time.Duration{10 * time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{seekable: true, ytdlSeek: tt.ytdl, startsAtOffset: !tt.ytdl && !tt.startMissed, duration: time.Hour}
			m := newCustomStreamModel(player)
			m.SetResume(tt.track.Path, 600)

			cmd := m.playTrack(tt.track)
			if tt.wantAsync {
				msg := streamPlayedFrom(t, cmd)
				// The track plays on while the start message waits.
				player.position += tt.lag
				updated, next := m.Update(msg)
				m = updated.(Model)
				cmd = next
			}

			if len(player.playAtOffsets) != 1 || player.playAtOffsets[0] != 10*time.Minute {
				t.Fatalf("PlayAt offsets = %v, want [10m0s]", player.playAtOffsets)
			}
			if !slices.Equal(player.seekCalls, tt.wantSeeks) {
				t.Fatalf("Seek calls in Update = %v, want %v", player.seekCalls, tt.wantSeeks)
			}
			if !tt.wantResumeSeek {
				if m.resume.path != "" || m.resume.secs != 0 {
					t.Fatalf("resume = %+v, want the hint cleared", m.resume)
				}
				return
			}
			if !m.seek.active {
				t.Fatal("seek.active = false, want a pending resume seek")
			}
			msg := runSeekCmd(t, cmd)
			if !msg.resume || msg.target != 10*time.Minute {
				t.Fatalf("resume seek = %+v, want a resume at 10m0s", msg)
			}
		})
	}
}

func TestPreloadNextWaitsForLeadTimeOnSourceResolverURI(t *testing.T) {
	tests := []struct {
		name        string
		position    time.Duration
		wantPreload bool
	}{
		{name: "early in the track", position: 10 * time.Second, wantPreload: false},
		{name: "inside the lead time", position: 58 * time.Second, wantPreload: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{playing: true, duration: time.Minute, position: tt.position}
			p := playlist.New()
			p.Replace([]playlist.Track{
				{Title: "Current", Path: "qobuz://track/1", DurationSecs: 60},
				{Title: "Next", Path: "qobuz://track/2", DurationSecs: 60},
			})
			p.SetIndex(0)
			m := Model{player: sourceResolverEngine{player, []string{"qobuz://track/"}}, playlist: p}

			cmd := m.preloadNext()
			if gotPreload := cmd != nil; gotPreload != tt.wantPreload {
				t.Fatalf("preloadNext() command = %v, want %v", gotPreload, tt.wantPreload)
			}
			if len(player.preloadCalls) != 0 {
				t.Fatalf("preloadCalls inside preloadNext = %v, want none", player.preloadCalls)
			}
		})
	}
}

func TestStreamPlayedNeedsAuthAsksForSignIn(t *testing.T) {
	player := &playbackFakeEngine{}
	m := newCustomStreamModel(player)
	track, _ := m.playlist.Current()
	m.playTrack(track)

	updated, _ := m.Update(streamPlayedMsg{
		path: track.Path,
		gen:  m.requests.stream,
		err:  fmt.Errorf("spotify: stream auth error: %w", playlist.ErrNeedsAuth),
	})
	m = updated.(Model)

	if !m.provPane.signIn {
		t.Fatal("provPane.signIn = false, want the sign-in prompt")
	}
	if m.err != nil {
		t.Fatalf("err = %v, want nil so the prompt is not hidden", m.err)
	}
	if !strings.Contains(m.status.text, "Sign-in required") {
		t.Fatalf("status = %q, want a sign-in hint", m.status.text)
	}
}

// TestStreamPlayedFailureStatus checks that a failed start shows the gated
// hint only for a source that can gate a track.
func TestStreamPlayedFailureStatus(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantGated bool
	}{
		{name: "yt-dlp page", path: "https://www.youtube.com/watch?v=abc", wantGated: true},
		// A local file fails to start, for example, when ffmpeg is missing.
		{name: "local ffmpeg format", path: "/music/local.m4a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{playErr: errors.New("decode: ffmpeg is required")}
			m := newCustomStreamModel(player)

			msg := streamPlayedFrom(t, m.playTrack(playlist.Track{Title: "Song", Path: tt.path}))
			updated, _ := m.Update(msg)
			m = updated.(Model)

			if m.err == nil {
				t.Fatal("err = nil, want the start error")
			}
			if gotGated := strings.Contains(m.status.text, "gated"); gotGated != tt.wantGated {
				t.Fatalf("status = %q, want gated hint %v", m.status.text, tt.wantGated)
			}
		})
	}
}

func TestProviderAuthFailureKeepsSignInPrompt(t *testing.T) {
	m := newCustomStreamModel(&playbackFakeEngine{})
	gen := nextRequest(&m.requests.auth)

	updated, _ := m.Update(provAuthDoneMsg{providerName: "Spotify", gen: gen, err: errors.New("access_denied")})
	m = updated.(Model)
	if !m.provPane.signIn || m.err == nil {
		t.Fatalf("after failure: provPane.signIn = %v, err = %v, want prompt and error", m.provPane.signIn, m.err)
	}

	// Enter retries the sign-in and clears the old error.
	m.focus = focusProvider
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the sign-in prompt returned no command")
	}
	if m.provPane.signIn || !m.provPane.loading || m.err != nil {
		t.Fatalf("after retry: provPane.signIn = %v, provPane.loading = %v, err = %v", m.provPane.signIn, m.provPane.loading, m.err)
	}
}

// Favorites, Recently Played and saved playlists keep provider tracks that
// open over the network at play time, such as qobuz:// tracks. A reloaded
// track must start and seek off the Update goroutine. This also holds for a
// file that an older version wrote without the stream key.
func TestReloadedResolverTracksStayOffUpdate(t *testing.T) {
	prefixes := []string{"qobuz://track/", "tidal://track/", "yandex:track:", "lyrion://track/"}
	stores := []struct {
		name string
		// reload saves track, lets strip edit the file and loads the track back.
		reload func(t *testing.T, track playlist.Track, strip func(path string)) playlist.Track
	}{
		{name: "favorites", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			path := filepath.Join(t.TempDir(), "favorites.toml")
			if _, err := favorites.NewAt(path).Toggle(track); err != nil {
				t.Fatal(err)
			}
			strip(path)
			tracks, err := favorites.NewAt(path).Tracks()
			if err != nil || len(tracks) != 1 {
				t.Fatalf("favorites = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
		{name: "history", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			path := filepath.Join(t.TempDir(), "history.toml")
			if err := history.NewAt(path).Record(track, time.Now()); err != nil {
				t.Fatal(err)
			}
			strip(path)
			tracks, err := history.NewAt(path).Tracks(0)
			if err != nil || len(tracks) != 1 {
				t.Fatalf("history = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
		{name: "saved playlist", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := local.New(nil, nil).SavePlaylist("Mix", []playlist.Track{track}); err != nil {
				t.Fatal(err)
			}
			strip(filepath.Join(dir, "playlists", "Mix.toml"))
			tracks, err := local.New(nil, nil).Tracks("Mix")
			if err != nil || len(tracks) != 1 {
				t.Fatalf("Mix = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
	}
	// Each action runs on the Model after the async start and returns the
	// command of the seek. It must not call the player in Update.
	resume := func(m *Model, played streamPlayedMsg) tea.Cmd {
		updated, cmd := m.Update(played)
		*m = updated.(Model)
		return cmd
	}
	actions := []struct {
		name    string
		resume  bool  // the startup resume hint names the track
		seekErr error // the seek command fails with this error
		act     func(m *Model, played streamPlayedMsg) tea.Cmd
	}{
		{name: "seek", act: func(m *Model, _ streamPlayedMsg) tea.Cmd { return m.seekAbsolute(30 * time.Second) }},
		{name: "resume", resume: true, act: resume},
		{name: "failed resume", resume: true, seekErr: errors.New("connection reset"), act: resume},
	}
	keep := func(string) {}
	for _, store := range stores {
		for _, legacy := range []bool{false, true} {
			for _, prefix := range prefixes {
				for _, action := range actions {
					name := fmt.Sprintf("%s/%s/legacy=%v/%s", store.name, prefix, legacy, action.name)
					t.Run(name, func(t *testing.T) {
						strip := keep
						if legacy {
							strip = func(path string) {
								data, err := os.ReadFile(path)
								if err != nil {
									t.Fatal(err)
								}
								if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "stream = true\n", "")), 0o644); err != nil {
									t.Fatal(err)
								}
							}
						}
						saved := playlist.Track{Path: prefix + "1", Title: "Song", Stream: true, DurationSecs: 200}
						track := store.reload(t, saved, strip)
						if track.Stream == legacy {
							t.Fatalf("reloaded Stream = %v, want %v", track.Stream, !legacy)
						}

						player := &playbackFakeEngine{seekable: true, duration: 200 * time.Second}
						m := newCustomStreamModel(player)
						m.player = sourceResolverEngine{player, prefixes}
						m.playlist.Replace([]playlist.Track{track})
						m.playlist.SetIndex(0)
						if action.resume {
							m.SetResume(track.Path, 30)
						}

						cmd := m.playTrack(track)
						if len(player.playCalls) != 0 || !m.buffering {
							t.Fatalf("playTrack opened the track in Update: play calls %v, buffering %v", player.playCalls, m.buffering)
						}
						played := streamPlayedFrom(t, cmd)
						if played.path != track.Path {
							t.Fatalf("async start = %+v, want %s", played, track.Path)
						}

						seek := action.act(&m, played)
						if len(player.seekCalls) != 0 || seek == nil {
							t.Fatalf("the seek ran in Update: seek calls %v, command %v", player.seekCalls, seek != nil)
						}
						if action.resume && (!m.seek.inFlight || m.seek.targetPos != 30*time.Second) {
							t.Fatalf("resume seek in flight %v to %v, want an async seek to 30s", m.seek.inFlight, m.seek.targetPos)
						}
						if action.seekErr == nil {
							return
						}
						// A failed resume names the track, which can be music
						// rather than a show, and spends the resume hint.
						player.seekErr = action.seekErr
						for _, msg := range runCmd(seek) {
							updated, _ := m.Update(msg)
							m = updated.(Model)
						}
						want := "Couldn't resume this track; playing from the previous position: connection reset"
						if m.status.text != want || m.resume.path != "" || m.resume.secs != 0 {
							t.Fatalf("after the failed resume: status %q, resume hint %q/%ds; want %q and no hint", m.status.text, m.resume.path, m.resume.secs, want)
						}
					})
				}
			}
		}
	}
}
