//go:build linux

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// desktopFileName is a dedicated entry rather than a MimeType line added to
// the packaged cliamp.desktop. Keeping them separate means registering and
// unregistering the scheme never rewrites the launcher entry that install.sh
// owns, and `cliamp protocol unregister` can simply delete this file.
const desktopFileName = "cliamp-url-handler.desktop"

// desktopEntry is the handler registration.
//
// NoDisplay keeps it out of application menus: it exists to answer cliamp://
// links, and a second "cliamp" entry beside the real launcher would be
// confusing. Terminal=true matters more than it looks, because a cold start
// turns into the TUI and needs somewhere to draw.
const desktopEntry = `[Desktop Entry]
Type=Application
Name=cliamp (URL handler)
Comment=Open cliamp:// links in cliamp
Exec=%s open %%u
Icon=cliamp
Terminal=true
NoDisplay=true
StartupNotify=false
MimeType=x-scheme-handler/%s;
Categories=Audio;Music;Player;AudioVideo;
`

// desktopExecArg renders a path as a single Exec argument.
//
// The Desktop Entry spec splits Exec on whitespace, so an unquoted path
// containing a space becomes two arguments and the link silently fails to
// open with no diagnostic anywhere. Quoting is valid for any argument, and
// inside quotes the spec requires ", `, $ and \ to be escaped. Field codes
// such as %u must stay outside the quotes, which they do.
func desktopExecArg(path string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + replacer.Replace(path) + `"`
}

func applicationsDir() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating the home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "applications"), nil
}

// handlerSearchDirs lists the application directories a handler entry can
// live in, in XDG precedence order.
//
// install.sh writes the same entry next to cliamp.desktop, which for an
// install outside the home directory is a system-wide, root-owned path. Only
// looking under XDG_DATA_HOME would report the scheme as unregistered while
// links kept working.
func handlerSearchDirs() ([]string, error) {
	dir, err := applicationsDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{dir}

	system := strings.TrimSpace(os.Getenv("XDG_DATA_DIRS"))
	if system == "" {
		system = "/usr/local/share:/usr/share"
	}
	seen := map[string]bool{dir: true}
	for _, base := range strings.Split(system, ":") {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		candidate := filepath.Join(base, "applications")
		if !seen[candidate] {
			seen[candidate] = true
			dirs = append(dirs, candidate)
		}
	}
	return dirs, nil
}

// handlerLocations returns every existing handler entry, most specific first.
func handlerLocations() ([]string, error) {
	dirs, err := handlerSearchDirs()
	if err != nil {
		return nil, err
	}
	var found []string
	for _, dir := range dirs {
		path := filepath.Join(dir, desktopFileName)
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	}
	return found, nil
}

func registerHandler(exe string) (string, error) {
	dir, err := applicationsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, desktopFileName)
	entry := fmt.Sprintf(desktopEntry, desktopExecArg(exe), SchemeName)
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	// Both helpers only refresh caches. The desktop file above is the
	// registration, so a machine without them is still correctly configured
	// once its session rescans.
	runQuiet("update-desktop-database", dir)
	runQuiet("xdg-mime", "default", desktopFileName, "x-scheme-handler/"+SchemeName)
	return path, nil
}

// unregisterHandler removes every handler entry it has permission to delete
// and reports the rest, which are the root-owned ones a system-wide install
// leaves behind.
func unregisterHandler() (removed, blocked []string, err error) {
	locations, err := handlerLocations()
	if err != nil {
		return nil, nil, err
	}
	for _, path := range locations {
		if err := os.Remove(path); err != nil {
			if os.IsPermission(err) {
				blocked = append(blocked, path)
				continue
			}
			if os.IsNotExist(err) {
				continue
			}
			return removed, blocked, fmt.Errorf("removing %s: %w", path, err)
		}
		removed = append(removed, path)
		runQuiet("update-desktop-database", filepath.Dir(path))
	}
	return removed, blocked, nil
}

func handlerStatus() ([]string, error) {
	return handlerLocations()
}
