package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestDocsVisualizerLists checks the visualizer comment of the example
// config and of configuration.md against the built-in modes, in cycle order.
func TestDocsVisualizerLists(t *testing.T) {
	tests := []struct {
		file string
		list *regexp.Regexp // captures the list of mode names
	}{
		{"config.toml.example", regexp.MustCompile(`(?m)^# Visualizer mode: (.+)$`)},
		{filepath.Join("docs", "configuration.md"), regexp.MustCompile(`(?m)^# Options: (.+)$`)},
	}
	want := VisModeNames()
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", tt.file))
			if err != nil {
				t.Fatalf("read %s: %v", tt.file, err)
			}
			// A Windows checkout can turn the line ends into CRLF.
			m := tt.list.FindStringSubmatch(strings.ReplaceAll(string(data), "\r\n", "\n"))
			if m == nil {
				t.Fatalf("%s has no match for %q", tt.file, tt.list)
			}
			got := strings.Split(strings.Replace(m[1], ", or ", ", ", 1), ", ")
			if !slices.Equal(got, want) {
				t.Errorf("%s lists %q, want %q", tt.file, got, want)
			}
		})
	}
}
