package resolve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

// fakeMix puts a yt-dlp on PATH that lists entries and logs its arguments to
// the returned file.
func fakeMix(t *testing.T, entries string) (argsLog string) {
	t.Helper()
	argsLog = filepath.Join(t.TempDir(), "args.log")
	fakeYTDL(t, `echo "$@" > "`+argsLog+`"
cat <<'JSON'
`+entries+`
JSON`)
	return argsLog
}

// The seed's Mix is listed through yt-dlp, one extra entry is asked for because
// the Mix starts with the seed, and the seed is dropped whatever URL form the
// seed track was saved with.
func TestYouTubeRelaterListsMixWithoutSeed(t *testing.T) {
	argsLog := fakeMix(t, `{"webpage_url":"https://music.youtube.com/watch?v=5NV6Rdv1a3I","title":"Get Lucky","uploader":"Daft Punk","duration":249}
{"webpage_url":"https://music.youtube.com/watch?v=NF-kLy44Hls","title":"Lose Yourself to Dance","uploader":"Daft Punk","duration":251}
{"webpage_url":"https://music.youtube.com/watch?v=Ic5vxw3eijY","title":"American Boy","uploader":"Estelle","duration":242}`)

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
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"--playlist-end 3", "https://music.youtube.com/watch?v=5NV6Rdv1a3I&list=RDAMVM5NV6Rdv1a3I"} {
		if !strings.Contains(string(args), part) {
			t.Errorf("yt-dlp args %q missing %q", args, part)
		}
	}
}

// A Mix that does not start with the seed still gives at most n songs.
func TestYouTubeRelaterCapsMixWithoutSeedAtN(t *testing.T) {
	fakeMix(t, `{"webpage_url":"https://www.youtube.com/watch?v=NF-kLy44Hls"}
{"webpage_url":"https://www.youtube.com/watch?v=Ic5vxw3eijY"}
{"webpage_url":"https://www.youtube.com/watch?v=h5EofwRzit0"}`)

	seed := playlist.Track{Path: "https://www.youtube.com/watch?v=5NV6Rdv1a3I"}
	got, err := (YouTubeRelater{}).RelatedTracks(context.Background(), seed, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("RelatedTracks = %+v, %v; want 2 songs", got, err)
	}
}

// Below one song, yt-dlp is not run. At n = -1, asking for n+1 entries would
// list the whole Mix.
func TestYouTubeRelaterAsksForNothingBelowOne(t *testing.T) {
	argsLog := fakeMix(t, `{"webpage_url":"https://www.youtube.com/watch?v=NF-kLy44Hls"}`)
	seed := playlist.Track{Path: "https://www.youtube.com/watch?v=5NV6Rdv1a3I"}
	for _, n := range []int{0, -1} {
		if got, err := (YouTubeRelater{}).RelatedTracks(context.Background(), seed, n); err != nil || len(got) != 0 {
			t.Fatalf("RelatedTracks(n=%d) = %+v, %v; want nothing", n, got, err)
		}
	}
	if _, err := os.Stat(argsLog); err == nil {
		t.Fatal("yt-dlp ran for n below 1")
	}
}

func TestYouTubeMixURL(t *testing.T) {
	got, _, _ := youTubeMix("https://youtu.be/5NV6Rdv1a3I")
	if want := "https://www.youtube.com/watch?v=5NV6Rdv1a3I&list=RD5NV6Rdv1a3I"; got != want {
		t.Fatalf("youTubeMix = %q, want %q", got, want)
	}
}
