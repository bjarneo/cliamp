//go:build !windows

package fileutil

import (
	"fmt"
	"os"
	"syscall"
)

// LockFile acquires an exclusive advisory lock on path (creating it when
// missing) so concurrent cliamp processes serialize their load-modify-save
// cycles on shared config files. The returned function releases the lock and
// closes the underlying file.
func LockFile(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock file: %w", err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock file: %w", unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close lock file: %w", closeErr)
		}
		return nil
	}, nil
}
