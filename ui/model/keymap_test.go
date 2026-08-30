package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReservedKeysCoversHandleKey is a drift guard: every `case "..."` clause in
// the main handleKey switch (keys.go) must be represented in commandRegistry.
func TestReservedKeysCoversHandleKey(t *testing.T) {
	path := filepath.Join("keys.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Find every `case "X", "Y", ...:` clause in keys.go. We intentionally
	// over-collect (subhandler switches too) and then filter to the main
	// handler's section between "func (m *Model) handleKey" and its close.
	src := string(data)
	// The main dispatch switch is anchored by its comment header; overlays
	// and subhandlers have their own switches with different anchors.
	start := strings.Index(src, "// Vim-style count prefix")
	if start < 0 {
		t.Fatal("could not locate main dispatch anchor in keys.go")
	}
	// Bound at the next top-level function declaration to avoid scanning
	// into helper functions below handleKey.
	body := src[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	caseRe := regexp.MustCompile(`case ("[^"]+"(?:, "[^"]+")*):`)
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	reserved := ReservedKeys()

	var missing []string
	for _, m := range caseRe.FindAllStringSubmatch(body, -1) {
		for _, tok := range tokenRe.FindAllStringSubmatch(m[1], -1) {
			key := tok[1]
			if !reserved[key] {
				missing = append(missing, key)
			}
		}
	}

	if len(missing) > 0 {
		t.Fatalf("handleKey has case clauses not covered by coreReservedKeys: %v\nAdd these to keymap.go so plugin binds can't shadow them.", missing)
	}
}

// TestCommandModeIDsCoverEveryMode is a drift guard: adding a commandMode
// without an id would make the IPC screen field report "unknown".
func TestCommandModeIDsCoverEveryMode(t *testing.T) {
	seen := make(map[string]commandMode)
	for mode := commandModeMain; mode <= commandModeRadioStats; mode <<= 1 {
		id := mode.id()
		if id == "unknown" {
			t.Errorf("commandMode %#x has no id", uint64(mode))
			continue
		}
		if other, dup := seen[id]; dup {
			t.Errorf("id %q used by both %#x and %#x", id, uint64(other), uint64(mode))
		}
		seen[id] = mode
	}
	if commandModeAny.id() != "unknown" {
		t.Errorf("commandModeAny should not have an id")
	}
}
