package resolve

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsBilibiliMedialist(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "www medialist", raw: "https://www.bilibili.com/medialist/detail/ml12345", want: true},
		{name: "bare bilibili host", raw: "https://bilibili.com/medialist/detail/ml12345?type=2", want: true},
		{name: "mobile subdomain mixed case", raw: "https://m.bilibili.com/MEDIALIST/DETAIL/ml99", want: true},
		{name: "http scheme", raw: "http://www.bilibili.com/medialist/detail/ml1", want: true},
		{name: "lookalike suffix host rejected", raw: "https://evilbilibili.com/medialist/detail/ml123", want: false},
		{name: "single video path rejected", raw: "https://www.bilibili.com/video/BV1xx411c7mD", want: false},
		{name: "space series path rejected", raw: "https://space.bilibili.com/12345/lists/678", want: false},
		{name: "non-http scheme rejected", raw: "ftp://www.bilibili.com/medialist/detail/ml123", want: false},
		{name: "malformed url rejected", raw: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBilibiliMedialist(tt.raw); got != tt.want {
				t.Errorf("isBilibiliMedialist(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveYTDLBilibiliFavoriteMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"" + logFile + "\"\n" +
		"case \"$*\" in\n" +
		"*--flat-playlist*) echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1xx\",\"title\":\"BV1xx\",\"uploader\":\"Playlist owner\",\"duration\":123}' ;;\n" +
		"*) echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1xx\",\"title\":\"Actual title\",\"uploader\":\"Video uploader\",\"duration\":123}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Bilibili medialist omits --flat-playlist and passes --ignore-errors.
	tracks, err := ResolveYTDLBatch("https://www.bilibili.com/medialist/detail/ml123", 0, 0, "")
	if err != nil {
		t.Fatalf("ResolveYTDLBatch(medialist) error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Actual title" || tracks[0].Artist != "Video uploader" {
		t.Fatalf("medialist tracks = %+v, want Actual title / Video uploader", tracks)
	}

	// Non-medialist URLs still pass --flat-playlist and omit --ignore-errors.
	otherTracks, err := ResolveYTDLBatch("https://www.bilibili.com/video/BV1xx", 0, 0, "")
	if err != nil {
		t.Fatalf("ResolveYTDLBatch(video) error = %v", err)
	}
	if len(otherTracks) != 1 || otherTracks[0].Title != "BV1xx" {
		t.Fatalf("video tracks = %+v, want flat entry", otherTracks)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("invocations = %d, want 2: %q", len(lines), string(logBytes))
	}
	if strings.Contains(lines[0], "--flat-playlist") || !strings.Contains(lines[0], "--ignore-errors") {
		t.Errorf("medialist args = %q, want no --flat-playlist and --ignore-errors", lines[0])
	}
	if !strings.Contains(lines[1], "--flat-playlist") || strings.Contains(lines[1], "--ignore-errors") {
		t.Errorf("video args = %q, want --flat-playlist and no --ignore-errors", lines[1])
	}
}

func TestResolveYTDLBilibiliPartialFailureScoped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}

	tmpDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1xx\",\"title\":\"Playable\",\"uploader\":\"Uploader\",\"duration\":123}'\n" +
		"echo 'entry unavailable' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Bilibili medialist tolerates a failed favorite and keeps parsed tracks.
	tracks, err := ResolveYTDLBatch("https://www.bilibili.com/medialist/detail/ml123", 0, 0, "")
	if err != nil {
		t.Fatalf("medialist partial failure returned error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Playable" {
		t.Fatalf("medialist tracks = %+v, want Playable", tracks)
	}

	// Non-medialist callers must still fail on nonzero yt-dlp exit so paginated
	// callers do not cache truncated playlists.
	if _, err := ResolveYTDLBatch("https://music.youtube.com/playlist?list=PL123", 0, 100, ""); err == nil {
		t.Fatal("non-medialist nonzero exit succeeded, want error")
	}
}

func TestResolveYTDLBilibiliWindowedMultipart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"" + logFile + "\"\n" +
		"case \"$*\" in\n" +
		"*--flat-playlist*)\n" +
		"  echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1multi\",\"title\":\"BV1multi\",\"uploader\":\"ListOwner\",\"duration\":300}'\n" +
		"  ;;\n" +
		"*)\n" +
		"  echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1multi?p=1\",\"title\":\"Part 1\",\"uploader\":\"Creator\",\"duration\":150}'\n" +
		"  echo '{\"webpage_url\":\"https://www.bilibili.com/video/BV1multi?p=2\",\"title\":\"Part 2\",\"uploader\":\"Creator\",\"duration\":150}'\n" +
		"  ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tracks, entries, err := ResolveYTDLBatchPageContext(context.Background(), "https://www.bilibili.com/medialist/detail/ml123", 20, 20, "")
	if err != nil {
		t.Fatalf("ResolveYTDLBatchPageContext() error = %v", err)
	}
	if entries != 1 {
		t.Fatalf("entries = %d, want 1 outer entry", entries)
	}
	if len(tracks) != 2 || tracks[0].Title != "Part 1" || tracks[1].Title != "Part 2" {
		t.Fatalf("tracks = %+v, want both multipart entries", tracks)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("invocations = %d, want 2: %q", len(lines), string(logBytes))
	}
	if !strings.Contains(lines[0], "--flat-playlist") || !strings.Contains(lines[0], "--playlist-start 21") || !strings.Contains(lines[0], "--playlist-end 40") {
		t.Errorf("step 1 args = %q, want flat range 21..40", lines[0])
	}
	if strings.Contains(lines[1], "--playlist-start") || strings.Contains(lines[1], "--playlist-end") {
		t.Errorf("step 2 args = %q, must not pass playlist range flags to multipart expansion", lines[1])
	}
	if !strings.Contains(lines[1], "https://www.bilibili.com/video/BV1multi") {
		t.Errorf("step 2 args = %q, want selected outer URL", lines[1])
	}
}
