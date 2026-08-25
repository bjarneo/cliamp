package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type playbackFakeEngine struct {
	playing           bool
	gaplessAdvanced   bool
	drained           bool
	paused            bool
	ytdlSeek          bool
	live              bool
	seekable          bool
	position          time.Duration
	duration          time.Duration
	seekYTDLErr       error
	playCalls         []string
	seekYTDLCalls     []time.Duration
	playAtOffsets     []time.Duration
	preloadCalls      []string
	clearPreloadCalls int
	stopCalls         int
	eqBands           [eqBandCount]float64
}

func (f *playbackFakeEngine) Play(path string, _ time.Duration) error {
	f.playing = true
	f.paused = false
	f.playCalls = append(f.playCalls, path)
	return nil
}
func (f *playbackFakeEngine) PlayAt(path string, dur, offset time.Duration) error {
	f.playAtOffsets = append(f.playAtOffsets, offset)
	return f.Play(path, dur)
}
func (f *playbackFakeEngine) PlayYTDL(string, time.Duration) error { return nil }
func (f *playbackFakeEngine) Preload(path string, _ time.Duration) error {
	f.preloadCalls = append(f.preloadCalls, path)
	return nil
}
func (f *playbackFakeEngine) PreloadYTDL(string, time.Duration) error { return nil }
func (f *playbackFakeEngine) ClearPreload()                           { f.clearPreloadCalls++ }
func (f *playbackFakeEngine) Stop() {
	f.stopCalls++
	f.playing, f.paused = false, false
}
func (f *playbackFakeEngine) Close()                   {}
func (f *playbackFakeEngine) TogglePause()             { f.paused = !f.paused }
func (f *playbackFakeEngine) Seek(time.Duration) error { return nil }
func (f *playbackFakeEngine) SeekYTDL(d time.Duration) error {
	f.seekYTDLCalls = append(f.seekYTDLCalls, d)
	return f.seekYTDLErr
}
func (f *playbackFakeEngine) CancelSeekYTDL()    {}
func (f *playbackFakeEngine) IsPlaying() bool    { return f.playing }
func (f *playbackFakeEngine) IsPaused() bool     { return f.paused }
func (f *playbackFakeEngine) Drained() bool      { return f.drained }
func (f *playbackFakeEngine) HasPreload() bool   { return false }
func (f *playbackFakeEngine) Seekable() bool     { return f.seekable }
func (f *playbackFakeEngine) IsStreamSeek() bool { return false }
func (f *playbackFakeEngine) IsYTDLSeek() bool   { return f.ytdlSeek }
func (f *playbackFakeEngine) IsLiveStream() bool { return f.live }
func (f *playbackFakeEngine) GaplessAdvanced() bool {
	if !f.gaplessAdvanced {
		return false
	}
	f.gaplessAdvanced = false
	return true
}
func (f *playbackFakeEngine) Position() time.Duration { return f.position }
func (f *playbackFakeEngine) Duration() time.Duration { return f.duration }
func (f *playbackFakeEngine) PositionAndDuration() (time.Duration, time.Duration) {
	return f.position, f.duration
}
func (f *playbackFakeEngine) SetVolumeMin(float64)                   {}
func (f *playbackFakeEngine) VolumeMin() float64                     { return -50 }
func (f *playbackFakeEngine) SetVolume(float64)                      {}
func (f *playbackFakeEngine) Volume() float64                        { return 0 }
func (f *playbackFakeEngine) SetSpeed(float64)                       {}
func (f *playbackFakeEngine) Speed() float64                         { return 1 }
func (f *playbackFakeEngine) ToggleMono()                            {}
func (f *playbackFakeEngine) Mono() bool                             { return false }
func (f *playbackFakeEngine) SetEQBand(band int, gain float64)       { f.eqBands[band] = gain }
func (f *playbackFakeEngine) EQBands() [10]float64                   { return f.eqBands }
func (f *playbackFakeEngine) StreamErr() error                       { return nil }
func (f *playbackFakeEngine) StreamTitle() string                    { return "" }
func (f *playbackFakeEngine) StreamBytes() (downloaded, total int64) { return 0, 0 }
func (f *playbackFakeEngine) SamplesInto([]float64) int              { return 0 }
func (f *playbackFakeEngine) WaveformSamplesInto([]float64) int      { return 0 }
func (f *playbackFakeEngine) StereoSamplesInto([][2]float64) int     { return 0 }
func (f *playbackFakeEngine) SampleRate() int                        { return 44100 }

