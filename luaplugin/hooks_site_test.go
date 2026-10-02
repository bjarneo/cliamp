package luaplugin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSiteEventCount checks that the event count on the website matches the
// Event* constants in hooks.go. The test parses hooks.go, so a new constant
// needs no edit here.
func TestSiteEventCount(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "hooks.go", nil, 0)
	if err != nil {
		t.Fatalf("parse hooks.go: %v", err)
	}
	want := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				if strings.HasPrefix(name.Name, "Event") {
					want++
				}
			}
		}
	}
	if want == 0 {
		t.Fatal("hooks.go has no Event* constants")
	}

	data, err := os.ReadFile(filepath.Join("..", "site", "index.html"))
	if err != nil {
		t.Fatalf("read site/index.html: %v", err)
	}
	matches := regexp.MustCompile(`(\d+) events`).FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("site/index.html has no event count")
	}
	for _, m := range matches {
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("site/index.html says %q, want %d", m[0], want)
		}
	}
}
