//go:build windows

package luaplugin

import "syscall"

// writeOpenFlags prevents blocking opens where supported on Windows.
const writeOpenFlags = syscall.O_NONBLOCK
