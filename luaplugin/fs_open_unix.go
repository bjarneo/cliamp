//go:build !windows

package luaplugin

import "syscall"

// writeOpenFlags prevents blocking on FIFOs and blocks following a symlink at
// the final path component.
const writeOpenFlags = syscall.O_NONBLOCK | syscall.O_NOFOLLOW
