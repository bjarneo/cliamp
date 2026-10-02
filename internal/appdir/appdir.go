package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dir returns the cliamp configuration directory.
//
// Resolution order:
//   - CLIAMP_CONFIG_DIR (explicit override)
//   - XDG_CONFIG_HOME/cliamp
//   - on Windows: APPDATA/cliamp, unless HOME points somewhere other than
//     the profile directory (an explicitly customized HOME keeps working as
//     before). A default HOME that merely mirrors the profile dir (Git
//     Bash/MSYS, or the HOME synthesized for plugin children) must not split
//     the config dir between the daemon and its children.
//     When the APPDATA location has no config yet but the legacy
//     HOME/.config/cliamp location does, the legacy one is used so existing
//     config survives the upgrade.
//   - HOME/.config/cliamp
//   - fallback: os.UserHomeDir()/.config/cliamp
func Dir() (string, error) {
	if dir, ok := os.LookupEnv("CLIAMP_CONFIG_DIR"); ok && dir != "" {
		return dir, nil
	}
	if xdg, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && xdg != "" {
		return filepath.Join(xdg, "cliamp"), nil
	}
	if runtime.GOOS == "windows" {
		if appData, ok := os.LookupEnv("APPDATA"); ok && appData != "" {
			home, homeSet := os.LookupEnv("HOME")
			userHome, _ := os.UserHomeDir()
			if dir, ok := resolveWindowsDir(appData, home, homeSet, userHome); ok {
				return dir, nil
			}
		}
	}
	if home, ok := os.LookupEnv("HOME"); ok && home != "" {
		return filepath.Join(home, ".config", "cliamp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cliamp"), nil
}

// resolveWindowsDir picks the Windows config dir for the given environment.
// A HOME that merely mirrors the profile directory (or no HOME at all)
// resolves to APPDATA/cliamp; an explicitly customized HOME falls through
// (ok=false) so the caller honors it as before. When the APPDATA location
// has no config.toml but the legacy HOME/.config/cliamp location does, the
// legacy dir is returned so upgrades keep existing config accessible.
func resolveWindowsDir(appData, home string, homeSet bool, userHome string) (string, bool) {
	customHome := homeSet && home != "" && !sameHomeDir(home, userHome)
	if customHome {
		return "", false
	}
	legacy := ""
	if homeSet && home != "" {
		legacy = filepath.Join(home, ".config", "cliamp")
	} else if userHome != "" {
		legacy = filepath.Join(userHome, ".config", "cliamp")
	}
	appDir := filepath.Join(appData, "cliamp")
	if _, err := os.Stat(filepath.Join(appDir, "config.toml")); os.IsNotExist(err) {
		if legacy != "" && legacy != appDir {
			if _, lerr := os.Stat(filepath.Join(legacy, "config.toml")); lerr == nil {
				return legacy, true
			}
		}
	}
	return appDir, true
}

// sameHomeDir reports whether home denotes the same directory as userHome.
// On Windows it first compares filesystem identity (so 8.3 aliases and
// junctions for the profile dir match), falling back to a comparison that
// ignores case, separator style and trailing separators, so spellings like
// `C:\Users\Ann` and `c:/users/ann/` match: a daemon with no HOME and a
// child with an equivalent HOME spelling must agree on the config dir, or
// the child misses the daemon socket. Elsewhere the comparison is exact.
func sameHomeDir(home, userHome string) bool {
	if home == "" || userHome == "" {
		return false
	}
	if runtime.GOOS != "windows" {
		return home == userHome
	}
	if homeInfo, homeErr := os.Stat(home); homeErr == nil {
		if userHomeInfo, userHomeErr := os.Stat(userHome); userHomeErr == nil {
			return os.SameFile(homeInfo, userHomeInfo)
		}
	}
	normalize := func(p string) string {
		return strings.TrimSuffix(strings.ToUpper(filepath.ToSlash(filepath.Clean(p))), "/")
	}
	return normalize(home) == normalize(userHome)
}

// PluginDir returns the cliamp plugin directory.
func PluginDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plugins"), nil
}

// DataDir returns the cliamp data directory (~/.local/share/cliamp), used for
// state that is not user-edited config: plugin stores, downloaded assets, etc.
func DataDir() (string, error) {
	// Honor HOME first, matching Dir(); on Windows os.UserHomeDir() reads
	// USERPROFILE and ignores HOME, so this keeps the two resolvers consistent.
	if home, ok := os.LookupEnv("HOME"); ok && home != "" {
		return filepath.Join(home, ".local", "share", "cliamp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "cliamp"), nil
}
