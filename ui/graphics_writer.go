package ui

import (
	"os"
	"sync"
)

// The terminal is written by two parties: Bubbletea's renderer, which flushes
// whole frames from its own ticker goroutine, and this package, which writes
// graphics escapes for a clock while a view is being built. Neither knows
// about the other, and a write to a tty is not atomic at the size of an image
// transmission, so without a shared lock a few hundred KB of glyph data can be
// spliced into the middle of a frame and corrupt the display.
//
// SyncWriter is that shared lock. main gives one to Bubbletea as its output
// and to SetGraphicsOutput, so every write to the terminal — frame or image —
// takes the same mutex and lands whole.
//
// It stays a file rather than wrapping one in a plain writer: Bubbletea only
// treats its output as a terminal when the writer exposes Fd(), and without
// that it cannot read the window size or detect the colour profile.
type SyncWriter struct {
	mu sync.Mutex
	f  *os.File
}

// NewSyncWriter returns a writer serializing everything written through it.
// A nil file means os.Stdout.
func NewSyncWriter(f *os.File) *SyncWriter {
	if f == nil {
		f = os.Stdout
	}
	return &SyncWriter{f: f}
}

func (s *SyncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Write(p)
}

// Read, Close and Fd forward to the file, so a SyncWriter is still the
// terminal as far as Bubbletea is concerned.
func (s *SyncWriter) Read(p []byte) (int, error) { return s.f.Read(p) }

func (s *SyncWriter) Close() error { return s.f.Close() }

func (s *SyncWriter) Fd() uintptr { return s.f.Fd() }
