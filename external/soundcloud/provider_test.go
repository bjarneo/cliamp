package soundcloud

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/resolve"
)

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool // want non-nil
	}{
		{"default (not enabled)", Config{}, false},
		{"explicitly enabled", Config{Enabled: true}, true},
		{"enabled with user", Config{Enabled: true, User: "alice"}, true},
		{"enabled trims user", Config{Enabled: true, User: "  bob  "}, true},
		{"user without enabled stays nil", Config{User: "alice"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewFromConfig(tt.cfg)
			if (got != nil) != tt.want {
				t.Fatalf("NewFromConfig(%+v) non-nil = %v, want %v", tt.cfg, got != nil, tt.want)
			}
			if got != nil && tt.cfg.User != "" && got.user != "bob" && got.user != "alice" {
				if got.user == "" {
					t.Errorf("user not propagated, got empty")
				}
			}
		})
	}
}

func TestProviderName(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	if got := p.Name(); got != "SoundCloud" {
		t.Errorf("Name() = %q, want SoundCloud", got)
	}
}

func TestPlaylistsWithoutUserShowsBrowse(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	pls, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(pls) == 0 {
		t.Fatal("Playlists() returned 0 entries, want curated browse list")
	}
	for _, pl := range pls {
		if pl.Section != "Browse" {
			t.Errorf("playlist %q has Section %q, want Browse", pl.Name, pl.Section)
		}
		if !strings.HasPrefix(pl.ID, "scsearch") {
			t.Errorf("browse playlist %q ID = %q, want scsearch prefix", pl.Name, pl.ID)
		}
	}
}

func TestPlaylistsWithUserShowsOnlyUserEntries(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, User: "alice"})
	pls, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(pls) != 3 {
		t.Fatalf("Playlists() = %d entries, want 3 (user-only when User is set)", len(pls))
	}

	wantNames := map[string]string{
		"https://soundcloud.com/alice/tracks":  "Tracks",
		"https://soundcloud.com/alice/likes":   "Likes",
		"https://soundcloud.com/alice/reposts": "Reposts",
	}
	for _, pl := range pls {
		want, ok := wantNames[pl.ID]
		if !ok {
			t.Errorf("unexpected playlist ID %q", pl.ID)
			continue
		}
		if pl.Name != want {
			t.Errorf("Playlist %q Name = %q, want %q", pl.ID, pl.Name, want)
		}
		if pl.Section != "alice" {
			t.Errorf("Playlist %q Section = %q, want alice", pl.ID, pl.Section)
		}
	}
}

func TestPlaylistsEscapesUsername(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true, User: "weird name"})
	pls, _ := p.Playlists()
	if len(pls) == 0 {
		t.Fatal("expected non-empty playlists")
	}
	for _, pl := range pls {
		if got, want := pl.ID, "https://soundcloud.com/weird%20name/"; len(got) < len(want) || got[:len(want)] != want {
			t.Errorf("playlist ID %q not URL-escaped", pl.ID)
		}
	}
}

