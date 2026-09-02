//go:build windows

package openmpt

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// libraryCandidates: the official Windows builds of libopenmpt ship as
// libopenmpt.dll.
var libraryCandidates = []string{
	"libopenmpt.dll",
}

func loadLibrary() (uintptr, error) {
	var lastErr error
	for _, name := range libraryCandidates {
		// Restrict the search to the directory cliamp.exe lives in and
		// system32. Plain LoadLibrary also consults the current working
		// directory and PATH (CWE-426), so a writable directory in either
		// could plant a malicious libopenmpt.dll. The application dir is
		// where the documented install instruction puts the DLL ("place
		// it next to cliamp.exe").
		const searchFlags = windows.LOAD_LIBRARY_SEARCH_APPLICATION_DIR |
			windows.LOAD_LIBRARY_SEARCH_SYSTEM32
		handle, err := windows.LoadLibraryEx(name, 0, searchFlags)
		if err == nil {
			return uintptr(handle), nil
		}
		lastErr = err
	}
	return 0, fmt.Errorf("load libopenmpt: %w", lastErr)
}

func dlsym(handle uintptr, name string) (uintptr, error) {
	addr, err := windows.GetProcAddress(windows.Handle(handle), name)
	return uintptr(addr), err
}
