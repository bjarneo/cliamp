//go:build darwin && cgo

package main

import "runtime"

func init() {
	runtime.LockOSThread()
}
