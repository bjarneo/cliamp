package theme

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestSiteThemeCount checks that the theme count on the website matches the
// embedded theme files.
func TestSiteThemeCount(t *testing.T) {
	files, err := fs.Glob(builtinThemes, "themes/*.toml")
	if err != nil {
		t.Fatalf("glob embedded themes: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("..", "site", "index.html"))
	if err != nil {
		t.Fatalf("read site/index.html: %v", err)
	}
	matches := regexp.MustCompile(`(\d+) built-in color schemes`).FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("site/index.html has no theme count")
	}
	for _, m := range matches {
		if got, _ := strconv.Atoi(m[1]); got != len(files) {
			t.Errorf("site/index.html says %q, want %d", m[0], len(files))
		}
	}
}