func TestApplyResumeRestartsMixcloudAtSavedPosition(t *testing.T) {
	track := playlist.Track{
		Title:        "Saved show",
		Path:         "https://www.mixcloud.com/creator/saved-show/",
		Stream:       true,
		DurationSecs: 3600,
	}
	player := &playbackFakeEngine{
		playing:  true,
		ytdlSeek: true,
		seekable: true,
		position: 5 * time.Second,
		duration: time.Hour,
	}
	m := Model{player: player, playlist: playlist.New(), playingTrack: track, playingTrackActive: true}
	m.SetResume(track.Path, 90)

	cmd := m.applyResume()
	if cmd == nil {
		t.Fatal("applyResume() = nil, want asynchronous Mixcloud seek")
	}
	msg, ok := cmd().(seekTickMsg)
	if !ok {
		t.Fatalf("resume command message = %T, want seekTickMsg", msg)
	}
	if msg.err != nil || !msg.resume || msg.target != 90*time.Second {
		t.Fatalf("resume message = %+v, want successful resume at 1m30s", msg)
	}
	if len(player.seekYTDLCalls) != 1 || player.seekYTDLCalls[0] != 85*time.Second {
		t.Fatalf("SeekYTDL calls = %v, want [1m25s]", player.seekYTDLCalls)
	}

	updated, _ := m.Update(msg)
	got := updated.(Model)
	if got.resume.path != "" || got.resume.secs != 0 {
		t.Fatalf("resume state was not cleared after success: %+v", got.resume)
	}
}

func TestStreamPlayedNotifiesOnceWithoutResume(t *testing.T) {
	track := playlist.Track{Title: "Show", Path: "https://www.mixcloud.com/creator/show/", Stream: true}
	pl := playlist.New()
	pl.Add(track)
	notifier := &fakeNotifier{}
	m := Model{
		player:    &playbackFakeEngine{playing: true},
		playlist:  pl,
		notifier:  notifier,
		buffering: true,
	}
	m.requests.stream = 1

	updated, _ := m.Update(streamPlayedMsg{path: track.Path, gen: 1})
	m = updated.(Model)
	if len(notifier.updates) != 1 {
		t.Fatalf("notifier updates = %d, want exactly 1", len(notifier.updates))
	}
}

func TestStreamPlayedResumeKeepsNextTrackPreload(t *testing.T) {
	current := playlist.Track{
		Title: "Saved show", Path: "https://www.mixcloud.com/creator/saved-show/", Stream: true, DurationSecs: 3600,
	}
	next := playlist.Track{Title: "Next", Path: "/tmp/next.mp3", DurationSecs: 180}
	pl := playlist.New()
	pl.Add(current, next)
	player := &playbackFakeEngine{playing: true, ytdlSeek: true, seekable: true, duration: time.Hour}
	notifier := &fakeNotifier{}
	m := Model{player: player, playlist: pl, notifier: notifier, buffering: true}
	m.SetResume(current.Path, 90)
	m.requests.stream = 1

	updated, cmd := m.Update(streamPlayedMsg{path: current.Path, gen: 1})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("streamPlayedMsg command = nil, want resume and preload batch")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("streamPlayedMsg command = %T len=%d, want two-command batch", batch, len(batch))
	}
	if !m.preloading {
		t.Fatal("next track preload was not armed while resuming")
	}
	if len(notifier.updates) != 1 {
		t.Fatalf("notifier updates = %d, want exactly 1", len(notifier.updates))
	}
}

