//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS delivers a URL to its handler as an Apple Event (kAEGetURL), not as a
// command-line argument, so a bare binary cannot be registered for a scheme.
// The handler has to be an app bundle that implements "open location".
//
// osacompile builds exactly that from a few lines of AppleScript and ships
// with every macOS install, which avoids carrying a prebuilt bundle in the
// repo or generating Mach-O by hand.
const bundleName = "cliamp URL Handler.app"

// handlerScript forwards the URL to `cliamp open` in a new Terminal window.
//
// The window is not optional: a cold start becomes the TUI and needs a
// terminal to draw in. When cliamp is already running, `cliamp open`
// dispatches over IPC and exits immediately, so the window is short-lived.
//
// "quoted form of" is what makes this safe. The URL arriving here has not
// been validated yet (that happens inside `cliamp open`), so it is escaped
// for the shell before it is ever interpolated into a command.
const handlerScript = `on open location this_URL
	set cliampPath to %s
	set theCommand to quoted form of cliampPath & " open " & quoted form of this_URL
	tell application "Terminal"
		activate
		do script theCommand
	end tell
end open location
`

func applicationsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the home directory: %w", err)
	}
	return filepath.Join(home, "Applications"), nil
}

func handlerFile() (string, error) {
	dir, err := applicationsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, bundleName), nil
}

func registerHandler(exe string) (string, error) {
	if _, err := exec.LookPath("osacompile"); err != nil {
		return "", fmt.Errorf("osacompile is required to build the URL handler bundle: %w", err)
	}

	dir, err := applicationsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	bundle := filepath.Join(dir, bundleName)

	// osacompile refuses to overwrite an existing bundle.
	if err := os.RemoveAll(bundle); err != nil {
		return "", fmt.Errorf("replacing %s: %w", bundle, err)
	}

	script := fmt.Sprintf(handlerScript, appleScriptString(exe))
	source, err := os.CreateTemp("", "cliamp-handler-*.applescript")
	if err != nil {
		return "", fmt.Errorf("creating the handler source: %w", err)
	}
	defer os.Remove(source.Name())
	if _, err := source.WriteString(script); err != nil {
		source.Close()
		return "", fmt.Errorf("writing the handler source: %w", err)
	}
	source.Close()

	out, err := exec.Command("osacompile", "-o", bundle, source.Name()).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("osacompile: %v: %s", err, strings.TrimSpace(string(out)))
	}

	if err := writeBundleURLTypes(bundle); err != nil {
		return "", err
	}
	registerWithLaunchServices(bundle)
	return bundle, nil
}

// writeBundleURLTypes declares the scheme in the bundle's Info.plist.
// osacompile writes a plist without CFBundleURLTypes, and LaunchServices only
// offers an app as a handler when that key names the scheme.
func writeBundleURLTypes(bundle string) error {
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	commands := [][]string{
		{"-c", "Delete :CFBundleURLTypes"},
		{"-c", "Add :CFBundleURLTypes array"},
		{"-c", "Add :CFBundleURLTypes:0 dict"},
		{"-c", "Add :CFBundleURLTypes:0:CFBundleURLName string cliamp"},
		{"-c", "Add :CFBundleURLTypes:0:CFBundleURLSchemes array"},
		{"-c", "Add :CFBundleURLTypes:0:CFBundleURLSchemes:0 string " + SchemeName},
		{"-c", "Add :LSBackgroundOnly bool true"},
	}
	for i, args := range commands {
		cmd := exec.Command("/usr/libexec/PlistBuddy", append(args, plist)...)
		out, err := cmd.CombinedOutput()
		// The leading Delete clears a previous run and fails on a fresh
		// bundle, which is expected. LSBackgroundOnly is a nicety, not a
		// requirement. Everything between them must succeed.
		if err != nil && i != 0 && i != len(commands)-1 {
			return fmt.Errorf("PlistBuddy %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// registerWithLaunchServices makes the new bundle visible without a logout.
func registerWithLaunchServices(bundle string) {
	const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	if _, err := os.Stat(lsregister); err != nil {
		return
	}
	_ = exec.Command(lsregister, "-f", bundle).Run()
}

// appleScriptString renders s as an AppleScript string literal.
func appleScriptString(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}

func unregisterHandler() (bool, string, error) {
	bundle, err := handlerFile()
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(bundle); err != nil {
		if os.IsNotExist(err) {
			return false, bundle, nil
		}
		return false, bundle, fmt.Errorf("reading %s: %w", bundle, err)
	}
	const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	if _, err := os.Stat(lsregister); err == nil {
		_ = exec.Command(lsregister, "-u", bundle).Run()
	}
	if err := os.RemoveAll(bundle); err != nil {
		return false, bundle, fmt.Errorf("removing %s: %w", bundle, err)
	}
	return true, bundle, nil
}

func handlerStatus() (string, bool, error) {
	bundle, err := handlerFile()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(bundle); err != nil {
		if os.IsNotExist(err) {
			return bundle, false, nil
		}
		return bundle, false, fmt.Errorf("reading %s: %w", bundle, err)
	}
	return bundle, true, nil
}
