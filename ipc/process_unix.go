//go:build !windows

package ipc

import (
	"os"
	"syscall"
)

// checkDirOwner rejects a socket directory owned by another user.
func checkDirOwner(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return peerUIDAllowed(st.Uid, os.Geteuid())
}