func TestApplyResumeFailureKeepsMixcloudPlayable(t *testing.T) {
	track := playlist.Track{
		Title:        "Saved show",
		Path:         "https://www.mixcloud.com/creator/saved-show/",
		Stream:       true,
		DurationSecs: 3600,
	}
	player := &playbackFakeEngine{
		playing:     true,
		ytdlSeek:    true,
		seekable:    true,
		duration:    time.Hour,
		seekYTDLErr: errors.New("seek not permitted"),
	}
	m := Model{player: player, playlist: playlist.New(), playingTrack: track, playingTrackActive: true}
	m.SetResume(track.Path, 90)

	msg := m.applyResume()().(seekTickMsg)
	updated, _ := m.Update(msg)
	got := updated.(Model)
	if got.resume.path != "" || got.resume.secs != 0 {
		t.Fatalf("failed resume remained armed: %+v", got.resume)
	}
	if !strings.Contains(got.status.text, "playing from the previous position") {
		t.Fatalf("status = %q, want fallback explanation", got.status.text)
	}
}

func TestQuitCapturesMixcloudResumePosition(t *testing.T) {
	track := playlist.Track{
		Title:        "Partly played show",
		Path:         "https://www.mixcloud.com/creator/partly-played/",
		Stream:       true,
		DurationSecs: 3600,
	}
	player := &playbackFakeEngine{playing: true, position: 12*time.Minute + 34*time.Second}
	m := Model{player: player, playingTrack: track, playingTrackActive: true}

	m.quit()
	if m.exitResume.path != track.Path || m.exitResume.secs != 754 {
		t.Fatalf("exit resume = (%q, %d), want (%q, 754)", m.exitResume.path, m.exitResume.secs, track.Path)
	}
}

func TestNavTrackListQueueStartsQueuedTrackWhenStopped(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Existing", Path: "https://example.com/existing", Stream: true},
		{Title: "Other", Path: "https://example.com/other", Stream: true},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		navBrowser: navBrowserState{
			tracks: []playlist.Track{
				{Title: "Queued", Path: "https://example.com/queued", Stream: true},
			},
		},
	}

	cmd := m.handleNavTrackListKey(tea.KeyPressMsg{Text: "q"})
	if cmd == nil {
		t.Fatal("handleNavTrackListKey(q) = nil, want command")
	}
	if current, idx := m.playlist.Current(); current.Title != "Queued" || idx != 2 {
		t.Fatalf("current = (%q,%d), want (\"Queued\",2)", current.Title, idx)
	}
	if m.plCursor != 2 {
		t.Fatalf("plCursor = %d, want 2", m.plCursor)
	}
	if p.QueueLen() != 0 {
		t.Fatalf("QueueLen() = %d, want 0 after starting queued track", p.QueueLen())
	}
}

func TestTogglePlayPauseRestartsQueuedCurrentTrack(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Base", Path: "base.mp3", DurationSecs: 180},
		{Title: "Queued", Path: "queued.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)
	p.Queue(1)
	if track, ok := p.Next(); !ok || track.Title != "Queued" {
		t.Fatalf("Next() = (%q,%t), want (\"Queued\",true)", track.Title, ok)
	}
	if !p.CurrentIsQueued() {
		t.Fatal("CurrentIsQueued() = false, want true")
	}

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}

	if cmd := m.togglePlayPause(); cmd != nil {
		_ = cmd()
	}

	if len(player.playCalls) != 1 || player.playCalls[0] != "queued.mp3" {
		t.Fatalf("playCalls = %v, want [queued.mp3]", player.playCalls)
	}
	if current, idx := m.playlist.Current(); current.Title != "Queued" || idx != 1 {
		t.Fatalf("current = (%q,%d), want (\"Queued\",1)", current.Title, idx)
	}
}

