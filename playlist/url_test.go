package playlist

import "testing"

func TestIsURL(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"http://example.com/stream.mp3", true},
		{"https://example.com/stream.mp3", true},
		{"ytsearch:lofi hip hop", true},
		{"ytsearch1:some song", true},
		{"scsearch:artist name", true},
		{"scsearch1:track name", true},
		{"/home/user/music/song.mp3", false},
		{"relative/path.flac", false},
		{"", false},
		{"ftp://files.example.com/song.mp3", false},
		{"spotify:track:abc123", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsURL(tt.path); got != tt.want {
				t.Errorf("IsURL(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsM3U(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://example.com/playlist.m3u", true},
		{"https://example.com/playlist.m3u8", true},
		{"https://example.com/playlist.M3U", true},
		{"/home/user/playlist.m3u", true},
		{"/home/user/playlist.m3u8", true},
		{"https://example.com/stream.mp3", false},
		{"/home/user/song.mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsM3U(tt.path); got != tt.want {
				t.Errorf("IsM3U(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsLocalM3U(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/playlist.m3u", true},
		{"/home/user/playlist.m3u8", true},
		{"https://example.com/playlist.m3u", false},
		{"/home/user/song.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsLocalM3U(tt.path); got != tt.want {
				t.Errorf("IsLocalM3U(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsPLS(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://example.com/station.pls", true},
		{"https://example.com/station.PLS", true},
		{"/home/user/station.pls", true},
		{"https://example.com/stream.mp3", false},
		{"/home/user/song.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsPLS(tt.path); got != tt.want {
				t.Errorf("IsPLS(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsLocalPLS(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/station.pls", true},
		{"https://example.com/station.pls", false},
		{"/home/user/song.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsLocalPLS(tt.path); got != tt.want {
				t.Errorf("IsLocalPLS(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsYouTubeURL(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtube.com/watch?v=abc123", true},
		{"https://youtu.be/abc123", true},
		{"https://m.youtube.com/watch?v=abc123", true},
		// YouTube Music URLs should NOT match
		{"https://music.youtube.com/watch?v=abc123", false},
		// ytsearch protocols should NOT match
		{"ytsearch:lofi hip hop", false},
		{"ytsearch1:some song", false},
		// Non-YouTube
		{"https://soundcloud.com/artist/track", false},
		{"/local/file.mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsYouTubeURL(tt.path); got != tt.want {
				t.Errorf("IsYouTubeURL(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsYouTubeMusicURL(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://music.youtube.com/watch?v=abc123", true},
		{"https://www.music.youtube.com/watch?v=abc123", true},
		// Regular YouTube should NOT match
		{"https://www.youtube.com/watch?v=abc123", false},
		{"https://youtu.be/abc123", false},
		{"/local/file.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsYouTubeMusicURL(tt.path); got != tt.want {
				t.Errorf("IsYouTubeMusicURL(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsYTDL(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// YouTube
		{"https://www.youtube.com/watch?v=abc123", true},
		{"https://youtu.be/abc123", true},
		// YouTube Music
		{"https://music.youtube.com/watch?v=abc123", true},
		// Search protocols
		{"ytsearch:lofi hip hop", true},
		{"ytsearch1:some song", true},
		{"scsearch:artist name", true},
		{"scsearch1:track name", true},
		// SoundCloud
		{"https://soundcloud.com/artist/track", true},
		// Bandcamp
		{"https://bandcamp.com/album", true},
		{"https://artist.bandcamp.com/album/name", true},
		// Bilibili
		{"https://bilibili.com/video/BV123", true},
		{"https://www.bilibili.com/video/BV123", true},
		{"https://space.bilibili.com/12345", true},
		{"https://b23.tv/abc123", true},
		// NetEase
		{"https://music.163.com/song?id=12345", true},
		// Non-YTDL
		{"https://example.com/stream.mp3", false},
		{"/local/file.mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsYTDL(tt.path); got != tt.want {
				t.Errorf("IsYTDL(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsFeed(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://example.com/feed.xml", true},
		{"https://example.com/podcast.rss", true},
		{"https://example.com/feed.atom", true},
		{"https://example.com/podcast.XML", true},
		// Non-feed
		{"https://example.com/stream.mp3", false},
		{"/local/feed.xml", false}, // not a URL
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsFeed(tt.path); got != tt.want {
				t.Errorf("IsFeed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsXiaoyuzhouEpisode(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://www.xiaoyuzhoufm.com/episode/abc123", true},
		{"https://xiaoyuzhoufm.com/episode/abc123", true},
		{"https://m.xiaoyuzhoufm.com/episode/abc123", true},
		// Not an episode path
		{"https://www.xiaoyuzhoufm.com/podcast/abc123", false},
		// Not xiaoyuzhou
		{"https://example.com/episode/abc123", false},
		{"/local/file.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsXiaoyuzhouEpisode(tt.path); got != tt.want {
				t.Errorf("IsXiaoyuzhouEpisode(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
