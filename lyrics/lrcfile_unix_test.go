//go:build unix

package lyrics

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO without a writer would block a plain open, and with it the whole
// lyrics lookup.
func TestLRCFileSkipsFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "song.lrc"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := LRCFile(filepath.Join(dir, "song.mp3"))(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("LRCFile() error = %v, want ErrNotFound", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LRCFile() blocked on a FIFO")
	}
}
