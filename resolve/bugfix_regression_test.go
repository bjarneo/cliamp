package resolve

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseXiaoyuzhouSchemaJSONPreservesHTMLEntitiesInRawScript(t *testing.T) {
	doc := `<!doctype html><html><head>
<script name="schema:podcast-show" type="application/ld+json">
{"name":"Episode &quot;One&quot;","timeRequired":"PT12M30S","associatedMedia":{"contentUrl":"https://cdn.example.com/ep1.m4a"},"partOfSeries":{"name":"Show &quot;A&quot;"}}
</script>
</head></html>`

	track, err := parseXiaoyuzhouEpisodeHTML("https://www.xiaoyuzhoufm.com/episode/abc", doc)
	if err != nil {
		t.Fatalf("parseXiaoyuzhouEpisodeHTML() error = %v", err)
	}
	if track.Path != "https://cdn.example.com/ep1.m4a" {
		t.Fatalf("Path = %q", track.Path)
	}
	if track.DurationSecs != 750 {
		t.Fatalf("DurationSecs = %d, want 750", track.DurationSecs)
	}
	if track.Artist == "" || track.Title == "" {
		t.Fatalf("missing metadata: %+v", track)
	}
}

func TestFetchUserPlaylistsHonorsCookiesFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixture")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "ytdl.args")
	cookieFile := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_YTDL_ARGS\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_YTDL_ARGS", argsFile)

	SetYTDLCookiesFileForHost("www.youtube.com", cookieFile)
	t.Cleanup(func() { SetYTDLCookiesFileForHost("www.youtube.com", "") })

	if _, err := FetchUserPlaylists(""); err != nil {
		t.Fatalf("FetchUserPlaylists() error = %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n--cookies\n") {
		t.Fatalf("yt-dlp args = %q, want --cookies flag", string(raw))
	}
}