func TestTogglePlayPauseReconnectsLongPausedYTDLAtCurrentPosition(t *testing.T) {
	player := &playbackFakeEngine{
		playing:  true,
		paused:   true,
		ytdlSeek: true,
		position: 90 * time.Second,
	}
	p := playlist.New()
	p.Replace([]playlist.Track{{
		Title:        "YouTube Music",
		Path:         "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
		Stream:       true,
		DurationSecs: 180,
	}})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		pausedAt: time.Now().Add(-ytdlReconnectPauseThreshold),
	}

	cmd := m.togglePlayPause()
	if cmd == nil {
		t.Fatal("togglePlayPause() = nil, want reconnect command")
	}
	msg := cmd()
	if reconnect, ok := msg.(ytdlUnpauseReconnectMsg); !ok || reconnect.err != nil {
		t.Fatalf("cmd() = %#v, want successful ytdlUnpauseReconnectMsg", msg)
	}
	if len(player.seekYTDLCalls) != 1 || player.seekYTDLCalls[0] != 0 {
		t.Fatalf("seekYTDLCalls = %v, want [0]", player.seekYTDLCalls)
	}
	if player.paused {
		t.Fatal("player stayed paused after reconnect command")
	}
	if !m.seek.active || m.seek.targetPos != 90*time.Second {
		t.Fatalf("seek state = active:%v target:%s, want active target 1m30s", m.seek.active, m.seek.targetPos)
	}
}

func TestPlayCurrentTrackUnplayableUsesSelectionOrder(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Queued", Path: "https://example.com/queued", Stream: true},
		{Title: "Missing", Unplayable: true},
		{Title: "Replacement", Path: "https://example.com/replacement", Stream: true},
	})
	p.SetIndex(1)
	p.Queue(0)

	m := Model{
		player:   player,
		playlist: p,
		provider: commandsTestProvider{name: "Test"},
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.requests.tracks = 1

	cmd := m.playCurrentTrack()
	if cmd == nil {
		t.Fatal("playCurrentTrack() = nil, want command")
	}
	if idx := m.playlist.Index(); idx != 2 {
		t.Fatalf("playlist.Index() = %d, want 2", idx)
	}
	if m.plCursor != 2 {
		t.Fatalf("plCursor = %d, want 2", m.plCursor)
	}
	if m.status.text != "Track unavailable, skipping..." {
		t.Fatalf("status.text = %q, want %q", m.status.text, "Track unavailable, skipping...")
	}
	if m.status.kind != feedbackWarning {
		t.Fatalf("status.kind = %v, want %v", m.status.kind, feedbackWarning)
	}
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() = %d, want 1", p.QueueLen())
	}
}

func TestPlayCurrentTrackUnplayableStopsWhenNoReplacementExists(t *testing.T) {
	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Playing", Path: "playing.mp3", DurationSecs: 2},
		{Title: "Missing", Unplayable: true},
	})
	p.SetIndex(1)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}

	if cmd := m.playCurrentTrack(); cmd != nil {
		t.Fatalf("playCurrentTrack() = %v, want nil", cmd)
	}
	if len(player.playCalls) != 0 {
		t.Fatalf("playCalls = %v, want none", player.playCalls)
	}
	if player.IsPlaying() {
		t.Fatal("player.IsPlaying() = true, want false")
	}
	if _, idx := m.playlist.Current(); idx != 1 {
		t.Fatalf("current index = %d, want 1", idx)
	}
	if m.status.text != "No available tracks" {
		t.Fatalf("status.text = %q, want %q", m.status.text, "No available tracks")
	}
	if m.status.kind != feedbackWarning {
		t.Fatalf("status.kind = %v, want %v", m.status.kind, feedbackWarning)
	}
}

func modelAfterProviderPlaylistLoadWhilePlaying(t *testing.T) (Model, *playbackFakeEngine) {
	t.Helper()

	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Old", Path: "old.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		provider: commandsTestProvider{name: "Test"},
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.requests.tracks = 1

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: []playlist.Track{
			{Title: "New 1", Path: "new1.mp3", DurationSecs: 180},
			{Title: "New 2", Path: "new2.mp3", DurationSecs: 180},
		},
		providerName: "Test",
		gen:          1,
	})
	m = updated.(Model)

	return m, player
}

