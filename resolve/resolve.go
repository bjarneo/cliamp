// Package resolve converts CLI arguments (files, directories, globs, URLs,
// M3U playlists, and RSS feeds) into a flat list of playlist tracks.
package resolve

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"cliamp/player"
	"cliamp/playlist"
)

// Result holds the output of Args: instantly-resolved tracks and
// remote URLs (feeds, M3U) that need async HTTP fetching.
type Result struct {
	Tracks  []playlist.Track // local files, dirs, plain stream URLs
	Pending []string         // feed/M3U URLs to resolve asynchronously
}

// Args separates CLI arguments into immediately-resolved local tracks
// and pending remote URLs (feeds, M3U) that require HTTP fetching.
func Args(args []string) (Result, error) {
	var r Result
	var files []string

	for _, arg := range args {
		if playlist.IsURL(arg) {
			if playlist.IsFeed(arg) || playlist.IsM3U(arg) || playlist.IsYTDL(arg) {
				r.Pending = append(r.Pending, arg)
			} else {
				files = append(files, arg)
			}
			continue
		}
		matches, err := filepath.Glob(arg)
		if err != nil || len(matches) == 0 {
			matches = []string{arg}
		}
		for _, path := range matches {
			resolved, err := collectAudioFiles(path)
			if err != nil {
				return r, fmt.Errorf("scanning %s: %w", path, err)
			}
			files = append(files, resolved...)
		}
	}

	for _, f := range files {
		r.Tracks = append(r.Tracks, playlist.TrackFromPath(f))
	}
	return r, nil
}

