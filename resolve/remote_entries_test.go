package resolve

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestURIScheme(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"https://example.com/a.mp3", "https"},
		{"HTTP://example.com/a.mp3", "http"},
		{"ssh://host/path/a.flac", "ssh"},
		{"file:///etc/passwd", "file"},
		{"data:audio/mp3,AAAA", "data"},
		{"javascript:alert(1)", "javascript"},
		{"ms-appx-web://x/y", "ms-appx-web"},
		// Windows drive letters are not schemes.
		{`C:\music\a.mp3`, ""},
		{`c:/music/a.mp3`, ""},
		// Plain paths and relative entries have no scheme.
		{"/home/user/a.mp3", ""},
		{"relative/a.mp3", ""},
		{"a.mp3", ""},
		{"", ""},
		{"1http://x", ""},
		{"no colon here", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := URIScheme(tt.path); got != tt.want {
				t.Errorf("URIScheme(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFilterRemoteEntries(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  []string
	}{
		{
			name:  "http and https survive",
			paths: []string{"http://a.com/1.mp3", "https://b.com/2.mp3"},
			want:  []string{"http://a.com/1.mp3", "https://b.com/2.mp3"},
		},
		{
			name:  "ssh entries are dropped",
			paths: []string{"https://a.com/1.mp3", "ssh://evil/etc/passwd"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "file and data entries are dropped",
			paths: []string{"file:///etc/passwd", "data:audio/mp3,AAAA", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name: "scheme-less entries are preserved unchanged",
			// These predate the filter and cannot name a transport, so
			// dropping them would change what users see.
			paths: []string{"relative/a.mp3", "/abs/b.mp3", `C:\music\c.mp3`},
			want:  []string{"relative/a.mp3", "/abs/b.mp3", `C:\music\c.mp3`},
		},
		{
			name:  "every entry dropped yields an empty list",
			paths: []string{"ssh://a/b", "file:///c"},
			want:  []string{},
		},
		{
			name:  "empty input",
			paths: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracks := make([]playlist.Track, 0, len(tt.paths))
			for _, p := range tt.paths {
				tracks = append(tracks, playlist.Track{Path: p})
			}
			got := filterRemoteEntries(tracks)
			if len(got) != len(tt.want) {
				t.Fatalf("filterRemoteEntries() returned %d tracks, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i].Path != w {
					t.Errorf("track %d path = %q, want %q", i, got[i].Path, w)
				}
			}
		})
	}
}

// buildTaggedWAV returns minimal WAV bytes with a LIST/INFO INAM title tag.
func buildTaggedWAV(title string) []byte {
	name := append([]byte(title), 0)
	if len(name)%2 != 0 {
		name = append(name, 0)
	}
	inam := make([]byte, 8+len(name))
	copy(inam[0:4], "INAM")
	binary.LittleEndian.PutUint32(inam[4:8], uint32(len(title)+1))
	copy(inam[8:], name)

	listPayload := append([]byte("INFO"), inam...)
	list := make([]byte, 8+len(listPayload))
	copy(list[0:4], "LIST")
	binary.LittleEndian.PutUint32(list[4:8], uint32(len(listPayload)))
	copy(list[8:], listPayload)

	riff := make([]byte, 12+len(list))
	copy(riff[0:4], "RIFF")
	binary.LittleEndian.PutUint32(riff[4:8], uint32(4+len(list)))
	copy(riff[8:12], "WAVE")
	copy(riff[12:], list)
	return riff
}

func TestRemotePlaylistsNeverReadLocalTags(t *testing.T) {
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "local_file.wav")
	if err := os.WriteFile(wavPath, buildTaggedWAV("Secret Local Title"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Verify local TrackFromPath does read the embedded WAV title.
	if local := playlist.TrackFromPath(wavPath); local.Title != "Secret Local Title" {
		t.Fatalf("local TrackFromPath title = %q, want %q", local.Title, "Secret Local Title")
	}

	tests := []struct {
		name    string
		ext     string
		body    string
		resolve func(string) ([]playlist.Track, error)
		want    []string
	}{
		{
			name: "remote m3u filters schemes before conversion and skips local tag read",
			ext:  "/list.m3u",
			body: "#EXTM3U\n" +
				"ssh://evil/track.mp3\n" +
				"file://" + wavPath + "\n" +
				wavPath + "\n" +
				"https://example.com/ok.mp3\n",
			resolve: func(u string) ([]playlist.Track, error) { return resolveM3U(t.Context(), u) },
			want:    []string{wavPath, "https://example.com/ok.mp3"},
		},
		{
			name: "remote pls filters schemes before conversion and skips local tag read",
			ext:  "/list.pls",
			body: "[playlist]\n" +
				"File1=ssh://evil/track.mp3\n" +
				"File2=file://" + wavPath + "\n" +
				"File3=" + wavPath + "\n" +
				"File4=https://example.com/ok.mp3\n" +
				"NumberOfEntries=4\n",
			resolve: func(u string) ([]playlist.Track, error) { return resolvePLS(t.Context(), u) },
			want:    []string{wavPath, "https://example.com/ok.mp3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			tracks, err := tt.resolve(srv.URL + tt.ext)
			if err != nil {
				t.Fatalf("resolve error: %v", err)
			}
			if len(tracks) != len(tt.want) {
				t.Fatalf("got %d tracks, want %d: %+v", len(tracks), len(tt.want), tracks)
			}
			for i, w := range tt.want {
				if tracks[i].Path != w {
					t.Errorf("tracks[%d].Path = %q, want %q", i, tracks[i].Path, w)
				}
				if tracks[i].Title == "Secret Local Title" {
					t.Errorf("tracks[%d] read local file tags from %q", i, tracks[i].Path)
				}
			}
		})
	}
}

func TestParseXiaoyuzhouAudioURLValidation(t *testing.T) {
	tests := []struct {
		name     string
		audioURL string
		wantErr  bool
	}{
		{name: "https valid", audioURL: "https://media.xyzcdn.net/a.m4a"},
		{name: "http valid", audioURL: "http://media.xyzcdn.net/a.m4a"},
		{name: "file scheme rejected", audioURL: "file:///etc/passwd", wantErr: true},
		{name: "ssh scheme rejected", audioURL: "ssh://host/a.mp3", wantErr: true},
		{name: "userinfo rejected", audioURL: "https://user:pass@media.xyzcdn.net/a.m4a", wantErr: true},
		{name: "empty host rejected", audioURL: "https:///a.m4a", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := `<html><head><meta property="og:audio" content="` + tt.audioURL + `"><meta property="og:title" content="Ep"></head></html>`
			track, err := parseXiaoyuzhouEpisodeHTML("https://www.xiaoyuzhoufm.com/episode/1", doc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got track %+v", tt.audioURL, track)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.audioURL, err)
			}
			if track.Path != tt.audioURL {
				t.Fatalf("Path = %q, want %q", track.Path, tt.audioURL)
			}
		})
	}
}

func TestParseYTDLTracksValidatesScheme(t *testing.T) {
	input := strings.Join([]string{
		`{"webpage_url":"https://example.com/a","title":"HTTPS"}`,
		`{"url":"http://example.com/b","title":"HTTP"}`,
		`{"webpage_url":"file:///etc/passwd","title":"File"}`,
		`{"url":"ssh://host/track","title":"SSH"}`,
		`{"url":"javascript:alert(1)","title":"JS"}`,
		`{"url":"https:///nohost","title":"NoHost"}`,
		`{"url":"/local/file.mp3","title":"Bare"}`,
	}, "\n")

	tracks, entries, err := parseYTDLTracks(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseYTDLTracks error: %v", err)
	}
	if entries != 7 {
		t.Fatalf("entries = %d, want 7", entries)
	}
	want := []string{"https://example.com/a", "http://example.com/b"}
	if len(tracks) != len(want) {
		t.Fatalf("got %d tracks, want %d: %+v", len(tracks), len(want), tracks)
	}
	for i, w := range want {
		if tracks[i].Path != w {
			t.Errorf("tracks[%d].Path = %q, want %q", i, tracks[i].Path, w)
		}
	}
}

func TestCappedBuffer(t *testing.T) {
	b := cappedBuffer{limit: 5}
	n, err := b.Write([]byte("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("Write = (%d, %v), want (11, nil)", n, err)
	}
	if got := b.String(); got != "hello" {
		t.Fatalf("String() = %q, want %q", got, "hello")
	}
	n, err = b.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("second Write = (%d, %v), want (4, nil)", n, err)
	}
	if got := b.String(); got != "hello" {
		t.Fatalf("String() after second write = %q, want %q", got, "hello")
	}
}

func TestSniffFeedURLsConcurrentAndOrdered(t *testing.T) {
	arrived := make(chan struct{}, 3)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-release
		if strings.HasSuffix(r.URL.Path, "feed") {
			w.Header().Set("Content-Type", "application/rss+xml")
		} else {
			w.Header().Set("Content-Type", "audio/mpeg")
		}
	}))
	defer srv.Close()

	oldSniff := sniffClient
	sniffClient = srv.Client()
	t.Cleanup(func() { sniffClient = oldSniff })

	args := []string{
		srv.URL + "/a/feed",
		srv.URL + "/b/stream",
		srv.URL + "/c/feed",
	}

	done := make(chan Result, 1)
	go func() {
		res, _ := argsContext(t.Context(), args)
		done <- res
	}()

	// All 3 HEAD requests arrive concurrently before release closes.
	for range 3 {
		<-arrived
	}
	close(release)

	res := <-done
	wantPending := []string{args[0], args[2]}
	if len(res.Pending) != len(wantPending) || res.Pending[0] != wantPending[0] || res.Pending[1] != wantPending[1] {
		t.Fatalf("Pending = %v, want %v", res.Pending, wantPending)
	}
	if len(res.Tracks) != 1 || res.Tracks[0].Path != args[1] {
		t.Fatalf("Tracks = %+v, want [%s]", res.Tracks, args[1])
	}
}