func TestProviderPlaylistLoadWhilePlayingKeepsNowPlayingTrack(t *testing.T) {
	m, player := modelAfterProviderPlaylistLoadWhilePlaying(t)

	track, idx := m.currentPlaybackTrack()
	if idx < 0 || track.Title != "Old" {
		t.Fatalf("currentPlaybackTrack() = (%q,%d), want old playing track", track.Title, idx)
	}
	if !m.playbackDetached {
		t.Fatal("playbackDetached = false, want true")
	}
	if player.clearPreloadCalls != 1 {
		t.Fatalf("ClearPreload calls = %d, want 1", player.clearPreloadCalls)
	}
	tracks := m.playlist.Tracks()
	if len(tracks) != 2 || tracks[0].Title != "New 1" || tracks[1].Title != "New 2" {
		t.Fatalf("playlist tracks = %#v, want new provider playlist only", tracks)
	}
	if info := m.renderTrackInfo(); !strings.Contains(info, "Old") {
		t.Fatalf("renderTrackInfo() = %q, want old playing track", info)
	}
}

func TestNextAfterProviderPlaylistLoadStartsFirstNewTrack(t *testing.T) {
	m, player := modelAfterProviderPlaylistLoadWhilePlaying(t)

	cmd := m.nextTrack()
	if cmd != nil {
		_ = cmd()
	}

	if len(player.playCalls) == 0 || player.playCalls[0] != "new1.mp3" {
		t.Fatalf("playCalls = %v, want first new track", player.playCalls)
	}
	if m.playbackDetached {
		t.Fatal("playbackDetached = true, want false")
	}
	track, _ := m.currentPlaybackTrack()
	if track.Title != "New 1" {
		t.Fatalf("currentPlaybackTrack() = %q, want New 1", track.Title)
	}
}

func TestPreloadAfterProviderPlaylistLoadUsesFirstNewTrack(t *testing.T) {
	m, player := modelAfterProviderPlaylistLoadWhilePlaying(t)

	cmd := m.preloadNext()
	if cmd == nil {
		t.Fatal("preloadNext() = nil, want preload command")
	}
	_ = cmd()

	if len(player.preloadCalls) != 1 || player.preloadCalls[0] != "new1.mp3" {
		t.Fatalf("preloadCalls = %v, want first new track", player.preloadCalls)
	}
}

func TestPreloadNextSkipsLiveStream(t *testing.T) {
	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Station 1", Path: "https://example.com/one", Stream: true, Realtime: true},
		{Title: "Station 2", Path: "https://example.com/two", Stream: true, Realtime: true},
	})
	p.SetIndex(0)

	m := Model{player: player, playlist: p}
	if cmd := m.preloadNext(); cmd != nil {
		t.Fatal("preloadNext() returned a command for a live stream, want nil")
	}
	if m.preloading {
		t.Fatal("preloading = true for a live stream, want false")
	}
}

func TestPreloadNextSkipsRuntimeDetectedLiveStream(t *testing.T) {
	player := &playbackFakeEngine{playing: true, live: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Station 1", Path: "https://example.com/one", Stream: true},
		{Title: "Station 2", Path: "https://example.com/two", Stream: true},
	})
	p.SetIndex(0)

	m := Model{player: player, playlist: p}
	if cmd := m.preloadNext(); cmd != nil {
		t.Fatal("preloadNext() returned a command for a runtime-detected live stream")
	}
}

func TestPreloadNextSkipsUnknownDurationStream(t *testing.T) {
	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Current", Path: "current.mp3"},
		{Title: "Unknown", Path: "https://example.com/unknown", Stream: true},
	})
	p.SetIndex(0)

	m := Model{player: player, playlist: p}
	if cmd := m.preloadNext(); cmd != nil {
		t.Fatal("preloadNext() returned a command without a known stream boundary")
	}
}

