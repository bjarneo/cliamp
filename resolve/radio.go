package resolve

import (
	"net/url"
	"strings"
)

// isYouTubeVideoID reports whether s looks like a video ID rather than a
// handle, path, or other identifier. YouTube IDs are base64url-ish, so any
// separator ("/", "@", "?", …) means the caller matched something else, such
// as youtu.be/@channel or a watch?v= value carrying extra path segments.
func isYouTubeVideoID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// YouTubeVideoID extracts the video ID from a YouTube, YouTube Music, or
// youtu.be track URL. Returns "" when no video ID is present.
func YouTubeVideoID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	// Only real web URLs describe a playable YouTube video; anything else
	// (ftp://, scheme-relative, custom schemes) is not one of ours.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "youtube.com", "music.youtube.com":
		if u.Path == "/watch" {
			if id := u.Query().Get("v"); isYouTubeVideoID(id) {
				return id
			}
		}
	case "youtu.be":
		if id := strings.Trim(u.Path, "/"); isYouTubeVideoID(id) {
			return id
		}
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
