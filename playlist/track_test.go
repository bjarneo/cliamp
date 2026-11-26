package playlist

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dhowden/tag"
)

func TestTrackMeta(t *testing.T) {
	t.Run("nil map returns empty", func(t *testing.T) {
		tr := Track{Title: "Test"}
		if got := tr.Meta("navidrome.id"); got != "" {
			t.Errorf("Meta on nil map = %q, want empty", got)
		}
	})

	t.Run("existing key", func(t *testing.T) {
		tr := Track{
			Title:        "Test",
			ProviderMeta: map[string]string{"navidrome.id": "abc123"},
		}
		if got := tr.Meta("navidrome.id"); got != "abc123" {
			t.Errorf("Meta = %q, want %q", got, "abc123")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		tr := Track{
			Title:        "Test",
			ProviderMeta: map[string]string{"navidrome.id": "abc123"},
		}
		if got := tr.Meta("jellyfin.id"); got != "" {
			t.Errorf("Meta = %q, want empty", got)
		}
	})
}

func TestTrackDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		track Track
		want  string
	}{
		{
			name:  "artist and title",
			track: Track{Artist: "Radiohead", Title: "Creep"},
			want:  "Radiohead - Creep",
		},
		{
			name:  "title only",
			track: Track{Title: "Unknown Song"},
			want:  "Unknown Song",
		},
		{
			name:  "empty",
			track: Track{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.track.DisplayName(); got != tt.want {
				t.Errorf("DisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrackIsLive(t *testing.T) {
	t.Run("realtime", func(t *testing.T) {
		tr := Track{Realtime: true}
		if !tr.IsLive() {
			t.Error("IsLive() = false, want true")
		}
	})

	t.Run("not realtime", func(t *testing.T) {
		tr := Track{Realtime: false}
		if tr.IsLive() {
			t.Error("IsLive() = true, want false")
		}
	})
}

func TestFileURL(t *testing.T) {
	got := fileURL(filepath.Join("tmp", "cover art.jpg"))
	if !strings.HasPrefix(got, "file:///") {
		t.Fatalf("fileURL = %q, want file URL", got)
	}
	if runtime.GOOS != "windows" && !strings.Contains(got, "cover%20art.jpg") {
		t.Fatalf("fileURL = %q, want escaped spaces", got)
	}
}

func TestCacheAlbumArtUsesContentHash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	first := cacheAlbumArt(&tag.Picture{Ext: "jpeg", Data: []byte("same cover")})
	second := cacheAlbumArt(&tag.Picture{Ext: "jpg", Data: []byte("same cover")})
	if first == "" || first != second {
		t.Fatalf("cacheAlbumArt URLs = %q and %q, want same non-empty URL", first, second)
	}

	matches, err := filepath.Glob(filepath.Join(home, ".local", "share", "cliamp", albumArtCacheDir, "*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("cached files = %d, want 1: %v", len(matches), matches)
	}
}

func TestCacheAlbumArtSkipsCleanupOnHit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	cleanups := 0
	orig := runAlbumArtCleanup
	runAlbumArtCleanup = func(dir string, maxBytes int64, keepPath string) {
		cleanups++
		orig(dir, maxBytes, keepPath)
	}
	t.Cleanup(func() { runAlbumArtCleanup = orig })

	pic := &tag.Picture{Ext: "jpg", Data: []byte("cover-bytes")}
	if got := cacheAlbumArt(pic); got == "" {
		t.Fatal("first cacheAlbumArt returned empty URL")
	}
	if cleanups != 1 {
		t.Fatalf("cleanups after miss = %d, want 1", cleanups)
	}
	if got := cacheAlbumArt(pic); got == "" {
		t.Fatal("second cacheAlbumArt returned empty URL")
	}
	if cleanups != 1 {
		t.Fatalf("cleanups after hit = %d, want 1", cleanups)
	}
}

func TestCleanupAlbumArtCacheKeepsCurrentFile(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jpg")
	keepPath := filepath.Join(dir, "keep.jpg")
	if err := os.WriteFile(oldPath, []byte(strings.Repeat("o", 80)), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.WriteFile(keepPath, []byte(strings.Repeat("k", 80)), 0o644); err != nil {
		t.Fatalf("WriteFile keep: %v", err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	cleanupAlbumArtCache(dir, 100, keepPath)
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old cache file still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("current cache file was removed: %v", err)
	}
}

func TestRefreshEmbeddedMetadataPreservesSavedFieldsWhenUnreadable(t *testing.T) {
	track := Track{
		Path:           filepath.Join(t.TempDir(), "missing.mp3"),
		Title:          "Saved title",
		Artist:         "Saved artist",
		EmbeddedLyrics: "old lyrics",
		AlbumArtURL:    "file:///old.jpg",
	}

	got := RefreshEmbeddedMetadata(track)
	if got.EmbeddedLyrics != track.EmbeddedLyrics || got.AlbumArtURL != track.AlbumArtURL {
		t.Fatalf("embedded metadata changed: got %q, %q want %q, %q", got.EmbeddedLyrics, got.AlbumArtURL, track.EmbeddedLyrics, track.AlbumArtURL)
	}
	if got.Title != track.Title || got.Artist != track.Artist {
		t.Fatalf("non-embedded fields changed: got %+v want title/artist from %+v", got, track)
	}
}

func TestRefreshEmbeddedMetadataClearsSavedFieldsWhenTagsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.mp3")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	track := Track{
		Path:           path,
		Title:          "Saved title",
		EmbeddedLyrics: "old lyrics",
		AlbumArtURL:    "file:///old.jpg",
	}

	got := RefreshEmbeddedMetadata(track)
	if got.EmbeddedLyrics != "" || got.AlbumArtURL != "" {
		t.Fatalf("embedded metadata = %q, %q; want empty refreshed fields", got.EmbeddedLyrics, got.AlbumArtURL)
	}
	if got.Title != track.Title {
		t.Fatalf("title changed: got %q want %q", got.Title, track.Title)
	}
}

func TestTrackFromURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantTtl string
		stream  bool
	}{
		{
			name:    "with filename",
			url:     "https://example.com/music/song.mp3",
			wantTtl: "song",
			stream:  true,
		},
		{
			name:    "stream path fallback to hostname",
			url:     "https://radio.example.com/stream",
			wantTtl: "radio.example.com",
			stream:  true,
		},
		{
			name:    "rest path fallback to hostname",
			url:     "https://api.example.com/rest",
			wantTtl: "api.example.com",
			stream:  true,
		},
		{
			name:    "query params ignored",
			url:     "https://example.com/song.mp3?token=abc",
			wantTtl: "song",
			stream:  true,
		},
		{
			name:    "root path fallback to hostname",
			url:     "https://radio.example.com/",
			wantTtl: "radio.example.com",
			stream:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := TrackFromPath(tt.url)
			if tr.Title != tt.wantTtl {
				t.Errorf("Title = %q, want %q", tr.Title, tt.wantTtl)
			}
			if tr.Stream != tt.stream {
				t.Errorf("Stream = %v, want %v", tr.Stream, tt.stream)
			}
			if tr.Path != tt.url {
				t.Errorf("Path = %q, want %q", tr.Path, tt.url)
			}
		})
	}
}

func TestRepeatModeString(t *testing.T) {
	tests := []struct {
		mode RepeatMode
		want string
	}{
		{RepeatOff, "Off"},
		{RepeatAll, "All"},
		{RepeatOne, "One"},
		{RepeatMode(99), "Off"}, // unknown defaults to "Off"
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
