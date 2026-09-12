package playlist

import "testing"

func TestShareLink(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		want  string
		share bool
	}{
		{
			name:  "spotify track",
			path:  "spotify:track:5FFTCVlkmd78TIw9mTfDUP",
			want:  "https://open.spotify.com/track/5FFTCVlkmd78TIw9mTfDUP",
			share: true,
		},
		{
			name:  "spotify episode",
			path:  "spotify:episode:4rOoJ6Egrf8K2IrywzwOMk",
			want:  "https://open.spotify.com/episode/4rOoJ6Egrf8K2IrywzwOMk",
			share: true,
		},
		{
			name:  "spotify album",
			path:  "spotify:album:6akEvsycLGftJxsoqdWqaw",
			want:  "https://open.spotify.com/album/6akEvsycLGftJxsoqdWqaw",
			share: true,
		},
		{
			name:  "spotify truncated id",
			path:  "spotify:track:",
			want:  "",
			share: false,
		},
		{
			name:  "spotify unknown type",
			path:  "spotify:user:repparw",
			want:  "",
			share: false,
		},
		{
			name:  "spotify id with reserved chars rejected",
			path:  "spotify:track:abc/def?x#y",
			want:  "",
			share: false,
		},
		{
			name:  "https stream",
			path:  "http://radio.cliamp.stream/lofi/stream",
			want:  "http://radio.cliamp.stream/lofi/stream",
			share: true,
		},
		{
			name:  "youtube page",
			path:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			want:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			share: true,
		},
		{
			name:  "yt-dlp search is not a link",
			path:  "ytsearch:never gonna give you up",
			want:  "",
			share: false,
		},
		{
			name:  "local file",
			path:  "/home/me/Music/song.flac",
			want:  "",
			share: false,
		},
		{
			name:  "empty",
			path:  "",
			want:  "",
			share: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ShareLink(tt.path)
			if ok != tt.share || got != tt.want {
				t.Fatalf("ShareLink(%q) = (%q, %v), want (%q, %v)", tt.path, got, ok, tt.want, tt.share)
			}
		})
	}
}