// Remote fetches feed and M3U URLs and returns the resolved tracks.
func Remote(urls []string) ([]playlist.Track, error) {
	var tracks []playlist.Track
	for _, u := range urls {
		switch {
		case playlist.IsYTDL(u):
			t, err := resolveYTDL(u)
			if err != nil {
				return nil, fmt.Errorf("resolving yt-dlp %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		case playlist.IsFeed(u):
			t, err := resolveFeed(u)
			if err != nil {
				return nil, fmt.Errorf("resolving feed %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		case playlist.IsM3U(u):
			streams, err := resolveM3U(u)
			if err != nil {
				return nil, fmt.Errorf("resolving m3u %s: %w", u, err)
			}
			for _, s := range streams {
				tracks = append(tracks, playlist.TrackFromPath(s))
			}
		}
	}
	return tracks, nil
}

// collectAudioFiles returns audio file paths for the given argument.
// If path is a directory, it walks it recursively collecting supported files.
// If path is a file with a supported extension, it returns it directly.
func collectAudioFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		if player.SupportedExts[strings.ToLower(filepath.Ext(path))] {
			return []string{path}, nil
		}
		return nil, nil
	}

	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && player.SupportedExts[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(files)
	return files, nil
}

// resolveFeed fetches a podcast RSS feed and returns tracks with metadata.
func resolveFeed(feedURL string) ([]playlist.Track, error) {
	resp, err := http.Get(feedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rss struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title     string `xml:"title"`
				Enclosure struct {
					URL  string `xml:"url,attr"`
					Type string `xml:"type,attr"`
				} `xml:"enclosure"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&rss); err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}

	var tracks []playlist.Track
	for _, item := range rss.Channel.Items {
		if item.Enclosure.URL == "" {
			continue
		}
		tracks = append(tracks, playlist.Track{
			Path:   item.Enclosure.URL,
			Title:  item.Title,
			Artist: rss.Channel.Title,
			Stream: true,
		})
	}
	return tracks, nil
}

// resolveM3U fetches an M3U playlist URL and returns the stream URLs it contains.
func resolveM3U(m3uURL string) ([]string, error) {
	resp, err := http.Get(m3uURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var urls []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	return urls, scanner.Err()
}

// ytdlFlatEntry holds JSON fields from yt-dlp --flat-playlist output.
type ytdlFlatEntry struct {
	URL                string `json:"url"`
	WebpageURL         string `json:"webpage_url"`
	Title              string `json:"title"`
	Uploader           string `json:"uploader"`
	PlaylistUploader   string `json:"playlist_uploader"`
	WebpageURLBasename string `json:"webpage_url_basename"`
}

// ytdlFullEntry holds JSON fields from yt-dlp --print-json output (download mode).
type ytdlFullEntry struct {
	Title    string `json:"title"`
	Uploader string `json:"uploader"`
	Filename string `json:"_filename"`
}

// ytdlTempDirs tracks temp directories created by ResolveYTDLTrack for cleanup.
var ytdlTempDirs []string

// CleanupYTDL removes all temp files created by yt-dlp downloads.
func CleanupYTDL() {
	for _, d := range ytdlTempDirs {
		os.RemoveAll(d)
	}
}

// resolveYTDL uses yt-dlp --flat-playlist to quickly enumerate tracks.
// Tracks are returned with their page URLs as Path (not direct audio URLs).
// Use ResolveYTDLTrack to lazily resolve individual tracks before playback.
func resolveYTDL(pageURL string) ([]playlist.Track, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, fmt.Errorf("yt-dlp not found in PATH — install it with: brew install yt-dlp")
	}

	cmd := exec.Command("yt-dlp", "--flat-playlist", "-j", pageURL)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("yt-dlp: %s", msg)
		}
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}

	var tracks []playlist.Track
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e ytdlFlatEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		trackURL := e.WebpageURL
		if trackURL == "" {
			trackURL = e.URL
		}
		if trackURL == "" {
			continue
		}
		title := e.Title
		if title == "" {
			title = humanizeBasename(e.WebpageURLBasename)
		}
		if title == "" {
			title = trackURL
		}
		artist := e.Uploader
		if artist == "" {
			artist = e.PlaylistUploader
		}
		tracks = append(tracks, playlist.Track{
			Path:   trackURL,
			Title:  title,
			Artist: artist,
			Stream: true,
		})
	}
	return tracks, scanner.Err()
}

// ResolveYTDLTrack downloads a single track via yt-dlp to a temp file and
// returns a Track pointing to the local file. Local files are seekable,
// unlike HTTP streams.
func ResolveYTDLTrack(pageURL string) (playlist.Track, error) {
	tmpDir, err := os.MkdirTemp("", "cliamp-ytdl-")
	if err != nil {
		return playlist.Track{}, fmt.Errorf("creating temp dir: %w", err)
	}
	ytdlTempDirs = append(ytdlTempDirs, tmpDir)

	outTemplate := filepath.Join(tmpDir, "%(id)s.%(ext)s")
	cmd := exec.Command("yt-dlp",
		"-f", "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio",
		"--no-playlist",
		"--print-json",
		"-o", outTemplate,
		pageURL)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		os.RemoveAll(tmpDir)
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return playlist.Track{}, fmt.Errorf("yt-dlp: %s", msg)
		}
		return playlist.Track{}, fmt.Errorf("yt-dlp: %w", err)
	}

	var e ytdlFullEntry
	if err := json.Unmarshal(stdout, &e); err != nil {
		// Best-effort: find the file in tmpDir even if JSON parsing fails.
		e.Filename = findFirstFile(tmpDir)
	}

	filePath := e.Filename
	if filePath == "" {
		filePath = findFirstFile(tmpDir)
	}
	if filePath == "" {
		os.RemoveAll(tmpDir)
		return playlist.Track{}, fmt.Errorf("yt-dlp: no file downloaded for %s", pageURL)
	}

	title := e.Title
	if title == "" {
		title = pageURL
	}
	return playlist.Track{
		Path:   filePath,
		Title:  title,
		Artist: e.Uploader,
		Stream: false,
	}, nil
}

// findFirstFile returns the path of the first file in a directory, or "".
func findFirstFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// humanizeBasename converts a URL basename like "clr-podcast-467" into "clr podcast 467".
func humanizeBasename(s string) string {
	return strings.ReplaceAll(s, "-", " ")
}