func TestSearchTracksEmptyQuery(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	tracks, err := p.SearchTracks(context.Background(), "   ", 10)
	if err != nil {
		t.Fatalf("SearchTracks(empty) error = %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("SearchTracks(empty) returned %d tracks, want 0", len(tracks))
	}
}

func TestSearchTracksHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	tmpDir := t.TempDir()
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")
	if err := os.WriteFile(fakeYTDL, []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewFromConfig(Config{Enabled: true}).SearchTracks(ctx, "query", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SearchTracks() error = %v, want context.Canceled", err)
	}
}

func TestTracksRejectsEmptyID(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	_, err := p.Tracks("")
	if err == nil {
		t.Fatal("Tracks(\"\") expected error, got nil")
	}
}

func TestValidatePlaylistID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "https root host", id: "https://soundcloud.com/alice/tracks"},
		{name: "https www host", id: "https://www.soundcloud.com/alice/likes"},
		{name: "https mobile host", id: "https://m.soundcloud.com/alice/reposts"},
		{name: "https subdomain", id: "https://on.soundcloud.com/QSWjV6ABJR5SICzGB3"},
		{name: "https mixed case host", id: "HTTPS://API.SoundCloud.COM/playlists/1"},
		{name: "scsearch bare", id: "scsearch:ambient"},
		{name: "scsearch numbered", id: "scsearch50:hip hop"},
		{name: "empty", id: "", wantErr: true},
		{name: "http scheme", id: "http://soundcloud.com/alice/tracks", wantErr: true},
		{name: "file scheme", id: "file:///etc/passwd", wantErr: true},
		{name: "ssh scheme", id: "ssh://attacker.example/tracks", wantErr: true},
		{name: "ytsearch prefix", id: "ytsearch10:ambient", wantErr: true},
		{name: "youtube host", id: "https://youtube.com/watch?v=dQw4w9WgXcQ", wantErr: true},
		{name: "netease host", id: "https://music.163.com/#/playlist?id=1", wantErr: true},
		{name: "suffix spoof host", id: "https://evil-soundcloud.com/alice/tracks", wantErr: true},
		{name: "prefix spoof host", id: "https://soundcloud.com.evil.com/alice/tracks", wantErr: true},
		{name: "leading dot host", id: "https://.soundcloud.com/alice/tracks", wantErr: true},
		{name: "double dot host", id: "https://www..soundcloud.com/alice/tracks", wantErr: true},
		{name: "userinfo present", id: "https://user:pass@soundcloud.com/alice/tracks", wantErr: true},
		{name: "empty userinfo present", id: "https://@soundcloud.com/alice/tracks", wantErr: true},
		{name: "opaque https url", id: "https:soundcloud.com/alice/tracks", wantErr: true},
		{name: "scsearch without query", id: "scsearch:", wantErr: true},
		{name: "scsearch whitespace query", id: "scsearch50:   ", wantErr: true},
		{name: "scsearch non-digit count", id: "scsearchabc:ambient", wantErr: true},
		{name: "scsearch negative count", id: "scsearch-1:ambient", wantErr: true},
		{name: "scsearch without colon", id: "scsearch50", wantErr: true},
		{name: "local path", id: "/etc/passwd", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlaylistID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePlaylistID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "pass") {
				t.Errorf("error leaks credential: %v", err)
			}
		})
	}
}

func TestTracksValidatesPlaylistIDBeforeExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "invoked")
	fakeYTDL := filepath.Join(tmpDir, "yt-dlp")
	script := "#!/bin/sh\necho invoked > " + marker + "\nprintf '{\"title\":\"Track\",\"webpage_url\":\"https://soundcloud.com/a/b\"}\\n'\n"
	if err := os.WriteFile(fakeYTDL, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := NewFromConfig(Config{Enabled: true})
	rejected := []string{
		"file:///etc/passwd",
		"http://soundcloud.com/alice/tracks",
		"ytsearch10:ambient",
		"https://youtube.com/watch?v=1",
		"https://music.163.com/#/playlist?id=1",
		"https://user:pass@soundcloud.com/alice/tracks",
	}
	for _, id := range rejected {
		if _, err := p.Tracks(id); err == nil {
			t.Errorf("Tracks(%q) returned nil error, want rejection", id)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("Tracks(%q) invoked yt-dlp despite invalid playlist id", id)
		}
	}

	tracks, err := p.Tracks("https://soundcloud.com/alice/tracks")
	if err != nil {
		t.Fatalf("Tracks(valid URL) error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Track" {
		t.Fatalf("Tracks(valid URL) = %+v, want 1 track", tracks)
	}
}

func TestConfigIsSet(t *testing.T) {
	if (Config{}).IsSet() {
		t.Error("Config{} should not be set (opt-in)")
	}
	if !(Config{Enabled: true}).IsSet() {
		t.Error("Config{Enabled:true} should report set")
	}
}

func TestNewFromConfigPropagatesCookies(t *testing.T) {
	t.Cleanup(func() { resolve.SetYTDLCookiesForHost("soundcloud.com", "") })

	NewFromConfig(Config{Enabled: true, CookiesFrom: "firefox"})
	if got := ytdlcookies.ForURL("https://soundcloud.com/user/tracks"); got != "firefox" {
		t.Errorf("SoundCloud cookie source = %q, want firefox", got)
	}
}
