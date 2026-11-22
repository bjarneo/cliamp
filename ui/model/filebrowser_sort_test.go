package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes %q: %v", path, err)
	}
}

func TestFBSortKeepsDirsBeforeFiles(t *testing.T) {
	root := t.TempDir()
	mustMkdir := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		return p
	}
	mustFile := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return p
	}

	base := time.Unix(1_700_000_000, 0)
	touch(t, mustMkdir("bravo"), base)
	touch(t, mustMkdir("alpha"), base.Add(2*time.Hour))
	touch(t, mustFile("b.mp3"), base.Add(3*time.Hour))
	touch(t, mustFile("a.mp3"), base.Add(time.Hour))

	m := fbIndexModel(t, root)
	m.loadFBDir()

	m.fbCycleSort() // Newest
	got := fbRowNames(m)
	want := []string{"..", "alpha/", "bravo/", "b.mp3", "a.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newest = %v, want %v", got, want)
	}

	m.fbCycleSort() // Oldest
	got = fbRowNames(m)
	want = []string{"..", "bravo/", "alpha/", "a.mp3", "b.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("oldest = %v, want %v", got, want)
	}

	m.fbCycleSort() // Name (default)
	got = fbRowNames(m)
	want = []string{"..", "alpha/", "bravo/", "a.mp3", "b.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("name = %v, want %v", got, want)
	}
}

func TestFBSortCycleKey(t *testing.T) {
	m := fbIndexModel(t, t.TempDir())
	m.loadFBDir()

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "s"})
	if m.fileBrowser.sortMode != fbSortNewest {
		t.Fatalf("sort = %v, want newest", m.fileBrowser.sortMode)
	}
	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "s"})
	if m.fileBrowser.sortMode != fbSortOldest {
		t.Fatalf("sort = %v, want oldest", m.fileBrowser.sortMode)
	}
	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "s"})
	if m.fileBrowser.sortMode != fbSortName {
		t.Fatalf("sort = %v, want default name", m.fileBrowser.sortMode)
	}
}

func TestFbHeaderSortAccent(t *testing.T) {
	m := Model{layout: frameLayout{panelWidth: 80}}
	m.fileBrowser.dir = "/music"
	m.fileBrowser.sortMode = fbSortNewest
	got := m.fbHeaderLine()
	if !strings.Contains(ansi.Strip(got), "Files: /music · Sort: Newest") {
		t.Fatalf("header = %q", ansi.Strip(got))
	}
	if strings.Contains(got, "Sort: Newest") && got == ansi.Strip(got) {
		t.Fatal("sort label should be styled")
	}
}
