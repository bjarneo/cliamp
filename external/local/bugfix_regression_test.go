package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddDirSourcesDeduplicatesUncleanedPaths(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	musicDir := filepath.Join(t.TempDir(), "jazz")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := New(nil, nil)
	added, err := p.AddDirSources("Mix", []string{musicDir, musicDir + string(os.PathSeparator)})
	if err != nil {
		t.Fatalf("AddDirSources(): %v", err)
	}
	if len(added) != 1 {
		t.Fatalf("added = %v, want 1 deduplicated entry", added)
	}
	if added2, err := p.AddDirSources("Mix", []string{musicDir + string(os.PathSeparator)}); err != nil {
		t.Fatalf("second AddDirSources(): %v", err)
	} else if len(added2) != 0 {
		t.Fatalf("second AddDirSources added duplicate %v, want empty", added2)
	}
}
