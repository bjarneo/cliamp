// Package ytdlcookies stores browser cookie sources for yt-dlp-backed hosts.
package ytdlcookies

import (
	"net/url"
	"strings"
	"sync"
)

var (
	mu     sync.RWMutex
	byHost = make(map[string]string)
)

// SetForHost associates a browser cookie source with a URL host. Passing an
// empty browser removes the association.
func SetForHost(host, browser string) {
	host = normalizeHost(host)
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
	return normalizeHost(u.Hostname())
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	return host
}
