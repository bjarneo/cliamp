// Package ytdlbin resolves which yt-dlp executable cliamp runs.
//
// cliamp shells out to yt-dlp for YouTube, SoundCloud, Mixcloud, NetEase,
// Bandcamp, and Bilibili. Distributions sometimes ship a yt-dlp that is months
// behind upstream, and YouTube breaks old versions quickly. Pointing cliamp at
// a newer binary should not require shadowing the packaged one on PATH, so the
// resolved command comes from, in order: the CLIAMP_YTDLP environment
// variable, the ytdlp_path config key, then "yt-dlp" on PATH.
package ytdlbin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// EnvVar overrides both the configured path and the PATH lookup.
const EnvVar = "CLIAMP_YTDLP"

// DefaultName is resolved from PATH when nothing else is configured.
const DefaultName = "yt-dlp"

var (
	mu         sync.RWMutex
	configured string
)

// Configure records an explicit yt-dlp binary, typically the ytdlp_path key
// from config.toml. An empty path restores the default PATH lookup.
func Configure(path string) {
	mu.Lock()
	defer mu.Unlock()
	configured = expand(path)
}

// Name returns the command cliamp should exec. A bare "yt-dlp" is resolved
// from PATH by os/exec; an explicit path is used as given.
func Name() string {
	if env := expand(os.Getenv(EnvVar)); env != "" {
		return env
	}

	mu.RLock()
	defer mu.RUnlock()
	if configured != "" {
		return configured
	}
	return DefaultName
}

// LookPath reports the absolute path of the yt-dlp cliamp would run, or an
// error when it is missing or not executable.
func LookPath() (string, error) {
	return exec.LookPath(Name())
}

// Available reports whether yt-dlp can be executed.
func Available() bool {
	_, err := LookPath()
	return err == nil
}

// NotFoundError describes an unusable yt-dlp. An explicitly selected binary is
// named together with the setting that selected it, so a typo in ytdlp_path is
// not mistaken for a missing installation.
func NotFoundError() error {
	name := Name()
	if name == DefaultName {
		return errors.New("yt-dlp not found in PATH")
	}
	return fmt.Errorf("yt-dlp not found at %s (selected by %s)", name, selectedBy())
}

// NotFoundErrorWithAdvice returns NotFoundError with install guidance
// appended, but only when the bare PATH lookup is in effect. An explicitly
// selected binary keeps precedence over anything installed on PATH, so telling
// a user with a broken ytdlp_path or CLIAMP_YTDLP to install yt-dlp sends them
// after a fix that cannot work.
func NotFoundErrorWithAdvice(advice string) error {
	err := NotFoundError()
	if Name() != DefaultName || strings.TrimSpace(advice) == "" {
		return err
	}
	return fmt.Errorf("%w — %s", err, advice)
}

// selectedBy names the setting that chose the current binary.
func selectedBy() string {
	if expand(os.Getenv(EnvVar)) != "" {
		return EnvVar
	}
	return "ytdlp_path"
}

// Command builds an *exec.Cmd for the resolved yt-dlp binary.
func Command(args ...string) *exec.Cmd {
	return exec.Command(Name(), args...)
}

// CommandContext builds a context-bound *exec.Cmd for the resolved yt-dlp
// binary.
func CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, Name(), args...)
}

// expand trims a configured path and resolves a leading ~ so config files can
// use "~/.local/bin/yt-dlp". Paths that need no expansion are returned as-is.
func expand(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path[0] != '~' {
		return path
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path // ~user is left to the shell; we cannot resolve it.
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
