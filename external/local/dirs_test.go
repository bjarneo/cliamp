package local

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

// writeAudioFile creates an empty file with a supported extension. Tag reading
// falls back to filename parsing for empty files, so fixtures stay simple.
func writeAudioFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("writeAudioFile(%s): %v", path, err)
	}
}

// makeAudioTree creates dir with two audio files and a subdir with one more.
func makeAudioTree(t *testing.T, dir string) {
	t.Helper()
	writeAudioFile(t, filepath.Join(dir, "b.mp3"))
	writeAudioFile(t, filepath.Join(dir, "a.flac"))
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeAudioFile(t, filepath.Join(sub, "c.ogg"))
}

func TestParsePlaylistDocMixedOrder(t *testing.T) {
	data := []byte(`[[track]]
path = "/a.mp3"
title = "A"

[[dir]]
path = "/music"

[[track]]
path = "/b.mp3"

[[dir]]
path = "/other"
recursive = false
`)
	doc := parsePlaylistDoc(data)
	if len(doc.tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(doc.tracks))
	}
	if len(doc.dirs) != 2 {
		t.Fatalf("got %d dirs, want 2", len(doc.dirs))
	}
	if doc.dirs[0].Path != "/music" || !doc.dirs[0].Recursive {
		t.Fatalf("dir 0 = %+v, want /music recursive", doc.dirs[0])
	}
	if doc.dirs[1].Path != "/other" || doc.dirs[1].Recursive {
		t.Fatalf("dir 1 = %+v, want /other non-recursive", doc.dirs[1])
	}
	// Order must follow the document: track, dir, track, dir.
	if len(doc.order) != 4 {
		t.Fatalf("order length = %d, want 4", len(doc.order))
	}
	if doc.order[0] != itemTrack || doc.order[1] != itemDir || doc.order[2] != itemTrack || doc.order[3] != itemDir {
		t.Fatalf("unexpected order: %v", doc.order)
	}
}

func TestParsePlaylistDocSkipsEmptyDirPath(t *testing.T) {
	doc := parsePlaylistDoc([]byte("[[dir]]\n[[track]]\npath = \"/a.mp3\"\n"))
	if len(doc.dirs) != 0 {
		t.Fatalf("got %d dirs, want 0", len(doc.dirs))
	}
	if len(doc.tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(doc.tracks))
	}
}

