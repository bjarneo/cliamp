package resume

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// withTempHome sets HOME so appdir.Dir() points inside a temp directory,
// restoring the original on cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempHome(t)

	Save("/music/song.mp3", 42, "main")

	got := Load()
	if got.Path != "/music/song.mp3" {
		t.Errorf("Path = %q, want /music/song.mp3", got.Path)
	}
	if got.PositionSec != 42 {
		t.Errorf("PositionSec = %d, want 42", got.PositionSec)
	}
	if got.Playlist != "main" {
		t.Errorf("Playlist = %q, want main", got.Playlist)
	}
}

func TestSaveStateLoadContextRoundTrip(t *testing.T) {
	withTempHome(t)
	state := State{
		Path:         "https://jf.example/Items/two/Download",
		PositionSec:  95,
		Context:      []playlist.Track{{Path: "one", Title: "One"}, {Path: "https://jf.example/Items/two/Download", Title: "Two", Stream: true}},
		ContextIndex: 1,
	}

	SaveState(state)

	if got := Load(); !reflect.DeepEqual(got, state) {
		t.Fatalf("Load() = %+v, want %+v", got, state)
	}
}

func TestSaveStatePersistsZeroPositionWithContext(t *testing.T) {
	withTempHome(t)
	state := State{
		Path:    "https://jf.example/Items/one/Download",
		Context: []playlist.Track{{Path: "https://jf.example/Items/one/Download", Title: "One"}},
	}

	SaveState(state)

	if got := Load(); !reflect.DeepEqual(got, state) {
		t.Fatalf("Load() = %+v, want %+v", got, state)
	}
}

func TestSaveIgnoresEmptyPath(t *testing.T) {
	home := withTempHome(t)
	Save("", 10, "p")

	f := filepath.Join(home, ".config", "cliamp", "resume.json")
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("resume.json should not exist for empty path, got err=%v", err)
	}
}

func TestSaveIgnoresNonPositivePosition(t *testing.T) {
	home := withTempHome(t)
	Save("/music/song.mp3", 0, "p")
	Save("/music/song.mp3", -5, "p")

	f := filepath.Join(home, ".config", "cliamp", "resume.json")
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("resume.json should not exist for non-positive position, got err=%v", err)
	}
}

func TestLoadMissingFileReturnsZero(t *testing.T) {
	withTempHome(t)
	got := Load()
	if !reflect.DeepEqual(got, State{}) {
		t.Errorf("Load() = %+v, want zero State", got)
	}
}

func TestLoadCorruptFileReturnsZero(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resume.json"), []byte("not json {{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := Load()
	if !reflect.DeepEqual(got, State{}) {
		t.Errorf("Load() = %+v, want zero State for corrupt file", got)
	}
}

func TestSaveCreatesParentDirectory(t *testing.T) {
	home := withTempHome(t)

	// Parent directory doesn't exist yet.
	parent := filepath.Join(home, ".config", "cliamp")
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("precondition: parent should not exist, got err=%v", err)
	}

	Save("/music/song.mp3", 1, "")

	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		t.Errorf("Save should create parent directory, err=%v", err)
	}
}

func TestSaveWriteFileIsReadable(t *testing.T) {
	home := withTempHome(t)
	Save("/music/a.mp3", 77, "pl")

	f := filepath.Join(home, ".config", "cliamp", "resume.json")
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("resume.json is empty")
	}
}

func TestSaveOverwritesPrevious(t *testing.T) {
	withTempHome(t)

	Save("/a.mp3", 10, "one")
	Save("/b.mp3", 20, "two")

	got := Load()
	if got.Path != "/b.mp3" || got.PositionSec != 20 || got.Playlist != "two" {
		t.Errorf("Load() = %+v, want Path=/b.mp3 PositionSec=20 Playlist=two", got)
	}
}
