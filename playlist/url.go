package playlist

import (
	"net/url"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// IsURL reports whether path is an HTTP or HTTPS URL, or a yt-dlp search protocol string.
func IsURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") ||
		IsYTSearch(path)
}

// IsYTSearch reports whether path is a yt-dlp search expression
// (ytsearch:, ytsearchN:, scsearch:, scsearchN:).
func IsYTSearch(path string) bool {
	return matchSearchPrefix(path, "ytsearch") || matchSearchPrefix(path, "scsearch")
}

func matchSearchPrefix(path, name string) bool {
	if !strings.HasPrefix(path, name) {
		return false
	}
	rest := path[len(name):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return false
	}
	for _, c := range rest[:colon] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// hostOf parses path as a URL and returns its host as NormalizeHost gives it.
// ok is false when path is not a URL or does not parse.
func hostOf(path string) (host string, u *url.URL, ok bool) {
	if !IsURL(path) {
		return "", nil, false
	}
	u, err := url.Parse(path)
	if err != nil {
		return "", nil, false
	}
	return NormalizeHost(u.Hostname()), u, true
}

// NormalizeHost returns host in lower case, without surrounding whitespace
// and without a leading "www." or "m.". The URL predicates and the yt-dlp
// cookie sources compare hosts in this form.
func NormalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	return strings.TrimPrefix(host, "m.")
}

// IsM3U reports whether the path points to an M3U playlist file (URL or local).
func IsM3U(path string) bool {
	if IsURL(path) {
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		ext := strings.ToLower(pathpkg.Ext(u.Path))
		return ext == ".m3u" || ext == ".m3u8"
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".m3u8"
}

// IsLocalM3U reports whether the path is a local (non-URL) M3U file.
func IsLocalM3U(path string) bool {
	return !IsURL(path) && IsM3U(path)
}

// IsPLS reports whether the path points to a PLS playlist file (URL or local).
func IsPLS(path string) bool {
	if IsURL(path) {
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		return strings.ToLower(pathpkg.Ext(u.Path)) == ".pls"
	}
	return strings.ToLower(filepath.Ext(path)) == ".pls"
}

// IsLocalPLS reports whether the path is a local (non-URL) PLS file.
func IsLocalPLS(path string) bool {
	return !IsURL(path) && IsPLS(path)
}

// IsYouTubeURL reports whether the URL points to YouTube (youtube.com or youtu.be).
// YouTube Music (music.youtube.com) is excluded — use IsYouTubeMusicURL for that.
func IsYouTubeURL(path string) bool {
	if !IsURL(path) {
		return false
	}
	// ytsearch: protocols are handled by yt-dlp, not the native YouTube client.
	if IsYTSearch(path) {
		return false
	}
	host, _, ok := hostOf(path)
	if !ok {
		return false
	}
	switch host {
	case "youtube.com", "youtu.be":
		return true
	}
	return false
}

// IsYouTubeMusicURL reports whether the URL points to YouTube Music (music.youtube.com).
// These URLs require yt-dlp rather than the native YouTube API client.
func IsYouTubeMusicURL(path string) bool {
	host, _, ok := hostOf(path)
	return ok && host == "music.youtube.com"
}

// IsMixcloudURL reports whether path is a Mixcloud website URL.
func IsMixcloudURL(path string) bool {
	host, _, ok := hostOf(path)
	return ok && host == "mixcloud.com"
}

// IsYTDL reports whether the URL points to a site supported by yt-dlp
// (YouTube, SoundCloud, Bandcamp, ytsearch: protocol, etc.).
func IsYTDL(path string) bool {
	if !IsURL(path) {
		return false
	}
	// YouTube and YouTube Music URLs are handled by yt-dlp for playback.
	if IsYouTubeURL(path) || IsYouTubeMusicURL(path) {
		return true
	}
	if IsYTSearch(path) {
		return true
	}
	host, _, ok := hostOf(path)
	if !ok {
		return false
	}
	switch host {
	case "soundcloud.com",
		"mixcloud.com",
		"bandcamp.com",
		"music.163.com",
		"bilibili.com",
		"b23.tv":
		return true
	}
	// Bilibili subdomains (e.g. space.bilibili.com)
	if strings.HasSuffix(host, ".bilibili.com") {
		return true
	}
	// Bandcamp artist subdomains (e.g. artist.bandcamp.com)
	if strings.HasSuffix(host, ".bandcamp.com") {
		return true
	}
	return false
}

// IsXiaoyuzhouEpisode reports whether the URL points to a Xiaoyuzhou episode page.
func IsXiaoyuzhouEpisode(path string) bool {
	host, u, ok := hostOf(path)
	if !ok || host != "xiaoyuzhoufm.com" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(u.Path), "/episode/")
}

// IsFeed reports whether the URL points to a podcast RSS/XML feed.
func IsFeed(path string) bool {
	if !IsURL(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	ext := strings.ToLower(pathpkg.Ext(u.Path))
	return ext == ".xml" || ext == ".rss" || ext == ".atom"
}
