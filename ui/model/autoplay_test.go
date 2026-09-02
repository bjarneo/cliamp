package model

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func autoplayTestModel(paths ...string) *Model {
	pl := playlist.New()
	for _, p := range paths {
		pl.Add(playlist.Track{Path: p, Title: p})
	}
	m := &Model{player: &playbackFakeEngine{}, playlist: pl, autoplayRadio: true}
	return m
}

func TestAutoplayEligibleSeed(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	mixURL, ok := m.autoplayEligibleSeed()
	if !ok {
		t.Fatal("eligible seed: ok = false, want true")
	}
	want := "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ"
	if mixURL != want {
		t.Errorf("mixURL = %q, want %q", mixURL, want)
	}

	m.autoplayRadio = false
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("disabled autoplay: ok = true, want false")
	}
	m.autoplayRadio = true

	m.autoplayFailedSeed = track.Path
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("failed seed: ok = true, want false")
	}
}

func TestAutoplayEligibleSeedNonYouTube(t *testing.T) {
	m := autoplayTestModel("/home/user/song.mp3")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("local file seed: ok = true, want false")
	}
}

func TestContinueWithAutoplayStartsFetchOnce(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	cmd, pending := m.continueWithAutoplay()
	if !pending || cmd == nil {
		t.Fatalf("first call: cmd=%v pending=%v, want non-nil cmd + pending", cmd, pending)
	}
	if !m.autoplayLoading {
		t.Error("autoplayLoading not set")
	}
	// Second call while in flight: pending, but no duplicate fetch.
	cmd2, pending2 := m.continueWithAutoplay()
	if !pending2 || cmd2 != nil {
		t.Errorf("second call: cmd=%v pending=%v, want nil cmd + pending", cmd2, pending2)
	}
}

func TestAppendAutoplayTracksDedupes(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	added := m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=seed00000001", Title: "seed again"},   // dup of queue entry
		{Path: "https://music.youtube.com/watch?v=seed00000001", Title: "seed music"}, // same ID, other host
		{Path: "https://www.youtube.com/watch?v=fresh0000001", Title: "fresh 1"},
		{Path: "https://www.youtube.com/watch?v=fresh0000002", Title: "fresh 2"},
		{Path: "https://www.youtube.com/watch?v=fresh0000001", Title: "fresh 1 dup"},
	})
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if m.playlist.Len() != 3 {
		t.Errorf("playlist len = %d, want 3", m.playlist.Len())
	}
}

func TestAppendAutoplayTracksAllDuplicates(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	added := m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=seed00000001"},
	})
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	if m.playlist.Len() != 1 {
		t.Errorf("playlist len = %d, want 1", m.playlist.Len())
	}
}

func TestNextTrackTriggersAutoplayAtQueueEnd(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	cmd := m.nextTrack()
	if cmd == nil {
		t.Fatal("nextTrack at queue end with autoplay on: cmd = nil, want fetch cmd")
	}
	if !m.autoplayAdvance {
		t.Error("autoplayAdvance not set")
	}
	if !m.playingTrackActive {
		t.Error("playback track cleared while autoplay fetch pending")
	}
}

func TestNextTrackStopsWhenAutoplayOff(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.autoplayRadio = false
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	if cmd := m.nextTrack(); cmd != nil {
		t.Errorf("autoplay off: cmd = %v, want nil (stop)", cmd)
	}
	if m.playingTrackActive {
		t.Error("playback track not cleared")
	}
}

func TestAutoplayTracksMsgAppendsAndAdvances(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)
	_ = m.nextTrack() // arms autoplayAdvance, sets gen

	updated, _ := m.Update(autoplayTracksMsg{
		gen:  m.requests.autoplay,
		seed: track.Path,
		tracks: []playlist.Track{
			{Path: "https://www.youtube.com/watch?v=fresh0000001", Title: "fresh", Stream: true},
		},
	})
	got := updated.(Model)
	if got.playlist.Len() != 2 {
		t.Fatalf("playlist len = %d, want 2", got.playlist.Len())
	}
	if got.autoplayAdvance {
		t.Error("autoplayAdvance not cleared")
	}
	cur, _ := got.playlist.Current()
	if cur.Path != "https://www.youtube.com/watch?v=fresh0000001" {
		t.Errorf("current = %q, want the appended track", cur.Path)
	}
}

