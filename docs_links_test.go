package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// markdownAnchors returns the anchors that GitHub gives the headings of a
// Markdown file. It skips fenced code blocks and adds -1, -2 and so on to a
// repeated slug.
func markdownAnchors(data string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	fenced := false
	for line := range strings.SplitSeq(data, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		title, ok := strings.CutPrefix(strings.TrimLeft(line, "#"), " ")
		if fenced || !ok || !strings.HasPrefix(line, "#") {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(title)) {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			}
		}
		slug := b.String()
		if n := seen[slug]; n > 0 {
			anchors[slug+"-"+strconv.Itoa(n)] = true
		} else {
			anchors[slug] = true
		}
		seen[slug]++
	}
	return anchors
}

// markdownLink matches a link to a Markdown file or to an anchor. It
// captures the file, which is empty for a link in the same file, and the
// anchor.
var markdownLink = regexp.MustCompile(`\]\(([^)#\s]*\.md)?#([^)\s]+)\)`)

// TestDocsAnchors checks that each anchor link of the docs and the README
// names a heading of the target file.
func TestDocsAnchors(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	// git ignores docs/ideas.md, a local notes file.
	files = slices.DeleteFunc(files, func(f string) bool { return filepath.Base(f) == "ideas.md" })
	files = append(files, "README.md")
	anchors := map[string]map[string]bool{}
	read := func(path string) map[string]bool {
		if a, ok := anchors[path]; ok {
			return a
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		anchors[path] = markdownAnchors(string(data))
		return anchors[path]
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range markdownLink.FindAllStringSubmatch(string(data), -1) {
				target := file
				if m[1] != "" {
					target = filepath.Join(filepath.Dir(file), m[1])
				}
				if !read(target)[m[2]] {
					t.Errorf("link %s#%s names no heading of %s", m[1], m[2], target)
				}
			}
		})
	}
}
