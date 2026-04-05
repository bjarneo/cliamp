//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation

#import <Foundation/NSThread.h>

static int cliampIsMainThread(void) {
	return [NSThread isMainThread] ? 1 : 0;
}
*/
import "C"

func cocoaMainThread() bool {
	return C.cliampIsMainThread() == 1
}
