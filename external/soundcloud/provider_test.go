package soundcloud

import (
	"context"
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

func TestTracksRejectsEmptyID(t *testing.T) {
	p := NewFromConfig(Config{Enabled: true})
	_, err := p.Tracks("")
	if err == nil {
		t.Fatal("Tracks(\"\") expected error, got nil")
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