func TestDrainedLiveStreamReconnectsCurrentStation(t *testing.T) {
	player := &playbackFakeEngine{playing: true, drained: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Station 1", Path: "https://example.com/one", Stream: true, Realtime: true},
		{Title: "Station 2", Path: "https://example.com/two", Stream: true, Realtime: true},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.SetVisualizer("none")

	now := time.Now()
	updated, _ := m.Update(tickMsg(now))
	m = updated.(Model)

	if got := m.playlist.Index(); got != 0 {
		t.Fatalf("playlist index = %d, want 0 after live stream drained", got)
	}
	if m.reconnect.at.IsZero() || !m.reconnect.at.After(now) {
		t.Fatalf("reconnect time = %v, want a future retry", m.reconnect.at)
	}
}

func TestGaplessAdvanceDoesNotAlsoDrainNextTrack(t *testing.T) {
	player := &playbackFakeEngine{playing: true, gaplessAdvanced: true, drained: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "One", Path: "one.mp3", DurationSecs: 180},
		{Title: "Two", Path: "two.mp3", DurationSecs: 180},
		{Title: "Three", Path: "three.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.SetVisualizer("none")

	updated, _ := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if got := m.playlist.Index(); got != 1 {
		t.Fatalf("playlist index = %d, want one gapless advance to index 1", got)
	}
}

func TestBeginPlaybackTrackFetchesEmbeddedLyricsWithoutNetworkMetadata(t *testing.T) {
	m := Model{lyrics: lyricsState{visible: true}}
	track := playlist.Track{Title: "Local", EmbeddedLyrics: "Line one\nLine two"}

	_, cmd := m.beginPlaybackTrack(track)
	if cmd == nil {
		t.Fatal("beginPlaybackTrack() command = nil, want embedded lyrics command")
	}
	if !m.lyrics.loading {
		t.Fatal("lyrics.loading = false, want true")
	}

	msg, ok := cmd().(lyricsLoadedMsg)
	if !ok {
		t.Fatalf("lyrics command returned %T, want lyricsLoadedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("lyrics command error = %v", msg.err)
	}
	if len(msg.lines) != 2 || msg.lines[0].Text != "Line one" || msg.lines[1].Text != "Line two" {
		t.Fatalf("lyrics lines = %+v, want embedded plain text", msg.lines)
	}
}

func TestGaplessAdvanceRefreshesLyricsAndArtwork(t *testing.T) {
	player := &playbackFakeEngine{playing: true, gaplessAdvanced: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Old", Artist: "Artist", Path: "old.mp3", DurationSecs: 180, EmbeddedLyrics: "old lyric", AlbumArtURL: "file:///old.jpg"},
		{Title: "New", Artist: "Artist", Path: "new.mp3", DurationSecs: 180, EmbeddedLyrics: "[00:01.00]new lyric", AlbumArtURL: "file:///new.jpg"},
	})
	p.SetIndex(0)

	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		lyrics: lyricsState{
			visible: true,
			query:   "Artist\nOld",
		},
	}
	m.setPlaybackTrack(p.Tracks()[0])
	m.lyrics.lines = nil

	next, cmd := m.Update(tickMsg(time.Now()))
	m2 := next.(Model)
	if cmd == nil {
		t.Fatal("Update() command = nil, want lyric/preload/tick batch")
	}

	track, _ := m2.currentPlaybackTrack()
	if track.Title != "New" {
		t.Fatalf("current track = %q, want New", track.Title)
	}
	if track.AlbumArtURL != "file:///new.jpg" {
		t.Fatalf("AlbumArtURL = %q, want new artwork", track.AlbumArtURL)
	}
	if m2.lyrics.query != "Artist\nNew" {
		t.Fatalf("lyrics.query = %q, want new track query", m2.lyrics.query)
	}
	if !m2.lyrics.loading {
		t.Fatal("lyrics.loading = false, want true for new track fetch")
	}
}

func TestQueueToggleRearmsGaplessPreload(t *testing.T) {
	player := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Playing", Path: "a.mp3", DurationSecs: 180},
		{Title: "Order next", Path: "b.mp3", DurationSecs: 180},
		{Title: "Queued", Path: "c.mp3", DurationSecs: 180},
	})
	m := Model{
		player:   player,
		playlist: p,
		plCursor: 2,
	}

	cmd := m.handleKey(tea.KeyPressMsg{Text: "a"})
	if cmd == nil {
		t.Fatal("handleKey(a) = nil, want preload command")
	}
	if got := p.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() = %d, want 1", got)
	}
	if player.clearPreloadCalls != 1 {
		t.Fatalf("ClearPreload calls = %d, want 1", player.clearPreloadCalls)
	}
	cmd()
	if len(player.preloadCalls) != 1 || player.preloadCalls[0] != "c.mp3" {
		t.Fatalf("preloadCalls = %v, want [c.mp3] (queued track, not order-next b.mp3)", player.preloadCalls)
	}
}
