//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Windows resolves a URI scheme through HKCU\Software\Classes\<scheme>. The
// per-user hive is deliberate: it needs no elevation and cannot affect other
// accounts on the machine.
const schemeKey = `HKCU\Software\Classes\` + SchemeName

func handlerFile() (string, error) {
	return schemeKey, nil
}

func registerHandler(exe string) (string, error) {
	// "%1" is the URI. Quoting both it and the executable path keeps a URI
	// containing spaces in one argument, which is what `cliamp open` expects.
	command := fmt.Sprintf(`"%s" open "%%1"`, exe)
	writes := [][]string{
		{"add", schemeKey, "/ve", "/t", "REG_SZ", "/d", "URL:cliamp Protocol", "/f"},
		{"add", schemeKey, "/v", "URL Protocol", "/t", "REG_SZ", "/d", "", "/f"},
		{"add", schemeKey + `\DefaultIcon`, "/ve", "/t", "REG_SZ", "/d", exe + ",1", "/f"},
		{"add", schemeKey + `\shell\open\command`, "/ve", "/t", "REG_SZ", "/d", command, "/f"},
	}
	for _, args := range writes {
		if out, err := exec.Command("reg", args...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("reg %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return schemeKey, nil
}

func unregisterHandler() (bool, string, error) {
	registered, err := keyExists()
	if err != nil {
		return false, schemeKey, err
	}
	if !registered {
		return false, schemeKey, nil
	}
	if out, err := exec.Command("reg", "delete", schemeKey, "/f").CombinedOutput(); err != nil {
		return false, schemeKey, fmt.Errorf("reg delete %s: %v: %s", schemeKey, err, strings.TrimSpace(string(out)))
	}
	return true, schemeKey, nil
}

func handlerStatus() (string, bool, error) {
	registered, err := keyExists()
	return schemeKey, registered, err
}

// keyExists reports whether the scheme key is present. reg query exits
// non-zero for a missing key, which is not an error worth surfacing, so only
// a missing reg.exe is reported as one.
func keyExists() (bool, error) {
	err := exec.Command("reg", "query", schemeKey).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("querying %s: %w", schemeKey, err)
}
