package model

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"cliamp/playlist"
	"cliamp/ui"
	tea "github.com/charmbracelet/bubbletea"
)

type wavHeader struct {
	RIFF          [4]byte
	FileSize      uint32
	WAVE          [4]byte
	Fmt           [4]byte
	FmtSize       uint32
	AudioFormat   uint16
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	Data          [4]byte
	DataSize      uint32
}

func writeSilentWAV(t *testing.T, sampleRate, frames int) string {
	t.Helper()

	path := t.TempDir() + "/test.wav"
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	defer file.Close()

	const (
		channels      = 2
		bitsPerSample = 16
	)
	blockAlign := channels * bitsPerSample / 8
	dataSize := frames * blockAlign
	header := wavHeader{
		RIFF:          [4]byte{'R', 'I', 'F', 'F'},
		FileSize:      uint32(36 + dataSize),
		WAVE:          [4]byte{'W', 'A', 'V', 'E'},
		Fmt:           [4]byte{'f', 'm', 't', ' '},
		FmtSize:       16,
		AudioFormat:   1,
		NumChannels:   channels,
		SampleRate:    uint32(sampleRate),
		ByteRate:      uint32(sampleRate * blockAlign),
		BlockAlign:    uint16(blockAlign),
		BitsPerSample: bitsPerSample,
		Data:          [4]byte{'d', 'a', 't', 'a'},
		DataSize:      uint32(dataSize),
	}
	if err := binary.Write(file, binary.LittleEndian, header); err != nil {
		t.Fatalf("binary.Write(header): %v", err)
	}
	if _, err := file.Write(make([]byte, dataSize)); err != nil {
		t.Fatalf("Write(data): %v", err)
	}

	return path
}

func TestNavTrackListQueueStartsQueuedTrackWhenStopped(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	sharedPlayer.Stop()
	t.Cleanup(sharedPlayer.Stop)

	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Existing", Path: "https://example.com/existing", Stream: true},
		{Title: "Other", Path: "https://example.com/other", Stream: true},
	})
	p.SetIndex(0)

	m := Model{
		player:   sharedPlayer,
		playlist: p,
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		navBrowser: navBrowserState{
			tracks: []playlist.Track{
				{Title: "Queued", Path: "https://example.com/queued", Stream: true},
			},
		},
	}

	cmd := m.handleNavTrackListKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
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

func TestPlayCurrentTrackUnplayableUsesSelectionOrder(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	sharedPlayer.Stop()
	t.Cleanup(sharedPlayer.Stop)

	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Queued", Path: "https://example.com/queued", Stream: true},
		{Title: "Missing", Unplayable: true},
		{Title: "Replacement", Path: "https://example.com/replacement", Stream: true},
	})
	p.SetIndex(1)
	p.Queue(0)

	m := Model{
		player:   sharedPlayer,
		playlist: p,
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
	}

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
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen() = %d, want 1", p.QueueLen())
	}
}

func TestPlayCurrentTrackUnplayableStopsWhenNoReplacementExists(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	sharedPlayer.Stop()
	t.Cleanup(sharedPlayer.Stop)

	path := writeSilentWAV(t, sharedPlayer.SampleRate(), sharedPlayer.SampleRate()*2)
	if err := sharedPlayer.Play(path, 2*time.Second); err != nil {
		t.Fatalf("sharedPlayer.Play(%q): %v", path, err)
	}
	if !sharedPlayer.IsPlaying() {
		t.Fatal("sharedPlayer.IsPlaying() = false, want true")
	}

	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Playing", Path: path, DurationSecs: 2},
		{Title: "Missing", Unplayable: true},
	})
	p.SetIndex(1)

	m := Model{
		player:   sharedPlayer,
		playlist: p,
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
	}

	if cmd := m.playCurrentTrack(); cmd != nil {
		t.Fatalf("playCurrentTrack() = %v, want nil", cmd)
	}
	if sharedPlayer.IsPlaying() {
		t.Fatal("sharedPlayer.IsPlaying() = true, want false")
	}
	if _, idx := m.playlist.Current(); idx != 1 {
		t.Fatalf("current index = %d, want 1", idx)
	}
	if m.status.text != "No available tracks" {
		t.Fatalf("status.text = %q, want %q", m.status.text, "No available tracks")
	}
}
