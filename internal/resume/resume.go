// Package resume persists the last-played track and position so playback
// can be resumed on the next launch.
package resume

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
)

// State holds enough information to resume a previous playback session.
type State struct {
	Path         string           `json:"path"`
	PositionSec  int              `json:"position_sec"`
	Playlist     string           `json:"playlist,omitempty"`
	Context      []playlist.Track `json:"context,omitempty"`
	ContextIndex int              `json:"context_index,omitempty"`
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
func Save(path string, positionSec int, playlist string) {
	SaveState(State{Path: path, PositionSec: positionSec, Playlist: playlist})
}

// SaveState writes a complete resume state, including its playback context.
// Errors are silently ignored so a failed write never disrupts normal exit.
func SaveState(state State) {
	if state.Path == "" || state.PositionSec < 0 || (state.PositionSec == 0 && len(state.Context) == 0) {
		return
	}
	f, err := stateFile()
	if err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = fileutil.WriteFileAtomic(f, data, 0o600)
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
