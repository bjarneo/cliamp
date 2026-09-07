package resolve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCollectAudioFilesSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := CollectAudioFiles(path)
	if err != nil {
		t.Fatalf("CollectAudioFiles: %v", err)
	}
	if len(got) != 1 || got[0] != path {
		t.Errorf("got %v, want [%s]", got, path)
	}
}

func TestCollectAudioFilesUnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := CollectAudioFiles(path)
	if err != nil {
		t.Fatalf("CollectAudioFiles: %v", err)
	}
	if got != nil {
		t.Errorf("non-audio file should return nil, got %v", got)
	}
}

func TestCollectAudioFilesDirectoryWalk(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "a.mp3"),
		filepath.Join(dir, "nested", "b.flac"),
		filepath.Join(dir, "README.txt"), // ignored
		filepath.Join(dir, "nested", "c.wav"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	got, err := CollectAudioFiles(dir)
	if err != nil {
		t.Fatalf("CollectAudioFiles: %v", err)
	}
	// Expect 3 audio files, sorted.
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3 — paths=%v", len(got), got)
	}
	// slices.IsSorted confirms deterministic order.
	if !slices.IsSorted(got) {
		t.Errorf("files not sorted: %v", got)
	}
	// README should not be in the result.
	for _, p := range got {
		if strings.HasSuffix(p, "README.txt") {
			t.Errorf("README included in audio files: %v", got)
		}
	}
}

func TestCollectAudioFilesMissing(t *testing.T) {
	_, err := CollectAudioFiles(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Error("CollectAudioFiles on missing path should error")
	}
}

func TestArgsLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Args([]string{path})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(got.Tracks))
	}
	if got.Tracks[0].Path != path {
		t.Errorf("Path = %q, want %q", got.Tracks[0].Path, path)
	}
	if len(got.Pending) != 0 {
		t.Errorf("Pending = %v, want empty", got.Pending)
	}
}

func TestArgsLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.mp3", "b.flac", "c.ogg"}
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	got, err := Args([]string{dir})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3", len(got.Tracks))
	}
	// Results preserve on-disk sort order (CollectAudioFiles sorts).
	names := make([]string, len(got.Tracks))
	for i, tr := range got.Tracks {
		names[i] = filepath.Base(tr.Path)
	}
	if !slices.IsSorted(names) {
		t.Errorf("tracks not sorted: %v", names)
	}
}

func TestArgsRemoteFeedURLGoesToPending(t *testing.T) {
	// .xml URL is recognised as a feed.
	feedURL := "https://example.com/podcast.xml"
	got, err := Args([]string{feedURL})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Pending) != 1 || got.Pending[0] != feedURL {
		t.Errorf("Pending = %v, want [%s]", got.Pending, feedURL)
	}
	if len(got.Tracks) != 0 {
		t.Errorf("Tracks should be empty, got %+v", got.Tracks)
	}
}

func TestArgsRemoteAudioURLBecomesTrack(t *testing.T) {
	audioURL := "https://stream.example.com/song.mp3"
	got, err := Args([]string{audioURL})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(got.Tracks))
	}
	if got.Tracks[0].Path != audioURL {
		t.Errorf("Path = %q, want %q", got.Tracks[0].Path, audioURL)
	}
}

func TestURLResolvesRawStream(t *testing.T) {
	tracks, err := URL("https://example.com/live.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Path != "https://example.com/live.mp3" {
		t.Fatalf("tracks = %#v", tracks)
	}
}

func TestArgsLocalM3U(t *testing.T) {
	dir := t.TempDir()
	// Write a local m3u that points at a dummy audio file.
	audio := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(audio, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m3u := filepath.Join(dir, "pl.m3u")
	content := `#EXTM3U
#EXTINF:120,My Song
song.mp3
`
	if err := os.WriteFile(m3u, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Args([]string{m3u})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(got.Tracks))
	}
	if got.Tracks[0].Title != "My Song" {
		t.Errorf("Title = %q, want My Song", got.Tracks[0].Title)
	}
}

func TestArgsLocalPLS(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(audio, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	pls := filepath.Join(dir, "pl.pls")
	content := "[playlist]\nFile1=" + audio + "\nTitle1=Track 1\nLength1=60\nNumberOfEntries=1\nVersion=2\n"
	if err := os.WriteFile(pls, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Args([]string{pls})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(got.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(got.Tracks))
	}
	if got.Tracks[0].Title != "Track 1" {
		t.Errorf("Title = %q, want Track 1", got.Tracks[0].Title)
	}
}

func TestArgsMissingFileErrors(t *testing.T) {
	// CollectAudioFiles stat's the path and Args wraps the os.Stat failure
	// into "scanning X: ..." — missing paths are surfaced, not swallowed.
	path := filepath.Join(t.TempDir(), "ghost.mp3")
	_, err := Args([]string{path})
	if err == nil {
		t.Error("Args for missing file should error")
	}
	if !strings.Contains(err.Error(), "scanning") {
		t.Errorf("error = %q, want to mention 'scanning'", err.Error())
	}
}

func TestScanTracksEmpty(t *testing.T) {
	got := scanTracks(nil)
	if got != nil {
		t.Errorf("scanTracks(nil) = %v, want nil", got)
	}
	got = scanTracks([]string{})
	if got != nil {
		t.Errorf("scanTracks(empty) = %v, want nil", got)
	}
}

func TestScanTracksPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 12) // more than workers = 8
	for i := range paths {
		paths[i] = filepath.Join(dir, "a.mp3")
	}

	tracks := scanTracks(paths)
	if len(tracks) != len(paths) {
		t.Fatalf("len = %d, want %d", len(tracks), len(paths))
	}
	for i, tr := range tracks {
		if tr.Path != paths[i] {
			t.Errorf("tracks[%d].Path = %q, want %q", i, tr.Path, paths[i])
		}
	}
}

// TestRemoteM3UKeepsFileOrder checks that expansion is order-preserving, which
// is what makes the startup playlist match the order cliamp renders. A sorted
// or reordered expansion would silently diverge from the pane.
func TestRemoteM3UKeepsFileOrder(t *testing.T) {
	const body = `#EXTM3U
#EXTINF:-1,Zulu
http://example.invalid/zulu/stream
#EXTINF:-1,Alpha
http://example.invalid/alpha/stream
#EXTINF:-1,Mike
http://example.invalid/mike/stream
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	tracks, err := Remote([]string{srv.URL + "/streams.m3u"})
	if err != nil {
		t.Fatalf("Remote error = %v", err)
	}

	want := []string{"Zulu", "Alpha", "Mike"}
	if len(tracks) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(want))
	}
	for i, title := range want {
		if tracks[i].Title != title {
			t.Fatalf("track %d = %q, want %q (file order must survive)", i+1, tracks[i].Title, title)
		}
		if !tracks[i].Stream || !tracks[i].Realtime {
			t.Errorf("track %q: Stream=%v Realtime=%v, want both true for a live stream",
				title, tracks[i].Stream, tracks[i].Realtime)
		}
	}
}
