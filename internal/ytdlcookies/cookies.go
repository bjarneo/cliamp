// Package ytdlcookies stores browser and cookie file sources for yt-dlp-backed
// hosts.
package ytdlcookies

import (
	"fmt"
	"net/url"
	"os"
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
// file, else --cookies-from-browser for a browser source, else nil. yt-dlp
// writes back to the --cookies path, so it gets a private copy of the file.
// Call the non-nil cleanup after yt-dlp exits to remove the copy.
func Args(rawURL string) (args []string, cleanup func(), err error) {
	cleanup = func() {}
	host := hostForURL(rawURL)
	if host == "" {
		return nil, cleanup, nil
	}

	mu.RLock()
	path, browser := filesByHost[host], byHost[host]
	mu.RUnlock()
	if path != "" {
		tmp, err := copyToTemp(path)
		if err != nil {
			return nil, cleanup, fmt.Errorf("copy cookies file: %w", err)
		}
		return []string{"--cookies", tmp}, func() { os.Remove(tmp) }, nil
	}
	if browser != "" {
		return []string{"--cookies-from-browser", browser}, cleanup, nil
	}
	return nil, cleanup, nil
}

// copyToTemp copies path to a new private temp file and returns its path.
func copyToTemp(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dst, err := os.CreateTemp("", "cliamp-ytdl-cookies-*.txt")
	if err != nil {
		return "", err
	}
	dst.Close()
	if err := os.WriteFile(dst.Name(), data, 0o600); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
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
	host := playlist.NormalizeHost(u.Hostname())
	if host == "soundcloud.com" || strings.HasSuffix(host, ".soundcloud.com") {
		return "soundcloud.com"
	}
	return host
}
