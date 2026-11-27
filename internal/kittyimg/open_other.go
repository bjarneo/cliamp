//go:build !unix

package kittyimg

import "os"

// openImage opens path. Platforms without O_NONBLOCK keep the plain open; the
// caller still validates that the descriptor is a regular file.
func openImage(path string) (*os.File, error) {
	return os.Open(path)
}
