//go:build windows

package openmpt

import "golang.org/x/sys/windows"

// libraryCandidates: the official Windows builds of libopenmpt ship as
// libopenmpt.dll.
var libraryCandidates = []string{
	"libopenmpt.dll",
}

func loadLibrary() (uintptr, error) {
	var lastErr error
	for _, name := range libraryCandidates {
		handle, err := windows.LoadLibrary(name)
		if err == nil {
			return uintptr(handle), nil
		}
		lastErr = err
	}
	return 0, lastErr
}

func dlsym(handle uintptr, name string) (uintptr, error) {
	addr, err := windows.GetProcAddress(windows.Handle(handle), name)
	return uintptr(addr), err
}
