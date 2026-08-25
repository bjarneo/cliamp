package ipc

import (
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// DefaultSocketPath returns the default IPC socket path (~/.config/cliamp/cliamp.sock).
func DefaultSocketPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cliamp.sock")
	}
	return filepath.Join(dir, "cliamp.sock")
}
