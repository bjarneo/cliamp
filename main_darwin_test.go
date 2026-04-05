//go:build darwin && cgo

package main

import "testing"

var startupOnCocoaMainThread bool

func init() {
	startupOnCocoaMainThread = cocoaMainThread()
}

func TestDarwinStartupRunsOnCocoaMainThread(t *testing.T) {
	if !startupOnCocoaMainThread {
		t.Fatal("startup did not run on the Cocoa main thread")
	}
}
