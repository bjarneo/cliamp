//go:build !linux

package cmd

import (
	"fmt"
	"runtime"
)

// Only Linux registration is implemented. macOS needs an app bundle because
// the OS delivers a URL as an Apple Event rather than an argument, and
// Windows needs registry keys; neither is written yet.
//
// `cliamp open <uri>` works on every platform, so the scheme is still usable
// through whatever the system provides for registering a handler.
func errUnsupported() error {
	return fmt.Errorf("registering the %s:// scheme is not implemented on %s yet; `cliamp open <uri>` works, so wire it up with your platform's handler settings", SchemeName, runtime.GOOS)
}

func registerHandler(string) (string, error)   { return "", errUnsupported() }
func unregisterHandler() (bool, string, error) { return false, "", errUnsupported() }
func handlerStatus() (string, bool, error)     { return "", false, errUnsupported() }