func TestExpandPathEnvAndTilde(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_TEST_DIR", dir)
	if got := filepath.Clean(ExpandPath("$CLIAMP_TEST_DIR/sub")); got != filepath.Join(dir, "sub") {
		t.Fatalf("env expand = %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if got := ExpandPath("~/music"); got != filepath.Join(home, "music") {
		t.Fatalf("tilde expand = %q, want %q", got, filepath.Join(home, "music"))
	}
	if got := ExpandPath("plain"); got != "plain" {
		t.Fatalf("plain expand = %q", got)
	}
}

func TestValidateDirSource(t *testing.T) {
	dir := t.TempDir()
	if err := validateDirSource(dir); err != nil {
		t.Fatalf("existing dir rejected: %v", err)
	}
	file := filepath.Join(dir, "f.mp3")
	writeAudioFile(t, file)
	if err := validateDirSource(file); err == nil {
		t.Fatal("file accepted as directory source")
	}
	if err := validateDirSource(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func TestPlaylistDocExpand(t *testing.T) {
	audio := t.TempDir()
	makeAudioTree(t, audio)

	doc := parsePlaylistDoc([]byte("[[dir]]\npath = " + quote(audio) + "\n"))
	tracks := doc.expand(true)
	if len(tracks) != 3 {
		t.Fatalf("expand = %d tracks, want 3", len(tracks))
	}
	for _, tr := range tracks {
		if !tr.DirSourced {
			t.Fatalf("track %q should be DirSourced", tr.Path)
		}
	}
	// Sorted by path: a.flac, b.mp3, sub/c.ogg.
	if filepath.Base(tracks[0].Path) != "a.flac" || filepath.Base(tracks[1].Path) != "b.mp3" || filepath.Base(tracks[2].Path) != "c.ogg" {
		for i, tr := range tracks {
			t.Logf("  [%d] %s", i, tr.Path)
		}
		t.Fatal("unexpected scan order")
	}
}

func TestPlaylistDocExpandNonRecursive(t *testing.T) {
	audio := t.TempDir()
	makeAudioTree(t, audio)
	doc := parsePlaylistDoc([]byte("[[dir]]\npath = " + quote(audio) + "\nrecursive = false\n"))
	tracks := doc.expand(false)
	if len(tracks) != 2 {
		t.Fatalf("expand = %d tracks, want 2 (no subdir)", len(tracks))
	}
}

func TestPlaylistDocExpandExplicitShadowsDir(t *testing.T) {
	audio := t.TempDir()
	makeAudioTree(t, audio)
	explicit := filepath.Join(audio, "b.mp3")

	doc := parsePlaylistDoc([]byte(
		"[[dir]]\npath = " + quote(audio) + "\n\n" +
			"[[track]]\npath = " + quote(explicit) + "\ntitle = \"Custom\"\nbookmark = true\n"))
	tracks := doc.expand(true)
	if len(tracks) != 3 {
		t.Fatalf("expand = %d tracks, want 3", len(tracks))
	}
	// The explicit b.mp3 must appear once, with its custom metadata.
	found := false
	for _, tr := range tracks {
		if tr.Path == explicit {
			if found {
				t.Fatal("explicit track duplicated by dir scan")
			}
			found = true
			if tr.Title != "Custom" || !tr.Bookmark || tr.DirSourced {
				t.Fatalf("explicit track overridden: %+v", tr)
			}
		}
	}
	if !found {
		t.Fatal("explicit track missing")
	}
}

func TestPlaylistDocExpandMissingDir(t *testing.T) {
	doc := parsePlaylistDoc([]byte("[[dir]]\npath = \"/nonexistent/xyz\"\n"))
	if tracks := doc.expand(true); len(tracks) != 0 {
		t.Fatalf("missing dir contributed %d tracks", len(tracks))
	}
}

func TestCreateDirPlaylist(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))

	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	dirs, err := p.DirSources("music")
	if err != nil {
		t.Fatalf("DirSources: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Path != audio || !dirs[0].Recursive {
		t.Fatalf("dirs = %+v", dirs)
	}

	// Virtual playlists have no directory sources: both reserved names must
	// be rejected so the manager never opens the dirs screen for them.
	for _, name := range []string{history.PlaylistName, favorites.PlaylistName} {
		if dirs, err := p.DirSources(name); err == nil {
			t.Fatalf("DirSources(%q) = %+v, want reserved-name error", name, dirs)
		}
	}
	// Reject duplicate create.
	if err := p.CreateDirPlaylist("music", []string{audio}); err == nil {
		t.Fatal("duplicate create should fail")
	}
	// Reject missing directory.
	if err := p.CreateDirPlaylist("bad", []string{filepath.Join(audio, "missing")}); err == nil {
		t.Fatal("create with missing dir should fail")
	}
}

func TestAddDirSource(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))

	// Creates the playlist if needed.
	ok, err := p.AddDirSource("music", audio)
	if err != nil || !ok {
		t.Fatalf("first AddDirSource: ok=%v err=%v", ok, err)
	}
	// Same dir is deduped.
	ok, err = p.AddDirSource("music", audio)
	if err != nil || ok {
		t.Fatalf("duplicate AddDirSource: ok=%v err=%v (want ok=false)", ok, err)
	}
	dirs, _ := p.DirSources("music")
	if len(dirs) != 1 {
		t.Fatalf("dirs = %+v, want 1 entry", dirs)
	}
	// Missing dir rejected.
	if _, err := p.AddDirSource("music", filepath.Join(audio, "missing")); err == nil {
		t.Fatal("AddDirSource with missing dir should fail")
	}
}

