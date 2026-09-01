package resolve

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestUriScheme(t *testing.T) {
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
			if got := uriScheme(tt.path); got != tt.want {
				t.Errorf("uriScheme(%q) = %q, want %q", tt.path, got, tt.want)
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
