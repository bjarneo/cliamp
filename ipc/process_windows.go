//go:build windows

package ipc

import "os"

// checkDirOwner is a no-op on Windows.
func checkDirOwner(os.FileInfo) error {
	return nil
}