func TestRemoveDirSource(t *testing.T) {
	p := newTestProvider(t)
	audio1 := t.TempDir()
	audio2 := t.TempDir()
	writeAudioFile(t, filepath.Join(audio1, "a.mp3"))
	writeAudioFile(t, filepath.Join(audio2, "b.mp3"))
	writeAudioFile(t, filepath.Join(audio2, "c.mp3"))

	if _, err := p.AddDirSource("music", audio1); err != nil {
		t.Fatalf("add audio1: %v", err)
	}
	if _, err := p.AddDirSource("music", audio2); err != nil {
		t.Fatalf("add audio2: %v", err)
	}
	// A directory-sourced track list between the two dirs should now hold
	// all three files.
	if tracks, err := p.Tracks("music"); err != nil || len(tracks) != 3 {
		t.Fatalf("tracks before remove = %d (err %v), want 3", len(tracks), err)
	}

	// Removing audio1 drops only its file; audio2's two remain.
	if err := p.RemoveDirSource("music", audio1); err != nil {
		t.Fatalf("RemoveDirSource: %v", err)
	}
	dirs, err := p.DirSources("music")
	if err != nil || len(dirs) != 1 || dirs[0].Path != audio2 {
		t.Fatalf("after remove dirs = %+v err %v, want only audio2", dirs, err)
	}
	tracks, err := p.Tracks("music")
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks after remove = %d (err %v), want 2", len(tracks), err)
	}

	// Removing a dir that is not referenced is a no-op (not an error).
	if err := p.RemoveDirSource("music", audio1); err != nil {
		t.Fatalf("remove missing source should be no-op, got %v", err)
	}
	// Removing from a playlist that does not exist is a no-op.
	if err := p.RemoveDirSource("nope", audio1); err != nil {
		t.Fatalf("remove from missing playlist should be no-op, got %v", err)
	}
	// The history playlist is reserved.
	if err := p.RemoveDirSource("Recently Played", audio1); err == nil {
		t.Fatal("RemoveDirSource on history should error")
	}
}

func TestSetDirRecursive(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	makeAudioTree(t, audio) // two top-level files + one nested

	if _, err := p.AddDirSource("music", audio); err != nil {
		t.Fatalf("add: %v", err)
	}
	dirs, _ := p.DirSources("music")
	if !dirs[0].Recursive {
		t.Fatalf("new dir should default to recursive, got %+v", dirs[0])
	}
	// Recursive scan sees all three files.
	if tracks, _ := p.Tracks("music"); len(tracks) != 3 {
		t.Fatalf("recursive tracks = %d, want 3", len(tracks))
	}

	// Flip to flat: only the two top-level files remain.
	if err := p.SetDirRecursive("music", audio, false); err != nil {
		t.Fatalf("SetDirRecursive(false): %v", err)
	}
	dirs, _ = p.DirSources("music")
	if dirs[0].Recursive {
		t.Fatalf("dir should now be flat, got %+v", dirs[0])
	}
	if tracks, _ := p.Tracks("music"); len(tracks) != 2 {
		t.Fatalf("flat tracks = %d, want 2", len(tracks))
	}

	// Flip back to recursive.
	if err := p.SetDirRecursive("music", audio, true); err != nil {
		t.Fatalf("SetDirRecursive(true): %v", err)
	}
	if tracks, _ := p.Tracks("music"); len(tracks) != 3 {
		t.Fatalf("re-enabled recursive tracks = %d, want 3", len(tracks))
	}

	// Setting the same value is a no-op (no error, no change).
	if err := p.SetDirRecursive("music", audio, true); err != nil {
		t.Fatalf("idempotent SetDirRecursive: %v", err)
	}
	// Missing source and missing playlist are no-ops.
	if err := p.SetDirRecursive("music", t.TempDir(), false); err != nil {
		t.Fatalf("missing source should be no-op, got %v", err)
	}
	if err := p.SetDirRecursive("nope", audio, false); err != nil {
		t.Fatalf("missing playlist should be no-op, got %v", err)
	}
	// History is reserved.
	if err := p.SetDirRecursive("Recently Played", audio, false); err == nil {
		t.Fatal("SetDirRecursive on history should error")
	}
}

func TestDirIndexByPathMatchesTildeAndAbsolute(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	audio := filepath.Join(home, "Music")
	doc := parsePlaylistDoc([]byte("[[dir]]\npath = \"~/Music\"\n"))
	if got := dirIndexByPath(doc, "~/Music"); got != 0 {
		t.Fatalf("dirIndexByPath ~/Music = %d, want 0", got)
	}
	if got := dirIndexByPath(doc, audio); got != 0 {
		t.Fatalf("dirIndexByPath absolute = %d, want 0 (tilde should match absolute)", got)
	}
	if got := dirIndexByPath(doc, "/elsewhere"); got != -1 {
		t.Fatalf("dirIndexByPath miss = %d, want -1", got)
	}
}