func TestAutoplayTracksMsgStaleGenerationDropped(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.requests.autoplay = 5
	updated, _ := m.Update(autoplayTracksMsg{gen: 3, tracks: []playlist.Track{{Path: "x"}}})
	got := updated.(Model)
	if got.playlist.Len() != 1 {
		t.Errorf("stale msg mutated playlist: len = %d, want 1", got.playlist.Len())
	}
}

func TestAutoplayTracksMsgEmptyResultStops(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)
	_ = m.nextTrack()

	updated, _ := m.Update(autoplayTracksMsg{gen: m.requests.autoplay, seed: track.Path})
	got := updated.(Model)
	if got.playingTrackActive {
		t.Error("playback not stopped after empty autoplay result")
	}
	if got.autoplayFailedSeed != track.Path {
		t.Errorf("autoplayFailedSeed = %q, want %q", got.autoplayFailedSeed, track.Path)
	}
}

func TestMaybePrefetchAutoplay(t *testing.T) {
	engine := &playbackFakeEngine{playing: true, duration: 3 * time.Minute}
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", Stream: true})
	pl.SetIndex(0)
	m := &Model{player: engine, playlist: pl, autoplayRadio: true}
	track, _ := pl.Current()
	m.setPlaybackTrack(track)

	// Far from the end: no fetch yet.
	engine.position = 1 * time.Minute
	if cmd := m.maybePrefetchAutoplay(); cmd != nil {
		t.Error("prefetch fired with >45s remaining")
	}
	// Inside the lead window: fetch starts once.
	engine.position = 3*time.Minute - 30*time.Second
	if cmd := m.maybePrefetchAutoplay(); cmd == nil {
		t.Fatal("prefetch did not fire inside lead window")
	}
	if !m.autoplayLoading {
		t.Error("autoplayLoading not set")
	}
	if cmd := m.maybePrefetchAutoplay(); cmd != nil {
		t.Error("duplicate prefetch while one is in flight")
	}
}

type autoplaySaverStub struct{ saved map[string]string }

func (s *autoplaySaverStub) Save(key, value string) error {
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	s.saved[key] = value
	return nil
}

func TestAutoplayToggleKeyPersists(t *testing.T) {
	saver := &autoplaySaverStub{}
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.autoplayRadio = false
	m.configSaver = saver

	m.toggleAutoplayRadio()
	if !m.autoplayRadio {
		t.Error("toggle did not enable autoplay")
	}
	if saver.saved["autoplay_radio"] != "true" {
		t.Errorf("saved autoplay_radio = %q, want \"true\"", saver.saved["autoplay_radio"])
	}
	m.toggleAutoplayRadio()
	if m.autoplayRadio {
		t.Error("toggle did not disable autoplay")
	}
	if saver.saved["autoplay_radio"] != "false" {
		t.Errorf("saved autoplay_radio = %q, want \"false\"", saver.saved["autoplay_radio"])
	}
}

func TestAutoplayToggleKeyDispatch(t *testing.T) {
	saver := &autoplaySaverStub{}
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.autoplayRadio = false
	m.configSaver = saver

	m.handleKey(tea.KeyPressMsg{Text: "c"})
	if !m.autoplayRadio {
		t.Error("c key did not enable autoplay radio")
	}
	if saver.saved["autoplay_radio"] != "true" {
		t.Errorf("saved autoplay_radio = %q, want \"true\"", saver.saved["autoplay_radio"])
	}
}

