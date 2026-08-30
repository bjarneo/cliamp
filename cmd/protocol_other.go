//go:build !linux && !darwin && !windows

package cmd

import (
	"fmt"
	"runtime"
)

// Registration is desktop-environment specific and there is no portable
// fallback. `cliamp open` still works everywhere, so a user on another
// platform can wire the scheme up with whatever their system provides.
func errUnsupported() error {
	return fmt.Errorf("registering the %s:// scheme is not supported on %s; run `cliamp open <uri>` directly", SchemeName, runtime.GOOS)
}

func registerHandler(string) (string, error)   { return "", errUnsupported() }
func unregisterHandler() (bool, string, error) { return false, "", errUnsupported() }
func handlerStatus() (string, bool, error)     { return "", false, errUnsupported() }
