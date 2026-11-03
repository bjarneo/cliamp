package applemusic

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

func testStreamer() *streamer {
	s := newStreamer(&engine{}, request{title: "x", secs: 10}, 1000, nil)
	s.once.Do(func() {}) // keep Stream from driving Music
	return s
}

func TestPushSkipsLeadingSilence(t *testing.T) {
	s := testStreamer()
	s.push([]float32{0, 0, 0, 0, 0.5, 0.25, 0, 0})
	if s.count != 2 {
		t.Fatalf("count = %d, want 2", s.count)
	}
	if s.ring[0] != [2]float32{0.5, 0.25} {
		t.Fatalf("first frame = %v", s.ring[0])
	}
}

func TestPushDropsWhilePaused(t *testing.T) {
	s := testStreamer()
	s.waiting = false
	s.paused = true
	s.push([]float32{1, 1})
	if s.count != 0 {
		t.Fatalf("count = %d, want 0", s.count)
	}
}

func TestRingOverflowKeepsNewest(t *testing.T) {
	s := testStreamer()
	s.waiting = false
	frames := make([]float32, 2*(len(s.ring)+3))
	for i := range frames {
		frames[i] = float32(i/2 + 1)
	}
	s.push(frames)
	if s.count != len(s.ring) {
		t.Fatalf("count = %d", s.count)
	}
	if got := s.ring[s.head][0]; got != 4 {
		t.Fatalf("oldest = %v, want 4", got)
	}
}

func TestStreamFillsSilenceWithoutMovingPosition(t *testing.T) {
	s := testStreamer()
	out := make([][2]float64, 16)
	out[0] = [2]float64{9, 9}
	n, ok := s.Stream(out)
	if n != len(out) || !ok {
		t.Fatalf("Stream = %d, %v", n, ok)
	}
	if out[0] != [2]float64{} {
		t.Fatalf("not silent: %v", out[0])
	}
	if s.Position() != 0 {
		t.Fatalf("position = %d", s.Position())
	}
}

func TestStreamEndsAfterDrain(t *testing.T) {
	s := testStreamer()
	s.waiting = false
	s.push([]float32{0.5, 0.5, 0.5, 0.5})
	s.markEnded()
	out := make([][2]float64, 4)
	if n, ok := s.Stream(out); n != 2 || !ok {
		t.Fatalf("Stream = %d, %v, want 2, true", n, ok)
	}
	if n, ok := s.Stream(out); n != 0 || ok {
		t.Fatalf("Stream = %d, %v, want 0, false", n, ok)
	}
	if s.Position() != 2 {
		t.Fatalf("position = %d", s.Position())
	}
}

func TestSeekBeforeStartSetsOffset(t *testing.T) {
	s := testStreamer()
	if err := s.Seek(5000); err != nil {
		t.Fatal(err)
	}
	if s.offset != 5000 || s.Position() != 5000 {
		t.Fatalf("offset = %d, position = %d", s.offset, s.Position())
	}
	if err := s.Seek(99999); err == nil {
		t.Fatal("expected an out of range error")
	}
}

func TestSameTitle(t *testing.T) {
	if !sameTitle("Hoppípolla", "hoppípolla") {
		t.Fatal("case")
	}
	if !sameTitle("Teardrop", "Teardrop (Remastered)") {
		t.Fatal("suffix")
	}
	if sameTitle("Fury", "Hoppípolla") {
		t.Fatal("different songs matched")
	}
}

func TestParseURI(t *testing.T) {
	r, err := parseURI("applemusic:track:42?a=Muse&d=300&t=Fury&u=music%3A%2F%2Fx")
	if err != nil {
		t.Fatal(err)
	}
	if r.id != "42" || r.title != "Fury" || r.artist != "Muse" || r.secs != 300 || r.library || r.link != "music://x" {
		t.Fatalf("parsed %+v", r)
	}
	lib, err := parseURI("applemusic:lib:ABC?a=Muse&d=300&t=Fury")
	if err != nil || !lib.library {
		t.Fatalf("library: %+v, %v", lib, err)
	}
	if _, err := parseURI("applemusic:track:42?t=Fury"); err == nil {
		t.Fatal("expected a missing link error")
	}
	if _, err := parseURI("spotify:track:1"); err == nil {
		t.Fatal("expected a scheme error")
	}
}

func TestMusicURL(t *testing.T) {
	got := musicURL("https://music.apple.com/us/album/teardrop/724466069?i=724466700&uo=4")
	if !strings.HasPrefix(got, "music://music.apple.com/us/album/teardrop/724466069?") ||
		!strings.Contains(got, "i=724466700") || strings.Contains(got, "uo=") {
		t.Fatalf("got %q", got)
	}
}

// Synced lyrics treat a stream with no provider metadata as possible radio.
func TestLibraryTrackHasProviderMeta(t *testing.T) {
	track := libraryTrack("ABC", "Fury", "Muse", "Absolution", 300)
	if track.ProviderMeta[provider.MetaAppleMusicID] != "ABC" {
		t.Fatalf("meta = %v", track.ProviderMeta)
	}
}

func TestLibraryScriptAvoidsReservedWords(t *testing.T) {
	for _, word := range []string{" times ", " names ", " ids "} {
		if strings.Contains(scriptLibrary, word) {
			t.Fatalf("script uses %q", word)
		}
	}
}