func TestAppendAutoplayTracksCapsAtMax(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	var mix []playlist.Track
	mix = append(mix, playlist.Track{Path: "https://www.youtube.com/watch?v=seed00000001", Title: "seed"})
	for i := 0; i < 15; i++ {
		mix = append(mix, playlist.Track{
			Path:  fmt.Sprintf("https://www.youtube.com/watch?v=fresh%07d", i),
			Title: fmt.Sprintf("fresh %d", i),
		})
	}
	added := m.appendAutoplayTracks(mix)
	if added != autoplayMaxAppend {
		t.Errorf("added = %d, want %d", added, autoplayMaxAppend)
	}
	if m.playlist.Len() != 1+autoplayMaxAppend {
		t.Errorf("playlist len = %d, want %d", m.playlist.Len(), 1+autoplayMaxAppend)
	}
	// The kept tracks must be the first five non-duplicate Mix entries, in order.
	for i := 0; i < autoplayMaxAppend; i++ {
		got, _ := m.playlist.Track(1 + i)
		want := fmt.Sprintf("https://www.youtube.com/watch?v=fresh%07d", i)
		if got.Path != want {
			t.Errorf("track %d = %q, want %q", 1+i, got.Path, want)
		}
	}
}

func TestAutoplayMaxAppendIsFive(t *testing.T) {
	if autoplayMaxAppend != 5 {
		t.Errorf("autoplayMaxAppend = %d, want 5", autoplayMaxAppend)
	}
}

func TestPlayTrackImmediateDiscardsAutoplayTracks(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	added := m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=auto0000001", Title: "auto 1"},
		{Path: "https://www.youtube.com/watch?v=auto0000002", Title: "auto 2"},
		{Path: "https://www.youtube.com/watch?v=auto0000003", Title: "auto 3"},
	})
	if added != 3 || m.playlist.Len() != 4 {
		t.Fatalf("setup: added=%d len=%d, want 3 and 4", added, m.playlist.Len())
	}

	_ = m.playTrackImmediate(playlist.Track{Path: "https://www.youtube.com/watch?v=search000001", Title: "searched"})

	if m.playlist.Len() != 2 {
		t.Fatalf("playlist len = %d, want 2 (seed + searched)", m.playlist.Len())
	}
	first, _ := m.playlist.Track(0)
	if first.Path != "https://www.youtube.com/watch?v=seed00000001" {
		t.Errorf("track 0 = %q, want the pre-existing user track", first.Path)
	}
	cur, idx := m.playlist.Current()
	if idx != 1 {
		t.Errorf("current index = %d, want 1 (last index)", idx)
	}
	if cur.Path != "https://www.youtube.com/watch?v=search000001" {
		t.Errorf("current = %q, want the searched track", cur.Path)
	}
	if m.plCursor != 1 {
		t.Errorf("plCursor = %d, want 1", m.plCursor)
	}
}

func TestPlayTrackImmediateKeepsUserTracks(t *testing.T) {
	m := autoplayTestModel(
		"/home/user/a.mp3",
		"https://www.youtube.com/watch?v=user00000001",
	)
	m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=auto0000001", Title: "auto 1"},
		{Path: "https://www.youtube.com/watch?v=auto0000002", Title: "auto 2"},
	})
	if m.playlist.Len() != 4 {
		t.Fatalf("setup len = %d, want 4", m.playlist.Len())
	}

	_ = m.playTrackImmediate(playlist.Track{Path: "https://www.youtube.com/watch?v=search000001"})

	if m.playlist.Len() != 3 {
		t.Fatalf("playlist len = %d, want 3 (2 user + searched)", m.playlist.Len())
	}
	want := []string{
		"/home/user/a.mp3",
		"https://www.youtube.com/watch?v=user00000001",
		"https://www.youtube.com/watch?v=search000001",
	}
	for i, w := range want {
		got, _ := m.playlist.Track(i)
		if got.Path != w {
			t.Errorf("track %d = %q, want %q", i, got.Path, w)
		}
	}
}

func TestDiscardAutoplayTracksIsIdempotent(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	m.appendAutoplayTracks([]playlist.Track{{Path: "https://www.youtube.com/watch?v=auto0000001"}})
	if n := m.discardAutoplayTracks(); n != 1 {
		t.Errorf("first discard removed %d, want 1", n)
	}
	if n := m.discardAutoplayTracks(); n != 0 {
		t.Errorf("second discard removed %d, want 0", n)
	}
	if m.playlist.Len() != 1 {
		t.Errorf("playlist len = %d, want 1", m.playlist.Len())
	}
}
