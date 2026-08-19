package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// captureStdout runs fn with os.Stdout redirected to a buffer and returns what
// was written.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	runErr := fn()
	w.Close()
	<-done
	os.Stdout = old
	return buf.String(), runErr
}

func writeAudioFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func setupTestEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestPlaylistListEmpty(t *testing.T) {
	setupTestEnv(t)

	out, err := captureStdout(t, PlaylistList)
	if err != nil {
		t.Fatalf("PlaylistList: %v", err)
	}
	if !strings.Contains(out, "No playlists") {
		t.Errorf("output = %q, want 'No playlists...'", out)
	}
}

func TestPlaylistCreateAndList(t *testing.T) {
	home := setupTestEnv(t)
	audioDir := filepath.Join(home, "music")
	writeAudioFile(t, filepath.Join(audioDir, "song1.mp3"))
	writeAudioFile(t, filepath.Join(audioDir, "song2.flac"))

	// Create
	out, err := captureStdout(t, func() error {
		return PlaylistCreate("mymix", []string{audioDir}, "", nil)
	})
	if err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	if !strings.Contains(out, "Created playlist") {
		t.Errorf("create output = %q, want 'Created playlist'", out)
	}

	// List
	out, err = captureStdout(t, PlaylistList)
	if err != nil {
		t.Fatalf("PlaylistList: %v", err)
	}
	if !strings.Contains(out, "mymix") {
		t.Errorf("list output = %q, want to mention 'mymix'", out)
	}
	if !strings.Contains(out, "2 tracks") {
		t.Errorf("list output = %q, want '2 tracks'", out)
	}
}

func TestPlaylistCreateNoAudio(t *testing.T) {
	home := setupTestEnv(t)
	emptyDir := filepath.Join(home, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	err := PlaylistCreate("nothing", []string{emptyDir}, "", nil)
	if err == nil {
		t.Fatal("PlaylistCreate with no audio should error")
	}
	if !strings.Contains(err.Error(), "no audio") {
		t.Errorf("error = %q, want to mention 'no audio'", err.Error())
	}
}

func TestPlaylistCreateEmpty(t *testing.T) {
	setupTestEnv(t)

	out, err := captureStdout(t, func() error {
		return PlaylistCreate("empty", nil, "", nil)
	})
	if err != nil {
		t.Fatalf("PlaylistCreate empty: %v", err)
	}
	if !strings.Contains(out, "Created empty playlist") {
		t.Fatalf("output = %q, want empty playlist confirmation", out)
	}
	out, err = captureStdout(t, func() error { return PlaylistShow("empty", false) })
	if err != nil {
		t.Fatalf("PlaylistShow empty: %v", err)
	}
	if !strings.Contains(out, "is empty") {
		t.Fatalf("show output = %q, want empty message", out)
	}
}

func TestPlaylistCreateDuplicate(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)

	if err := PlaylistCreate("dup", []string{audio}, "", nil); err != nil {
		t.Fatalf("first PlaylistCreate: %v", err)
	}
	err := PlaylistCreate("dup", []string{audio}, "", nil)
	if err == nil {
		t.Error("duplicate create should error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want to mention 'already exists'", err.Error())
	}
}

func TestPlaylistAddAppends(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)

	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistAdd("mix", []string{b}, nil); err != nil {
		t.Fatalf("PlaylistAdd: %v", err)
	}

	out, _ := captureStdout(t, func() error { return PlaylistShow("mix", false) })
	if !strings.Contains(out, "2 tracks") {
		t.Errorf("Show output = %q, want '2 tracks' after add", out)
	}
}

func TestPlaylistAddSkipsDuplicates(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)

	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistAdd("mix", []string{a}, nil) })
	if err != nil {
		t.Fatalf("PlaylistAdd duplicate: %v", err)
	}
	if !strings.Contains(out, "0 tracks") || !strings.Contains(out, "1 duplicate skipped") {
		t.Fatalf("output = %q, want duplicate skip count", out)
	}
}

func TestPlaylistAddNonExistent(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)

	err := PlaylistAdd("ghost", []string{a}, nil)
	if err == nil {
		t.Error("PlaylistAdd on non-existent playlist should error")
	}
}

func TestPlaylistShowEmpty(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistShow("ghost", false)
	if err == nil {
		t.Error("PlaylistShow of missing playlist should error")
	}
}

