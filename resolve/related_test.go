package resolve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestYouTubeRelaterCanRelate(t *testing.T) {
	cases := map[string]bool{
		"https://www.youtube.com/watch?v=5NV6Rdv1a3I":                              true,
		"https://youtu.be/5NV6Rdv1a3I":                                             true,
		"https://m.youtube.com/shorts/5NV6Rdv1a3I":                                 true,
		"https://music.youtube.com/watch?v=5NV6Rdv1a3I":                            true,
		"https://www.youtube.com/playlist?list=PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG": false,
		"https://www.youtube.com/watch?v=short":                                    false,
		"https://example.com/watch?v=5NV6Rdv1a3I":                                  false,
	}
	for path, want := range cases {
		if got := (YouTubeRelater{}).CanRelate(playlist.Track{Path: path}); got != want {
			t.Errorf("CanRelate(%q) = %v, want %v", path, got, want)
		}
	}
}

// The seed's Mix is listed through yt-dlp, one extra entry is asked for because
// the Mix starts with the seed, and the seed is dropped whatever URL form the
// seed track was saved with.
func TestYouTubeRelaterListsMixWithoutSeed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "args.log")
	script := `#!/bin/sh
echo "$@" > "` + logFile + `"
cat <<'JSON'
{"webpage_url":"https://music.youtube.com/watch?v=5NV6Rdv1a3I","title":"Get Lucky","uploader":"Daft Punk","duration":249}
{"webpage_url":"https://music.youtube.com/watch?v=NF-kLy44Hls","title":"Lose Yourself to Dance","uploader":"Daft Punk","duration":251}
{"webpage_url":"https://music.youtube.com/watch?v=Ic5vxw3eijY","title":"American Boy","uploader":"Estelle","duration":242}
JSON
`
	if err := os.WriteFile(filepath.Join(tmpDir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	seed := playlist.Track{Path: "https://music.youtube.com/watch?v=5NV6Rdv1a3I&si=abc"}
	got, err := (YouTubeRelater{}).RelatedTracks(context.Background(), seed, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []playlist.Track{
		{Path: "https://music.youtube.com/watch?v=NF-kLy44Hls", Title: "Lose Yourself to Dance", Artist: "Daft Punk", Stream: true, DurationSecs: 251},
		{Path: "https://music.youtube.com/watch?v=Ic5vxw3eijY", Title: "American Boy", Artist: "Estelle", Stream: true, DurationSecs: 242},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedTracks =\n%+v\nwant\n%+v", got, want)
	}
	args, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"--playlist-end 3", "https://music.youtube.com/watch?v=5NV6Rdv1a3I&list=RDAMVM5NV6Rdv1a3I"} {
		if !strings.Contains(string(args), part) {
			t.Errorf("yt-dlp args %q missing %q", args, part)
		}
	}
}

func TestYouTubeMixURL(t *testing.T) {
	got, _, _ := youTubeMix("https://youtu.be/5NV6Rdv1a3I")
	if want := "https://www.youtube.com/watch?v=5NV6Rdv1a3I&list=RD5NV6Rdv1a3I"; got != want {
		t.Fatalf("youTubeMix = %q, want %q", got, want)
	}
}
