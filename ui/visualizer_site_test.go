package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestSiteVisualizerCount checks that every visualizer count on the website
// matches the built-in modes. VisNone hides the visualizer and is no mode.
func TestSiteVisualizerCount(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "site", "index.html"))
	if err != nil {
		t.Fatalf("read site/index.html: %v", err)
	}
	want := int(VisCount) - 1
	for _, pattern := range []string{`(\d+) visualizers`, `(\d+) built-in modes`} {
		matches := regexp.MustCompile(pattern).FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("site/index.html has no match for %q", pattern)
		}
		for _, m := range matches {
			if got, _ := strconv.Atoi(m[1]); got != want {
				t.Errorf("site/index.html says %q, want %d", m[0], want)
			}
		}
	}
}
