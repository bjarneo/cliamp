//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package session

import "golang.org/x/sys/unix"

const (
	getTermios = unix.TIOCGETA
	setTermios = unix.TIOCSETA
)
