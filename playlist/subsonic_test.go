package playlist

import "testing"

func TestIsSubsonicStreamURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://music.example.com/rest/stream?id=1", true},
		{"https://music.example.com/rest/stream.view?id=1", true},
		{"https://music.example.com/rest/download?id=1", true},
		{"https://music.example.com/rest/download.view?id=1", true},
		{"https://bandcamp.com/api/subsonic/rest/stream?id=1", true},
		{"https://music.example.com/rest/getPlaylists", false},
		{"https://example.com/song.mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSubsonicStreamURL(tt.url); got != tt.want {
			t.Errorf("IsSubsonicStreamURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
