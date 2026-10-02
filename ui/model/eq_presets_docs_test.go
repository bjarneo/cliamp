package model

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// TestDocsEQPresetLists checks the eq_preset comment of the example config
// and of configuration.md against the built-in presets, in cycle order.
func TestDocsEQPresetLists(t *testing.T) {
	var want []string
	for _, p := range eqPresets {
		want = append(want, p.Name)
	}
	block := regexp.MustCompile(`(?m)^# EQ preset: (?:.*\n)*?# Leave empty`)
	quoted := regexp.MustCompile(`"([^"]+)"`)
	for _, file := range []string{"config.toml.example", filepath.Join("docs", "configuration.md")} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", file))
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			b := block.FindString(string(data))
			if b == "" {
				t.Fatalf("%s has no EQ preset comment", file)
			}
			var got []string
			for _, m := range quoted.FindAllStringSubmatch(b, -1) {
				got = append(got, m[1])
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s lists %q, want %q", file, got, want)
			}
		})
	}
}
