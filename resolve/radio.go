package resolve

import (
	"net/url"
	"strings"
)

// YouTubeVideoID extracts the video ID from a YouTube, YouTube Music, or
// youtu.be track URL. Returns "" when no video ID is present.
func YouTubeVideoID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "youtube.com", "music.youtube.com":
		if u.Path == "/watch" {
			return u.Query().Get("v")
		}
	case "youtu.be":
		return strings.Trim(u.Path, "/")
	}
	return ""
}

// RadioMixURL returns the YouTube auto-generated Mix ("radio") playlist URL
// seeded from trackURL, or ok=false when the track is not a YouTube video.
// The RD<videoID> list is the same source YouTube's own autoplay uses;
// resolveYTDL already handles RD lists via yt-dlp --flat-playlist.
func RadioMixURL(trackURL string) (string, bool) {
	id := YouTubeVideoID(trackURL)
	if id == "" {
		return "", false
	}
	return "https://www.youtube.com/watch?v=" + id + "&list=RD" + id, true
}
