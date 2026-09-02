//go:build linux || darwin

// loadLibrary and dlsym are identical on Linux and Darwin — both use
// purego's dlopen(3)/dlsym(3) wrappers directly. Only the candidate
// library paths differ (see lib_linux.go / lib_darwin.go), so those stay
// per-platform while this file carries the shared logic once.
package openmpt

import "github.com/ebitengine/purego"

func loadLibrary() (uintptr, error) {
	var lastErr error
	for _, name := range libraryCandidates {
		handle, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return handle, nil
		}
		lastErr = err
	}
	return 0, lastErr
}

func dlsym(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}
