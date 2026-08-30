// protocol.go implements `cliamp protocol register|unregister|status`, which
// wires the cliamp:// URI scheme into the desktop environment so links open
// in cliamp.
//
// Registration is deliberately opt-in rather than something install.sh does.
// A registered scheme lets any web page hand cliamp a target with one click,
// which is a capability the user should grant on purpose and be able to take
// back with one command.
package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// SchemeName is the URI scheme registered with the desktop environment. It
// matches deeplink.Scheme; the two are kept in step by TestSchemeMatches.
const SchemeName = "cliamp"

// ProtocolRegister makes this binary the system handler for cliamp:// links.
func ProtocolRegister(out io.Writer) error {
	exe, err := handlerExecutable()
	if err != nil {
		return err
	}
	location, err := registerHandler(exe)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Registered %s:// -> %s\n", SchemeName, exe)
	fmt.Fprintf(out, "Handler: %s\n", location)
	fmt.Fprintf(out, "\nTest it with:\n  %s open '%s://play?url=https://example.com/stream.mp3'\n", exe, SchemeName)
	fmt.Fprintf(out, "\nRemove it with:\n  cliamp protocol unregister\n")
	return nil
}

// ProtocolUnregister removes every handler registration it can write to.
//
// install.sh registers the scheme too, and for a system-wide install it does
// so under a root-owned directory. Reporting those separately matters: saying
// "nothing to do" while links still open cliamp would be worse than saying
// which file is left and why.
func ProtocolUnregister(out io.Writer) error {
	removed, blocked, err := unregisterHandler()
	if err != nil {
		return err
	}
	if len(removed) == 0 && len(blocked) == 0 {
		fmt.Fprintf(out, "%s:// was not registered; nothing to do\n", SchemeName)
		return nil
	}
	for _, location := range removed {
		fmt.Fprintf(out, "Removed %s\n", location)
	}
	if len(blocked) > 0 {
		fmt.Fprintf(out, "\n%s:// is still registered by a file this user cannot remove:\n", SchemeName)
		for _, location := range blocked {
			fmt.Fprintf(out, "  %s\n", location)
		}
		fmt.Fprintf(out, "Remove it with:\n  sudo rm %s\n", blocked[0])
		return nil
	}
	fmt.Fprintf(out, "\nUnregistered %s://\n", SchemeName)
	return nil
}

// ProtocolStatus reports where the scheme is currently registered.
func ProtocolStatus(out io.Writer) error {
	locations, err := handlerStatus()
	if err != nil {
		return err
	}
	if len(locations) == 0 {
		fmt.Fprintf(out, "%s:// is not registered\n", SchemeName)
		fmt.Fprintf(out, "Register it with:\n  cliamp protocol register\n")
		return nil
	}
	fmt.Fprintf(out, "%s:// is registered\n", SchemeName)
	for _, location := range locations {
		fmt.Fprintf(out, "Handler: %s\n", location)
	}
	if len(locations) > 1 {
		fmt.Fprintf(out, "\nThe first entry takes precedence; the others are shadowed.\n")
	}
	return nil
}

// handlerExecutable resolves the absolute path recorded in the registration.
// A relative or symlinked path would break as soon as the working directory
// changed or the binary moved, and the desktop environment gives no useful
// error when a handler fails to launch.
func handlerExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the cliamp binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// A broken symlink is worth reporting, but an unreadable one should
		// not block registration when the original path is usable.
		return exe, nil
	}
	return resolved, nil
}

// runQuiet executes a desktop-integration helper, ignoring a missing binary.
// These commands refresh caches; the registration itself is the file or key
// written before them, so a missing helper must not fail the whole command.
func runQuiet(name string, args ...string) {
	if _, err := exec.LookPath(name); err != nil {
		return
	}
	_ = exec.Command(name, args...).Run()
}