func TestPlaylistsDirSourceCount(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	writeAudioFile(t, filepath.Join(audio, "b.flac"))

	if _, err := p.AddDirSource("music", audio); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A second playlist with no dirs for contrast.
	if _, err := p.CreatePlaylist(context.Background(), "plain"); err != nil {
		t.Fatalf("create plain: %v", err)
	}

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	byName := map[string]playlist.PlaylistInfo{}
	for _, l := range lists {
		byName[l.Name] = l
	}
	if byName["music"].DirSourceCount != 1 {
		t.Fatalf("music DirSourceCount = %d, want 1", byName["music"].DirSourceCount)
	}
	if byName["plain"].DirSourceCount != 0 {
		t.Fatalf("plain DirSourceCount = %d, want 0", byName["plain"].DirSourceCount)
	}
	if byName["music"].TrackCount != 2 {
		t.Fatalf("music TrackCount = %d, want 2", byName["music"].TrackCount)
	}
}

func TestSavePlaylistPreservesDirsAndSkipsDirTracks(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	explicit := filepath.Join(audio, "b.mp3")
	writeAudioFile(t, explicit)

	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	tracks := []playlist.Track{
		{Path: explicit, Title: "B"},
		// Simulated dir-sourced track that must not be persisted.
		{Path: filepath.Join(audio, "a.mp3"), Title: "A", DirSourced: true},
	}
	if err := p.SavePlaylist("music", tracks); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(p.dir, "music.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Assert on the parsed document rather than raw text: the writer escapes
	// backslashes (%q), so a substring match would break on Windows paths.
	doc := parsePlaylistDoc(data)
	if len(doc.dirs) != 1 || doc.dirs[0].Path != audio {
		t.Fatalf("[[dir]] section lost or wrong: %+v", doc.dirs)
	}
	if len(doc.tracks) != 1 || doc.tracks[0].Path != explicit {
		t.Fatalf("explicit track lost or wrong: %+v", doc.tracks)
	}
}

func TestTracksExpandsDirs(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	makeAudioTree(t, audio)
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	tracks, err := p.Tracks("music")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("Tracks = %d, want 3", len(tracks))
	}
}

func TestPlaylistsCountIncludesDirTracks(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	makeAudioTree(t, audio)
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	found := false
	for _, pl := range lists {
		if pl.Name == "music" {
			found = true
			if pl.TrackCount != 3 {
				t.Fatalf("music TrackCount = %d, want 3", pl.TrackCount)
			}
		}
	}
	if !found {
		t.Fatal("music playlist missing from Playlists()")
	}
}

func TestSetBookmarkMaterializesDirTrack(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}

	if err := p.SetBookmark("music", 0); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	tracks, err := p.Tracks("music")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || !tracks[0].Bookmark {
		t.Fatalf("bookmark not persisted: %+v", tracks)
	}
	if tracks[0].DirSourced {
		t.Fatal("bookmarked track should be materialized (DirSourced=false)")
	}
	// The materialized track must now be an explicit [[track]] entry.
	data, _ := os.ReadFile(filepath.Join(p.dir, "music.toml"))
	if strings.Count(string(data), "[[track]]") != 1 {
		t.Fatalf("materialized track not written as [[track]]:\n%s", data)
	}
}

func TestSavePlaylistReadErrorAbortsRewrite(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	// Replace the playlist file with a directory so ReadFile fails (EISDIR).
	path := filepath.Join(p.dir, "music.toml")
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// A failed read must abort the rewrite instead of replacing the playlist
	// with a copy missing its [[dir]] sections.
	if err := p.SetBookmark("music", 0); err == nil {
		t.Fatal("SetBookmark should fail when the playlist cannot be read")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("playlist path removed: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("playlist was rewritten despite read failure")
	}
}

func TestSetBookmarkByPathMaterializesDirTrack(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	trackPath := filepath.Join(audio, "a.mp3")
	if err := p.SetBookmarkByPath("music", trackPath); err != nil {
		t.Fatalf("SetBookmarkByPath: %v", err)
	}
	tracks, err := p.Tracks("music")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || !tracks[0].Bookmark || tracks[0].DirSourced {
		t.Fatalf("materialized bookmark failed: %+v", tracks)
	}
}

func TestRemoveTrackDirSourcedError(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	if err := p.RemoveTrack("music", 0); err == nil {
		t.Fatal("removing a dir-sourced track should error")
	}
	// The playlist file must be untouched.
	tracks, err := p.Tracks("music")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("playlist changed after rejected removal: %+v err=%v", tracks, err)
	}
}

