package ipc

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// DefaultSocketPath returns the default IPC socket path.
func DefaultSocketPath() string {
	return defaultSocketPath(appdir.Dir, os.Getuid())
}

func defaultSocketPath(dirFn func() (string, error), uid int) string {
	dir, err := dirFn()
	if err != nil {
		return filepath.Join(fallbackSocketDir(uid), "cliamp.sock")
	}
	return filepath.Join(dir, "cliamp.sock")
}

// fallbackSocketDir returns a per-user directory under os.TempDir.
func fallbackSocketDir(uid int) string {
	if uid >= 0 {
		return filepath.Join(os.TempDir(), fmt.Sprintf("cliamp-%d", uid))
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		if name := sanitizeDirComponent(u.Username); name != "" {
			return filepath.Join(os.TempDir(), "cliamp-"+name)
		}
	}
	return filepath.Join(os.TempDir(), "cliamp-user")
}

func sanitizeDirComponent(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		return ""
	}
	return name
}
