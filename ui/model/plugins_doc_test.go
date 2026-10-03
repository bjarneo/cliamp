package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPluginDocsKeys checks the keys that docs/plugins.md names for p:bind
// against ReservedKeys. A copied example must bind, and the documented
// reserved and free keys must match the core.
func TestPluginDocsKeys(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "plugins.md"))
	if err != nil {
		t.Fatalf("read docs/plugins.md: %v", err)
	}
	doc := string(data)
	// sentence returns the text from prefix to the end of its sentence.
	sentence := func(prefix string) string {
		start := strings.Index(doc, prefix)
		if start < 0 {
			return ""
		}
		rest := doc[start:]
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			rest = rest[:end]
		}
		if end := strings.Index(rest, ". "); end >= 0 {
			rest = rest[:end]
		}
		return rest
	}
	quoted := regexp.MustCompile("`\"([^\"]+)\"`")
	code := regexp.MustCompile("`([^`]+)`")
	reserved := ReservedKeys()

	tests := []struct {
		name         string
		text         string
		pattern      *regexp.Regexp
		wantReserved bool
	}{
		{"p:bind examples", doc, regexp.MustCompile(`p:bind\("([^"]+)"`), false},
		{"key string examples", sentence("For example: `\""), quoted, false},
		{"text-editor keys", sentence("It also reserves the text-editor keys"), code, true},
		{"cursor aliases", sentence("The cursor aliases"), code, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := tt.pattern.FindAllStringSubmatch(tt.text, -1)
			if len(matches) == 0 {
				t.Fatal("docs/plugins.md names no key here")
			}
			for _, m := range matches {
				key := strings.ToLower(m[1])
				if reserved[key] != tt.wantReserved {
					t.Errorf("docs/plugins.md names %q: reserved = %v, want %v", m[1], reserved[key], tt.wantReserved)
				}
			}
		})
	}
}
