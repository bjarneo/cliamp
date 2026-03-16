// Package resume persists the last-played track and position so playback
// can be resumed on the next launch.
package resume

import (
	"encoding/json"
	"os"
	"path/filepath"

	"cliamp/internal/appdir"
)

// State holds enough information to resume a previous playback session.
type State struct {
	Path        string `json:"path"`
	PositionSec int    `json:"position_sec"`
}

func stateFile() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "resume.json"), nil
}

// Save writes the resume state to disk. No-ops for empty path or zero/negative
// position to avoid overwriting a valid resume file with useless data.
// Errors are silently ignored so a failed write never disrupts normal exit.
func Save(path string, positionSec int) {
	if path == "" || positionSec <= 0 {
		return
	}
	f, err := stateFile()
	if err != nil {
		return
	}
	data, err := json.Marshal(State{Path: path, PositionSec: positionSec})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, data, 0o600)
}

// Load reads the resume state from disk. Returns a zero State if the file
// does not exist or cannot be parsed.
func Load() State {
	f, err := stateFile()
	if err != nil {
		return State{}
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}
