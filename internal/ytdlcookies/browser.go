package ytdlcookies

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// FromBrowser exports cookies through yt-dlp without visiting a website or
// downloading media. The cookie file exists only inside a private temp directory.
func FromBrowser(ctx context.Context, browser string) (http.CookieJar, error) {
	if strings.TrimSpace(browser) == "" {
		return nil, fmt.Errorf("browser cookie source is required")
	}
	dir, err := os.MkdirTemp("", "cliamp-browser-cookies-*")
	if err != nil {
		return nil, fmt.Errorf("create private cookie directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(path, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		return nil, fmt.Errorf("prepare cookie file: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--ignore-config", "--no-cache-dir", "--simulate",
		"--cookies-from-browser", browser, "--cookies", path,
		"--load-info-json", "-",
	)
	cmd.Stdin = strings.NewReader(`[]`)
	// Do not include yt-dlp output in errors: it may contain private cookie data.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("read browser cookies with yt-dlp (check browser profile and keyring): %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			continue
		}
		expires, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			continue
		}
		cookie := &http.Cookie{
			Domain:   strings.TrimPrefix(fields[0], "#HttpOnly_"),
			Path:     fields[2],
			Secure:   fields[3] == "TRUE",
			HttpOnly: strings.HasPrefix(fields[0], "#HttpOnly_"),
			Name:     fields[5],
			Value:    fields[6],
		}
		if expires != 0 {
			cookie.Expires = time.Unix(expires, 0)
		}
		if cookie.Valid() != nil {
			continue
		}
		origin := &url.URL{Scheme: "https", Host: strings.TrimPrefix(cookie.Domain, ".")}
		if fields[1] == "FALSE" {
			cookie.Domain = "" // Host-only cookies must not reach subdomains.
		}
		jar.SetCookies(origin, []*http.Cookie{cookie})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return jar, nil
}