func TestRemoveTrackExplicitAfterDir(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	// An explicit track outside the scanned directory is not shadowed.
	explicit := filepath.Join(t.TempDir(), "b.mp3")
	writeAudioFile(t, explicit)
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	if _, _, err := p.AddTracks("music", []playlist.Track{{Path: explicit, Title: "B"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	// Expanded order: a.mp3 (dir), b.mp3 (explicit). Removing index 1 must
	// drop the explicit track but keep the dir source.
	if err := p.RemoveTrack("music", 1); err != nil {
		t.Fatalf("RemoveTrack: %v", err)
	}
	dirs, _ := p.DirSources("music")
	if len(dirs) != 1 {
		t.Fatalf("dir source removed: %+v", dirs)
	}
	tracks, err := p.Tracks("music")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("after removal tracks = %+v err=%v", tracks, err)
	}
	if tracks[0].Path == explicit {
		t.Fatal("explicit track still present")
	}
}

func TestAddTracksSkipsDirSourcedPaths(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	added, skipped, err := p.AddTracks("music", []playlist.Track{{Path: filepath.Join(audio, "a.mp3")}})
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	if added != 0 || skipped != 1 {
		t.Fatalf("added=%d skipped=%d, want 0/1", added, skipped)
	}
}

func TestAddTracksPersistsCrossPlaylistDirTrack(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("src", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	srcTracks, err := p.Tracks("src")
	if err != nil || len(srcTracks) != 1 || !srcTracks[0].DirSourced {
		t.Fatalf("src tracks = %+v err=%v, want one dir-sourced track", srcTracks, err)
	}
	if _, err := p.CreatePlaylist(context.Background(), "dst"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	// Adding a dir-sourced track to another playlist must persist it as an
	// explicit [[track]] entry instead of counting it as added and dropping it.
	added, skipped, err := p.AddTracks("dst", srcTracks)
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d, want 1/0", added, skipped)
	}
	dstTracks, err := p.Tracks("dst")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(dstTracks) != 1 || dstTracks[0].Path != srcTracks[0].Path {
		t.Fatalf("dst tracks = %+v, want the transferred track", dstTracks)
	}
	if dstTracks[0].DirSourced {
		t.Fatal("transferred track must be persisted as an explicit [[track]]")
	}
	data, _ := os.ReadFile(filepath.Join(p.dir, "dst.toml"))
	if strings.Count(string(data), "[[track]]") != 1 {
		t.Fatalf("transferred track not written as [[track]]:\n%s", data)
	}
}

func TestWriteDirRoundTrip(t *testing.T) {
	var b strings.Builder
	writeDir(&b, playlist.DirSource{Path: "/music", Recursive: true})
	writeDir(&b, playlist.DirSource{Path: "/other", Recursive: false})
	doc := parsePlaylistDoc([]byte(b.String()))
	if len(doc.dirs) != 2 {
		t.Fatalf("round trip dirs = %d", len(doc.dirs))
	}
	if doc.dirs[0].Path != "/music" || !doc.dirs[0].Recursive {
		t.Fatalf("dir 0 = %+v", doc.dirs[0])
	}
	if doc.dirs[1].Path != "/other" || doc.dirs[1].Recursive {
		t.Fatalf("dir 1 = %+v", doc.dirs[1])
	}
}

// quote returns a double-quoted Go string literal for use in TOML fixtures.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// writeInterleavedDoc writes a hand-authored mix.toml with the order
// [[track]] x, [[dir]] dirA, [[track]] y, [[dir]] dirB so rewrites must not
// flatten the interleaving.
func writeInterleavedDoc(t *testing.T, p *Provider, x, dirA, y, dirB string) {
	t.Helper()
	doc := "[[track]]\npath = " + quote(x) + "\n\n" +
		"[[dir]]\npath = " + quote(dirA) + "\n\n" +
		"[[track]]\npath = " + quote(y) + "\n\n" +
		"[[dir]]\npath = " + quote(dirB) + "\n"
	if err := os.WriteFile(filepath.Join(p.dir, "mix.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("writing mix.toml: %v", err)
	}
}

func checkOrder(t *testing.T, tracks []playlist.Track, want []string) {
	t.Helper()
	if len(tracks) != len(want) {
		got := make([]string, len(tracks))
		for i, tr := range tracks {
			got[i] = filepath.Base(tr.Path)
		}
		t.Fatalf("tracks = %v, want %v", got, want)
	}
	for i, w := range want {
		if filepath.Base(tracks[i].Path) != w {
			got := make([]string, len(tracks))
			for j, tr := range tracks {
				got[j] = filepath.Base(tr.Path)
			}
			t.Fatalf("tracks = %v, want %v (index %d)", got, want, i)
		}
	}
}

func TestSavePlaylistPreservesSectionOrder(t *testing.T) {
	p := newTestProvider(t)
	dirA := t.TempDir()
	writeAudioFile(t, filepath.Join(dirA, "a.mp3"))
	dirB := t.TempDir()
	writeAudioFile(t, filepath.Join(dirB, "b.mp3"))
	writeAudioFile(t, filepath.Join(dirB, "c.ogg"))
	x := filepath.Join(t.TempDir(), "x.mp3")
	y := filepath.Join(t.TempDir(), "y.mp3")
	writeAudioFile(t, x)
	writeAudioFile(t, y)
	writeInterleavedDoc(t, p, x, dirA, y, dirB)

	// Expanded order follows the document interleaving.
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	checkOrder(t, tracks, []string{"x.mp3", "a.mp3", "y.mp3", "b.mp3", "c.ogg"})

	// Removing x must drop only its slot: y stays anchored before dirB.
	if err := p.RemoveTrack("mix", 0); err != nil {
		t.Fatalf("RemoveTrack: %v", err)
	}
	tracks, err = p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	checkOrder(t, tracks, []string{"a.mp3", "y.mp3", "b.mp3", "c.ogg"})

	// A metadata update (enrich-style) keeps y in place.
	var yt *playlist.Track
	for i := range tracks {
		if tracks[i].Path == y {
			yt = &tracks[i]
		}
	}
	if yt == nil {
		t.Fatal("y missing after removal")
	}
	yt.Album = "Studio"
	if err := p.SavePlaylist("mix", tracks); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	tracks, err = p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	checkOrder(t, tracks, []string{"a.mp3", "y.mp3", "b.mp3", "c.ogg"})
	for _, tr := range tracks {
		if tr.Path == y && tr.Album != "Studio" {
			t.Fatalf("metadata update lost: %+v", tr)
		}
	}
}

func TestSetBookmarkKeepsDirPosition(t *testing.T) {
	p := newTestProvider(t)
	dirA := t.TempDir()
	writeAudioFile(t, filepath.Join(dirA, "a.mp3"))
	dirB := t.TempDir()
	writeAudioFile(t, filepath.Join(dirB, "b.mp3"))
	writeAudioFile(t, filepath.Join(dirB, "c.ogg"))
	x := filepath.Join(t.TempDir(), "x.mp3")
	y := filepath.Join(t.TempDir(), "y.mp3")
	writeAudioFile(t, x)
	writeAudioFile(t, y)
	writeInterleavedDoc(t, p, x, dirA, y, dirB)

	// Expanded: x, a, y, b, c. Bookmark b (index 3).
	if err := p.SetBookmark("mix", 3); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	// The materialized track stays at b's position instead of jumping around.
	checkOrder(t, tracks, []string{"x.mp3", "a.mp3", "y.mp3", "b.mp3", "c.ogg"})
	for _, tr := range tracks {
		if tr.Path == filepath.Join(dirB, "b.mp3") {
			if !tr.Bookmark || tr.DirSourced {
				t.Fatalf("materialized bookmark wrong: %+v", tr)
			}
		}
	}
}

func TestSavePlaylistReorderKeepsDirSlots(t *testing.T) {
	p := newTestProvider(t)
	dirA := t.TempDir()
	writeAudioFile(t, filepath.Join(dirA, "a.mp3"))
	dirB := t.TempDir()
	writeAudioFile(t, filepath.Join(dirB, "b.mp3"))
	writeAudioFile(t, filepath.Join(dirB, "c.ogg"))
	x := filepath.Join(t.TempDir(), "x.mp3")
	y := filepath.Join(t.TempDir(), "y.mp3")
	writeAudioFile(t, x)
	writeAudioFile(t, y)
	writeInterleavedDoc(t, p, x, dirA, y, dirB)

	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	// Reorder so y leads: [y, x, a, b, c].
	reordered := []playlist.Track{tracks[2], tracks[0], tracks[1], tracks[3], tracks[4]}
	if err := p.SavePlaylist("mix", reordered); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	tracks, err = p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	checkOrder(t, tracks, []string{"y.mp3", "a.mp3", "x.mp3", "b.mp3", "c.ogg"})
}

func TestSavePlaylistMultiMaterializedKeepsDirPositions(t *testing.T) {
	p := newTestProvider(t)
	dirA := t.TempDir()
	writeAudioFile(t, filepath.Join(dirA, "a.mp3"))
	dirB := t.TempDir()
	writeAudioFile(t, filepath.Join(dirB, "b.mp3"))
	x := filepath.Join(t.TempDir(), "x.mp3")
	writeAudioFile(t, x)
	// Document: [[dir A]], [[track x]], [[dir B]].
	doc := "[[dir]]\npath = " + quote(dirA) + "\n\n" +
		"[[track]]\npath = " + quote(x) + "\n\n" +
		"[[dir]]\npath = " + quote(dirB) + "\n"
	if err := os.WriteFile(filepath.Join(p.dir, "mix.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("writing mix.toml: %v", err)
	}
	// Both dir tracks are materialized bookmarks handed back out of document
	// order, as a TUI reorder after bookmarking would. The first leftover
	// (dirA) is inserted last in reverse order and shifts the dirB section, so
	// the dirB leftover must not land before x.
	if err := p.SavePlaylist("mix", []playlist.Track{
		{Path: x, Title: "X"},
		{Path: filepath.Join(dirB, "b.mp3"), Title: "B", Bookmark: true},
		{Path: filepath.Join(dirA, "a.mp3"), Title: "A", Bookmark: true},
	}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	checkOrder(t, tracks, []string{"a.mp3", "x.mp3", "b.mp3"})
}

func TestDirSuppliesFile(t *testing.T) {
	dir := t.TempDir()
	rec := playlist.DirSource{Path: dir, Recursive: true}
	nonRec := playlist.DirSource{Path: dir, Recursive: false}
	tests := []struct {
		name string
		src  playlist.DirSource
		file string
		want bool
	}{
		{name: "audio under recursive dir", src: rec, file: filepath.Join(dir, "song.mp3"), want: true},
		{name: "audio at non-recursive top level", src: nonRec, file: filepath.Join(dir, "song.mp3"), want: true},
		{name: "non-audio under recursive dir", src: rec, file: filepath.Join(dir, "cover.jpg"), want: false},
		{name: "non-audio uppercase extension", src: rec, file: filepath.Join(dir, "cover.JPG"), want: false},
		{name: "extension-less file", src: rec, file: filepath.Join(dir, "noextension"), want: false},
		{name: "audio in subdir of non-recursive dir", src: nonRec, file: filepath.Join(dir, "sub", "song.mp3"), want: false},
		{name: "audio outside dir", src: rec, file: filepath.Join(dir, "..", "outside.mp3"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirSuppliesFile(tt.src, tt.file); got != tt.want {
				t.Fatalf("dirSuppliesFile(%+v, %q) = %v, want %v", tt.src, tt.file, got, tt.want)
			}
		})
	}
}

func TestSavePlaylistNonAudioTrackAppendedNotPlacedBeforeDir(t *testing.T) {
	p := newTestProvider(t)
	dirA := t.TempDir()
	writeAudioFile(t, filepath.Join(dirA, "a.mp3"))
	cover := filepath.Join(dirA, "cover.jpg")
	if err := p.CreateDirPlaylist("mix", []string{dirA}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	// A new explicit track for a non-audio file that lives under the directory
	// must not be treated as dir-supplied; it belongs at the end.
	added, skipped, err := p.AddTracks("mix", []playlist.Track{{Path: cover, Title: "Cover"}})
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d, want 1/0", added, skipped)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	// Expanded order: a.mp3 (dir), then cover.jpg (explicit, appended at end).
	checkOrder(t, tracks, []string{"a.mp3", "cover.jpg"})
}

func TestAddDirSourcesAtomic(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("mix", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}

	// A batch with one missing directory must fail without persisting the
	// valid one.
	valid := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := p.AddDirSources("mix", []string{valid, missing}); err == nil {
		t.Fatal("batch with missing dir should fail")
	}
	dirs, err := p.DirSources("mix")
	if err != nil {
		t.Fatalf("DirSources: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("partial batch persisted: dirs = %+v", dirs)
	}

	// A failing batch on a new playlist must not create the file.
	if _, err := p.AddDirSources("ghost", []string{missing}); err == nil {
		t.Fatal("batch with missing dir should fail")
	}
	if p.Exists("ghost") {
		t.Fatal("failed batch created a playlist")
	}

	// Dedupe: batch of already-present + new adds only the new one.
	added, err := p.AddDirSources("mix", []string{audio, valid})
	if err != nil {
		t.Fatalf("AddDirSources: %v", err)
	}
	if len(added) != 1 || added[0] != valid {
		t.Fatalf("added = %v, want [%s]", added, valid)
	}
	dirs, _ = p.DirSources("mix")
	if len(dirs) != 2 {
		t.Fatalf("dirs = %+v, want 2", dirs)
	}
}

func TestCreateDirPlaylistNoFileOnInvalidDir(t *testing.T) {
	p := newTestProvider(t)
	valid := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	if err := p.CreateDirPlaylist("mix", []string{valid, missing}); err == nil {
		t.Fatal("create with missing dir should fail")
	}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial playlist created: %v", entries)
	}
}

func TestPlaylistDocumentRoundTripPreservesDirs(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))

	if err := p.CreateDirPlaylist("mix", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}

	snapshot, err := p.PlaylistDocument("mix")
	if err != nil {
		t.Fatalf("PlaylistDocument: %v", err)
	}
	if doc := parsePlaylistDoc(snapshot); len(doc.dirs) != 1 {
		t.Fatalf("snapshot missing [[dir]]: %+v", doc)
	}

	if err := p.DeletePlaylist("mix"); err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.dir, "mix.toml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("playlist file should be gone after delete, stat err = %v", err)
	}

	if err := p.RestorePlaylistDocument("mix", snapshot); err != nil {
		t.Fatalf("RestorePlaylistDocument: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(p.dir, "mix.toml"))
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(restored) != string(snapshot) {
		t.Fatal("restored document differs from the snapshot")
	}
	doc := parsePlaylistDoc(restored)
	if len(doc.dirs) != 1 || doc.dirs[0].Path != audio || doc.dirs[0].Recursive != true {
		t.Fatalf("[[dir]] source lost in restore: %+v", doc.dirs)
	}
}

func TestRestorePlaylistDocumentRejectsHistory(t *testing.T) {
	p := newTestProviderWithHistory(t)
	if err := p.RestorePlaylistDocument(history.PlaylistName, []byte("[[track]]\n")); err == nil {
		t.Fatal("expected an error restoring over the reserved history name")
	}
}

func TestPlaylistsMigratesLegacyFavoritesToml(t *testing.T) {
	p := newTestProvider(t)
	src := filepath.Join(p.dir, "Favorites.toml")
	if err := os.WriteFile(src, []byte("[[track]]\npath = \"/a.mp3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}

	// Original must be gone.
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("Favorites.toml should have been migrated away")
	}

	// Renamed file must appear.
	dst := filepath.Join(p.dir, favoritesLegacyName+".toml")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("migrated file missing: %v", err)
	}

	found := false
	for _, l := range lists {
		if l.ID == favoritesLegacyName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("migrated playlist not listed; got %v", lists)
	}
}

func TestPlaylistsSkipsMigrationWhenDestExists(t *testing.T) {
	p := newTestProvider(t)
	src := filepath.Join(p.dir, "Favorites.toml")
	dst := filepath.Join(p.dir, favoritesLegacyName+".toml")
	if err := os.WriteFile(src, []byte("[[track]]\npath = \"/a.mp3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("[[track]]\npath = \"/b.mp3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Playlists(); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	// Source must NOT be clobbered when destination already exists.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("Favorites.toml should remain when dest already exists: %v", err)
	}
}
