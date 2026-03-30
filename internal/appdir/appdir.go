package appdir

import (
	"os"
	"path/filepath"
)

// Dir returns the cliamp configuration directory (~/.config/cliamp).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cliamp"), nil
}

// PluginDir returns the cliamp plugin directory (~/.config/cliamp/plugins).
func PluginDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plugins"), nil
}
