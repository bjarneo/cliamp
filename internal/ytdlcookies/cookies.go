// Package ytdlcookies stores browser and cookie file sources for yt-dlp-backed
// hosts.
package ytdlcookies

import (
	"net/url"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/playlist"
)

var (
	mu          sync.RWMutex
	byHost      = make(map[string]string)
	filesByHost = make(map[string]string)
)

// SetForHost associates a browser cookie source with a URL host. Passing an
// empty browser removes the association.
func SetForHost(host, browser string) {
	host = playlist.NormalizeHost(host)
	if host == "" {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	browser = strings.TrimSpace(browser)
	if browser == "" {
		delete(byHost, host)
		return
	}
	byHost[host] = browser
}

// SetFileForHost associates a Netscape-format cookies file with a URL host.
// A file takes precedence over a browser source for the same host. Passing an
// empty path removes the association.
func SetFileForHost(host, path string) {
	host = playlist.NormalizeHost(host)
	if host == "" {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	path = strings.TrimSpace(path)
	if path == "" {
		delete(filesByHost, host)
		return
	}
	filesByHost[host] = path
}

// Args returns the yt-dlp cookie arguments for rawURL: --cookies for a cookies
// file, else --cookies-from-browser for a browser source, else nil.
func Args(rawURL string) []string {
	host := hostForURL(rawURL)
	if host == "" {
		return nil
	}

	mu.RLock()
	defer mu.RUnlock()
	if path := filesByHost[host]; path != "" {
		return []string{"--cookies", path}
	}
	if browser := byHost[host]; browser != "" {
		return []string{"--cookies-from-browser", browser}
	}
	return nil
}

// ForURL returns the browser cookie source associated with rawURL. yt-dlp
// search prefixes are mapped to the service host they query.
func ForURL(rawURL string) string {
	host := hostForURL(rawURL)
	if host == "" {
		return ""
	}

	mu.RLock()
	defer mu.RUnlock()
	return byHost[host]
}

func hostForURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	lower := strings.ToLower(rawURL)
	switch {
	case strings.HasPrefix(lower, "scsearch"):
		return "soundcloud.com"
	case strings.HasPrefix(lower, "ytsearch"):
		return "youtube.com"
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return playlist.NormalizeHost(u.Hostname())
}
