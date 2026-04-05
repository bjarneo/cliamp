package playlist

import "testing"

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
