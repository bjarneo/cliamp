package ui

import (
	"os"
	"sync"
	"testing"

	"github.com/charmbracelet/x/term"
)

// Bubbletea only treats its output as a terminal when the writer exposes
// Fd(). If this stops satisfying term.File, cliamp loses its window size and
// its colour profile — with no error anywhere, just a broken-looking UI.
func TestSyncWriterIsStillATerminal(t *testing.T) {
	var _ term.File = NewSyncWriter(os.Stdout)
}

func TestSyncWriterSerializesWrites(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	sw := NewSyncWriter(w)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				if _, err := sw.Write([]byte("0123456789abcdef")); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	w.Close()

	buf := make([]byte, 8*32*16)
	n, _ := r.Read(buf)
	for i := 0; i+16 <= n; i += 16 {
		if string(buf[i:i+16]) != "0123456789abcdef" {
			t.Fatalf("writes interleaved at byte %d: %q", i, buf[i:i+16])
		}
	}
}
