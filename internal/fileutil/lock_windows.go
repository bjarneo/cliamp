//go:build windows

package fileutil

import (
	"fmt"
	"os"
)

// LockFile is a best-effort placeholder on Windows: it opens (and creates)
// path but takes no advisory lock. Windows builds assume a single cliamp
// process writes any given config file at a time.
func LockFile(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return func() error {
		if err := f.Close(); err != nil {
			return fmt.Errorf("close lock file: %w", err)
		}
		return nil
	}, nil
}
