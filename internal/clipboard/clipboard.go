// Package clipboard copies text to the system clipboard using whatever
// backend the platform provides. It is deliberately dependency-free:
// backends are resolved with exec.LookPath at call time so headless and
// minimal installs simply report an error instead of failing to build.
package clipboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// copyTimeout bounds a single backend invocation. Backends normally exit
// at once; the deadline only fires when one stalls (locked Wayland socket,
// wedged X server), which must not freeze the TUI update path.
const copyTimeout = 5 * time.Second

// Copy writes text to the system clipboard. It returns an error when no
// supported backend is installed; callers should fall back to showing the
// text (footer message, stdout) in that case.
func Copy(text string) error {
	candidates, err := backends()
	if err != nil {
		return fmt.Errorf("resolve clipboard backends: %w", err)
	}
	var lastErr error
	for _, c := range candidates {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
		cmd := exec.CommandContext(ctx, c.name, c.args...)
		cmd.Stdin = strings.NewReader(text)
		// Clipboard backends daemonize (wl-copy and xclip fork to
		// keep serving the selection). Capturing their output would
		// hold the pipes open until the daemon exits, so Run with
		// discarded output instead of CombinedOutput.
		cmd.Stdout = nil
		cmd.Stderr = nil
		runErr := cmd.Run()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			lastErr = fmt.Errorf("%s: timed out after %s", c.name, copyTimeout)
			continue
		}
		if runErr != nil {
			lastErr = fmt.Errorf("%s: %w", c.name, runErr)
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no clipboard backend found (%s)", backendNames(candidates))
}

type backend struct {
	name string
	args []string
}

func backends() ([]backend, error) {
	switch runtime.GOOS {
	case "darwin":
		return []backend{{name: "pbcopy"}}, nil
	case "windows":
		return []backend{{name: "clip"}}, nil
	default:
		// Wayland first when a Wayland session is present, then X11.
		// WSLg sets WAYLAND_DISPLAY; plain WSL falls through to the error,
		// where the caller prints the text instead.
		if isWayland() {
			return []backend{
				{name: "wl-copy"},
				{name: "xclip", args: []string{"-selection", "clipboard"}},
				{name: "xsel", args: []string{"--clipboard", "--input"}},
			}, nil
		}
		return []backend{
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
			{name: "wl-copy"},
		}, nil
	}
}

func isWayland() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland")
}

func backendNames(candidates []backend) string {
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}
