//go:build !linux && !darwin && !windows

package openmpt

import "errors"

func loadLibrary() (uintptr, error) {
	return 0, errors.New("openmpt: unsupported platform")
}

func dlsym(uintptr, string) (uintptr, error) {
	return 0, errors.New("openmpt: unsupported platform")
}
