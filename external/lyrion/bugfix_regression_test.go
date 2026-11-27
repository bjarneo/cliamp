package lyrion

import "testing"

func TestIsStreamURLWithReverseProxyPrefix(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://lms.local:9000/music/77/download", true},
		{"https://example.com/lms/music/77/download", true},
		{"https://example.com/a/b/music/77/download.mp3", true},
		{"https://example.com/lms/music/download", false},
		{"https://example.com/lms/music/77/download/extra", false},
	}
	for _, tt := range tests {
		if got := IsStreamURL(tt.url); got != tt.want {
			t.Errorf("IsStreamURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
