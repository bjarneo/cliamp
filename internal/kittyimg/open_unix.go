//go:build unix

package kittyimg

import (
	"os"
	"syscall"
)

// openImage opens path without blocking on a FIFO that has no writer. The
// caller validates the descriptor and rejects anything that is not a regular
// file, so the non-blocking read never returns data.
func openImage(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
