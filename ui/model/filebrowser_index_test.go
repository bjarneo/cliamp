package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fbIndexModel builds a model whose browser sits on dir with an empty listing.
func fbIndexModel(t *testing.T, dir string) Model {
	t.Helper()
	m := Model{}
	m.fileBrowser.visible = true
	m.fileBrowser.dir = dir
	m.fileBrowser.selected = make(map[string]bool)
	return m
}

// writeTree creates the files of a small music tree and returns its root.
func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"Root Track.mp3",
		"Albums/Nova Tunes/01 Intro.mp3",
		"Albums/Nova Tunes/02 Nova Track.mp3",
		"Albums/Other/03 Intro.flac",
		"Albums/.hidden/secret.mp3",
		"Podcasts/episode.mp3",
		"notes.txt",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

// fbRowNames returns the labels of the current view, in order.
func fbRowNames(m Model) []string {
	names := make([]string, 0, m.fbCount())
	for i := 0; i < m.fbCount(); i++ {
		names = append(names, m.fbEntry(i).name)
	}
	return names
}

func TestIndexDirRecursiveSkipsHiddenAndKeepsNested(t *testing.T) {
	root := writeTree(t)

	entries, err := indexDirRecursive(root)
	if err != nil {
		t.Fatalf("indexDirRecursive: %v", err)
	}

	var dirs, files []string
	for _, e := range entries {
		if e.isDir {
			dirs = append(dirs, e.name)
			continue
		}
		files = append(files, e.name)
	}
	wantDirs := []string{"Albums/", "Albums/Nova Tunes/", "Albums/Other/", "Podcasts/"}
	if !reflect.DeepEqual(dirs, wantDirs) {
		t.Fatalf("dirs = %v, want %v", dirs, wantDirs)
	}
	// A walk yields each directory's entries before it descends, so the
	// order follows the tree, not a global sort.
	wantFiles := []string{
		"Albums/Nova Tunes/01 Intro.mp3",
		"Albums/Nova Tunes/02 Nova Track.mp3",
		"Albums/Other/03 Intro.flac",
		"Podcasts/episode.mp3",
		"Root Track.mp3",
		"notes.txt",
	}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
	for _, e := range entries {
		if e.base == "" {
			t.Fatalf("entry %q has no base name", e.name)
		}
	}
}

func TestFBFilterReachesSubfolders(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()

	msg := m.startFBIndex()()
	index, ok := msg.(fbIndexMsg)
	if !ok {
		t.Fatalf("index command returned %T", msg)
	}
	m.handleFBIndex(index)

	m.fileBrowser.filtering = true
	m.fileBrowser.filter = "nova track"
	m.fbUpdateFilter()

	got := fbRowNames(m)
	want := []string{"Albums/Nova Tunes/02 Nova Track.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered rows = %v, want %v", got, want)
	}
}

func TestFBFilterListsDirsBeforeFiles(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()

	msg := m.startFBIndex()()
	m.handleFBIndex(msg.(fbIndexMsg))

	// "intro" names two files and no folder, so add a folder that carries it.
	if err := os.MkdirAll(filepath.Join(root, "Intro Mixes"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	entries, err := indexDirRecursive(root)
	if err != nil {
		t.Fatalf("indexDirRecursive: %v", err)
	}
	m.fileBrowser.index = entries

	m.fileBrowser.filtering = true
	m.fileBrowser.filter = "intro"
	m.fbUpdateFilter()

	rows := make([]fbEntry, 0, m.fbCount())
	for i := 0; i < m.fbCount(); i++ {
		rows = append(rows, m.fbEntry(i))
	}
	if len(rows) < 3 {
		t.Fatalf("rows = %v, want one folder and two files", fbRowNames(m))
	}
	if !rows[0].isDir || rows[0].base != "Intro Mixes" {
		t.Fatalf("first row = %q, want the Intro Mixes folder", rows[0].name)
	}
	sawFile := false
	for _, e := range rows {
		if e.isDir && sawFile {
			t.Fatalf("directory %q came after a file in %v", e.name, fbRowNames(m))
		}
		if !e.isDir {
			sawFile = true
		}
	}
}

func TestFBFilterKeepsDirectoryListingWithoutQuery(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()

	msg := m.startFBIndex()()
	m.handleFBIndex(msg.(fbIndexMsg))

	// A cleared query must show the current folder again, not the index.
	m.fileBrowser.filter = ""
	m.fbUpdateFilter()

	got := fbRowNames(m)
	want := []string{"..", "Albums/", "Podcasts/", "Root Track.mp3", "notes.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestFBIndexDroppedWhenDirectoryChanges(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()

	cmd := m.startFBIndex()
	if cmd == nil {
		t.Fatal("startFBIndex returned no command")
	}
	msg := cmd().(fbIndexMsg)

	// Moving on before the walk lands invalidates the pending index.
	m.fileBrowser.dir = filepath.Join(root, "Albums")
	m.loadFBDir()
	m.handleFBIndex(msg)

	if m.fileBrowser.index != nil {
		t.Fatal("index of the previous directory was installed")
	}
	m.fileBrowser.filtering = true
	m.fileBrowser.filter = "novatrack"
	m.fbUpdateFilter()
	if got := fbRowNames(m); len(got) != 0 {
		t.Fatalf("rows = %v, want no cross-directory matches", got)
	}
}

func TestFBIndexReusedForSameDirectory(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()

	msg := m.startFBIndex()()
	m.handleFBIndex(msg.(fbIndexMsg))

	if cmd := m.startFBIndex(); cmd != nil {
		t.Fatal("startFBIndex walked a directory it had already indexed")
	}
}

func TestFBSelectedInOrderFollowsViewOrder(t *testing.T) {
	root := writeTree(t)
	m := fbIndexModel(t, root)
	m.loadFBDir()
	m.startFBIndex()()
	m.handleFBIndex(m.startFBIndexCmdForTest())

	m.fileBrowser.filtering = true
	m.fileBrowser.filter = "intro"
	m.fbUpdateFilter()

	// Select the file first, then the folder: playback order follows the
	// rows on screen, not the order of the selection.
	m.fileBrowser.selected[filepath.Join(root, "Albums", "Other", "03 Intro.flac")] = true
	m.fileBrowser.selected[filepath.Join(root, "Albums", "Nova Tunes")] = true

	got := m.fbSelectedInOrder()
	want := []string{
		filepath.Join(root, "Albums", "Nova Tunes"),
		filepath.Join(root, "Albums", "Other", "03 Intro.flac"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection order = %v, want %v", got, want)
	}
}

// startFBIndexCmdForTest returns the index message the browser would receive.
func (m *Model) startFBIndexCmdForTest() fbIndexMsg {
	entries, err := indexDirRecursive(m.fileBrowser.dir)
	return fbIndexMsg{gen: m.fileBrowser.indexGen, dir: m.fileBrowser.dir, entries: entries, err: err}
}