func TestPlaylistShowJSON(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	out, err := captureStdout(t, func() error { return PlaylistShow("mix", true) })
	if err != nil {
		t.Fatalf("PlaylistShow JSON: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("JSON output should start with '[': %s", out)
	}
	if !strings.Contains(out, "\"path\"") {
		t.Errorf("JSON output should contain 'path' key: %s", out)
	}
}

func TestPlaylistRemove(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)
	if err := PlaylistCreate("mix", []string{a, b}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistRemove("mix", 1); err != nil {
		t.Fatalf("PlaylistRemove: %v", err)
	}

	out, _ := captureStdout(t, func() error { return PlaylistShow("mix", false) })
	if !strings.Contains(out, "1 tracks") {
		t.Errorf("output = %q, want '1 tracks' after remove", out)
	}
}

func TestPlaylistRemoveOutOfRange(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	if err := PlaylistCreate("mix", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	err := PlaylistRemove("mix", 999)
	if err == nil {
		t.Error("PlaylistRemove with out-of-range index should error")
	}
}

func TestPlaylistDelete(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("todelete", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	if err := PlaylistDelete("todelete"); err != nil {
		t.Fatalf("PlaylistDelete: %v", err)
	}

	out, _ := captureStdout(t, PlaylistList)
	if strings.Contains(out, "todelete") {
		t.Errorf("after delete, List output still contains 'todelete': %s", out)
	}
}

func TestPlaylistDeleteMissing(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistDelete("ghost")
	if err == nil {
		t.Error("PlaylistDelete on missing playlist should error")
	}
}

func TestPlaylistRename(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	if err := PlaylistCreate("old", []string{a}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	if err := PlaylistRename("old", "new"); err != nil {
		t.Fatalf("PlaylistRename: %v", err)
	}
	if err := PlaylistShow("new", false); err != nil {
		t.Fatalf("renamed playlist missing: %v", err)
	}
	if err := PlaylistShow("old", false); err == nil {
		t.Fatal("old playlist should no longer exist")
	}
}

func TestPlaylistDedupeAndSort(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	b := filepath.Join(home, "b.mp3")
	writeAudioFile(t, a)
	writeAudioFile(t, b)
	if err := PlaylistCreate("mix", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate empty: %v", err)
	}
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("mix", []playlist.Track{{Path: b, Title: "B"}, {Path: a, Title: "A"}, {Path: a, Title: "A dup"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	if err := PlaylistDedupe("mix"); err != nil {
		t.Fatalf("PlaylistDedupe: %v", err)
	}
	if err := PlaylistSort("mix", "title"); err != nil {
		t.Fatalf("PlaylistSort: %v", err)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Title != "A" || tracks[1].Title != "B" {
		t.Fatalf("tracks after dedupe/sort = %+v", tracks)
	}
}

func TestPlaylistDoctorFixPrunesMissing(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	missing := filepath.Join(home, "missing.mp3")
	writeAudioFile(t, a)
	if err := PlaylistCreate("mix", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate empty: %v", err)
	}
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("mix", []playlist.Track{{Path: a, Title: "A"}, {Path: missing, Title: "Missing"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	if err := PlaylistDoctor("mix", true); err != nil {
		t.Fatalf("PlaylistDoctor: %v", err)
	}
	tracks, err := p.Tracks("mix")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Path != a {
		t.Fatalf("tracks after doctor = %+v", tracks)
	}
}

func TestPlaylistImportExportM3U(t *testing.T) {
	home := setupTestEnv(t)
	a := filepath.Join(home, "a.mp3")
	writeAudioFile(t, a)
	m3u := filepath.Join(home, "mix.m3u")
	if err := os.WriteFile(m3u, []byte("#EXTM3U\n#EXTINF:12,Song\na.mp3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := PlaylistImport(m3u, "imported"); err != nil {
		t.Fatalf("PlaylistImport: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistExport("imported", "m3u", "") })
	if err != nil {
		t.Fatalf("PlaylistExport: %v", err)
	}
	if !strings.Contains(out, "#EXTM3U") || !strings.Contains(out, a) {
		t.Fatalf("export output = %q, want M3U with resolved path", out)
	}
}

func TestPlaylistExportInvalidFormatDoesNotTruncateOutput(t *testing.T) {
	home := setupTestEnv(t)
	p, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("mix", []playlist.Track{{Path: "/a.mp3", Title: "A"}}); err != nil {
		t.Fatalf("SavePlaylist: %v", err)
	}
	out := filepath.Join(home, "keep.txt")
	if err := os.WriteFile(out, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := PlaylistExport("mix", "bad", out); err == nil {
		t.Fatal("PlaylistExport should reject unsupported format")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("output file = %q, want keep", string(data))
	}
}

func TestPlaylistBookmarkToggle(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	// Bookmark on.
	out, err := captureStdout(t, func() error { return PlaylistBookmark("mix", 1) })
	if err != nil {
		t.Fatalf("PlaylistBookmark: %v", err)
	}
	if !strings.Contains(out, "★") {
		t.Errorf("first bookmark output = %q, want '★'", out)
	}

	// Bookmark off.
	out, err = captureStdout(t, func() error { return PlaylistBookmark("mix", 1) })
	if err != nil {
		t.Fatalf("PlaylistBookmark toggle: %v", err)
	}
	if !strings.Contains(out, "☆") {
		t.Errorf("second bookmark output = %q, want '☆'", out)
	}
}

func TestPlaylistBookmarksEmpty(t *testing.T) {
	setupTestEnv(t)
	out, err := captureStdout(t, PlaylistBookmarks)
	if err != nil {
		t.Fatalf("PlaylistBookmarks: %v", err)
	}
	if !strings.Contains(out, "No bookmarks") {
		t.Errorf("output = %q, want 'No bookmarks...'", out)
	}
}

func TestPlaylistBookmarksShowsStars(t *testing.T) {
	home := setupTestEnv(t)
	audio := filepath.Join(home, "a.mp3")
	writeAudioFile(t, audio)
	if err := PlaylistCreate("mix", []string{audio}, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	// Bookmark (captureStdout silences the toggle output).
	_, _ = captureStdout(t, func() error { return PlaylistBookmark("mix", 1) })

	out, err := captureStdout(t, PlaylistBookmarks)
	if err != nil {
		t.Fatalf("PlaylistBookmarks: %v", err)
	}
	if !strings.Contains(out, "★") {
		t.Errorf("bookmarks output = %q, want '★'", out)
	}
}

func TestCollectLocalAudioMultiplePaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp3")
	b := filepath.Join(dir, "sub", "b.flac")
	writeAudioFile(t, a)
	writeAudioFile(t, b)

	got, err := collectLocalAudio([]string{a, b})
	if err != nil {
		t.Fatalf("collectLocalAudio: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d paths, want 2 — %v", len(got), got)
	}
}

func TestCollectLocalAudioMissingPath(t *testing.T) {
	_, err := collectLocalAudio([]string{filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Error("collectLocalAudio with missing path should error")
	}
}

func TestNewProvider(t *testing.T) {
	setupTestEnv(t)
	p, err := newProvider()
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if p == nil {
		t.Error("newProvider returned nil provider")
	}
}
