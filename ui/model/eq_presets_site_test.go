package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestSiteEQPresetCount checks that the EQ preset count on the website
// matches the built-in presets.
func TestSiteEQPresetCount(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "site", "index.html"))
	if err != nil {
		t.Fatalf("read site/index.html: %v", err)
	}
	matches := regexp.MustCompile(`(\d+) presets`).FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("site/index.html has no preset count")
	}
	for _, m := range matches {
		if got, _ := strconv.Atoi(m[1]); got != len(eqPresets) {
			t.Errorf("site/index.html says %q, want %d", m[0], len(eqPresets))
		}
	}
}
