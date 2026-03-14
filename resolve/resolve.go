// Package resolve converts CLI arguments (files, directories, globs, URLs,
// M3U playlists, and RSS feeds) into a flat list of playlist tracks.
package resolve

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"cliamp/player"
	"cliamp/playlist"

	"github.com/kkdai/youtube/v2"
)

// httpClient is used for feed and M3U resolution. It has a generous but
// finite timeout to prevent hanging on unresponsive servers.
var httpClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &uaTransport{rt: http.DefaultTransport},
}

// uaTransport injects the cliamp User-Agent header into every request.
type uaTransport struct{ rt http.RoundTripper }

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "cliamp/1.0 (https://github.com/bjarneo/cliamp)")
	return t.rt.RoundTrip(req)
}

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
			if playlist.IsFeed(arg) || playlist.IsM3U(arg) || playlist.IsPLS(arg) || playlist.IsYouTubeURL(arg) || playlist.IsYTDL(arg) || playlist.IsXiaoyuzhouEpisode(arg) || sniffFeedURL(arg) {
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
			if playlist.IsLocalM3U(path) {
				tracks, err := ResolveLocalM3U(path)
				if err != nil {
					return r, fmt.Errorf("loading m3u %s: %w", path, err)
				}
				r.Tracks = append(r.Tracks, tracks...)
				continue
			}
			if playlist.IsLocalPLS(path) {
				tracks, err := ResolveLocalPLS(path)
				if err != nil {
					return r, fmt.Errorf("loading pls %s: %w", path, err)
				}
				r.Tracks = append(r.Tracks, tracks...)
				continue
			}
			resolved, err := collectAudioFiles(path)
			if err != nil {
				return r, fmt.Errorf("scanning %s: %w", path, err)
			}
			files = append(files, resolved...)
		}
	}

	r.Tracks = append(r.Tracks, scanTracks(files)...)
	return r, nil
}

// Remote fetches feed and M3U URLs and returns the resolved tracks.
func Remote(urls []string) ([]playlist.Track, error) {
	var tracks []playlist.Track
	for _, u := range urls {
		switch {
		case playlist.IsXiaoyuzhouEpisode(u):
			t, err := resolveXiaoyuzhouEpisode(u)
			if err != nil {
				return nil, fmt.Errorf("resolving xiaoyuzhou episode %s: %w", u, err)
		case playlist.IsYouTubeMusicURL(u):
			// YouTube Music requires yt-dlp; the native YouTube API client
			// does not support music.youtube.com playlists.
			t, err := resolveYTDL(u)
			if err != nil {
				return nil, fmt.Errorf("resolving youtube music %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		case playlist.IsYouTubeURL(u):
			t, err := resolveYouTube(u)
			if err != nil {
				return nil, fmt.Errorf("resolving youtube %s: %w", u, err)
			}
			tracks = append(tracks, t...)
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
			t, err := resolveM3U(u)
			if err != nil {
				return nil, fmt.Errorf("resolving m3u %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		case playlist.IsPLS(u):
			t, err := resolvePLS(u)
			if err != nil {
				return nil, fmt.Errorf("resolving pls %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		default:
			// URL was classified as a feed by content-type sniffing.
			t, err := resolveFeed(u)
			if err != nil {
				return nil, fmt.Errorf("resolving feed %s: %w", u, err)
			}
			tracks = append(tracks, t...)
		}
	}
	return tracks, nil
}

// sniffFeedURL does a HEAD request and returns true if the Content-Type
// indicates an RSS/Atom feed. Used as a fallback when the URL has no
// recognizable file extension (e.g. https://feeds.megaphone.fm/GLT1412515089).
func sniffFeedURL(rawURL string) bool {
	resp, err := httpClient.Head(rawURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)
	switch mediaType {
	case "application/rss+xml", "application/atom+xml",
		"application/xml", "text/xml":
		return true
	}
	return false
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

// scanTracks converts file paths to Tracks concurrently, preserving order.
func scanTracks(files []string) []playlist.Track {
	if len(files) == 0 {
		return nil
	}
	tracks := make([]playlist.Track, len(files))
	workers := min(len(files), 8)
	var wg sync.WaitGroup
	ch := make(chan int, len(files))
	for i := range files {
		ch <- i
	}
	close(ch)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range ch {
				tracks[i] = playlist.TrackFromPath(files[i])
			}
		}()
	}
	wg.Wait()
	return tracks
}

// resolveFeed fetches a podcast RSS feed and returns tracks with metadata.
func resolveFeed(feedURL string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(feedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}

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

// resolveM3U fetches an M3U playlist URL and returns tracks with EXTINF metadata.
func resolveM3U(m3uURL string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(m3uURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}

	entries, err := parseM3U(resp.Body, "")
	if err != nil {
		return nil, err
	}
	return entriesToTracks(entries), nil
}

// resolvePLS fetches a PLS playlist URL and returns tracks.
func resolvePLS(plsURL string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(plsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}

	entries, err := parsePLS(resp.Body)
	if err != nil {
		return nil, err
	}
	return plsEntriesToTracks(entries), nil
}

// ytdlFlatEntry holds JSON fields from yt-dlp --flat-playlist output.
type ytdlFlatEntry struct {
	URL                string  `json:"url"`
	WebpageURL         string  `json:"webpage_url"`
	Title              string  `json:"title"`
	Uploader           string  `json:"uploader"`
	PlaylistUploader   string  `json:"playlist_uploader"`
	WebpageURLBasename string  `json:"webpage_url_basename"`
	Duration           float64 `json:"duration"`
}

// ytdlFullEntry holds JSON fields from yt-dlp --print-json output (download mode).
type ytdlFullEntry struct {
	Title    string `json:"title"`
	Uploader string `json:"uploader"`
	Filename string `json:"_filename"`
}

// ytdlTempDirs tracks temp directories created by ResolveYTDLTrack for cleanup.
var (
	ytdlTempDirs []string
	ytdlMu       sync.Mutex
)

// CleanupYTDL removes all temp files created by yt-dlp downloads.
func CleanupYTDL() {
	ytdlMu.Lock()
	defer ytdlMu.Unlock()
	for _, d := range ytdlTempDirs {
		os.RemoveAll(d)
	}
	ytdlTempDirs = nil
}

// YTDLRadioInitialItems is the number of tracks fetched in the first pass
// for YouTube Radio/Mix playlists. The UI uses this as the batch offset
// when starting incremental loading.
const YTDLRadioInitialItems = 20

// resolveYouTube uses the kkdai/youtube library to resolve YouTube URLs.
// For playlist URLs it enumerates all entries natively; for single video URLs
// it returns a single track with metadata from the YouTube API.
func resolveYouTube(pageURL string) ([]playlist.Track, error) {
	client := youtube.Client{}

	// Only attempt playlist resolution if the URL contains a "list=" parameter,
	// avoiding a wasted API call for single-video URLs (the common case).
	u, _ := url.Parse(pageURL)
	isList := u != nil && u.Query().Get("list") != ""

	// YouTube Radio/Mix playlists (list=RD...) are dynamically generated and
	// unsupported by the native library. Route them directly to yt-dlp.
	listID := ""
	if u != nil {
		listID = u.Query().Get("list")
	}
	if isList && strings.HasPrefix(listID, "RD") {
		if tracks, err := resolveYTDL(pageURL, YTDLRadioInitialItems); err == nil && len(tracks) > 0 {
			return tracks, nil
		}
	}

	if isList {
		pl, err := client.GetPlaylist(pageURL)
		if err == nil && len(pl.Videos) > 0 {
			tracks := make([]playlist.Track, 0, len(pl.Videos))
			for _, entry := range pl.Videos {
				tracks = append(tracks, playlist.Track{
					Path:         "https://www.youtube.com/watch?v=" + entry.ID,
					Title:        entry.Title,
					Artist:       entry.Author,
					Stream:       true,
					DurationSecs: int(entry.Duration.Seconds()),
				})
			}
			return tracks, nil
		}
		// Native library failed (e.g. YouTube Radio/Mix playlists are dynamic
		// and unsupported). Fall back to yt-dlp which handles them.
		if tracks, err := resolveYTDL(pageURL); err == nil && len(tracks) > 0 {
			return tracks, nil
		}
	}

	// Single video.
	video, err := client.GetVideo(pageURL)
	if err != nil {
		return nil, fmt.Errorf("youtube resolve: %w", err)
	}

	return []playlist.Track{{
		Path:         "https://www.youtube.com/watch?v=" + video.ID,
		Title:        video.Title,
		Artist:       video.Author,
		Stream:       true,
		DurationSecs: int(video.Duration.Seconds()),
	}}, nil
}

// ResolveYTDLBatch is like resolveYTDL but fetches a specific range
// [start, start+count) from the playlist. Exported for UI incremental loading.
// ResolveYTDLBatch fetches tracks starting at offset `start`.
// If count > 0, fetches at most `count` items; if count == 0, fetches all remaining.
func ResolveYTDLBatch(pageURL string, start, count int) ([]playlist.Track, error) {
	end := 0
	if count > 0 {
		end = start + count
	}
	return resolveYTDLRange(pageURL, start, end)
}

// resolveYTDL uses yt-dlp --flat-playlist to quickly enumerate tracks.
// Tracks are returned with their page URLs as Path (not direct audio URLs).
// If maxItems > 0, only the first maxItems tracks are fetched.
func resolveYTDL(pageURL string, maxItems ...int) ([]playlist.Track, error) {
	end := 0
	if len(maxItems) > 0 {
		end = maxItems[0]
	}
	return resolveYTDLRange(pageURL, 0, end)
}

func resolveYTDLRange(pageURL string, start, end int) ([]playlist.Track, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, fmt.Errorf("yt-dlp not found in PATH — see https://github.com/yt-dlp/yt-dlp#installation")
	}

	args := []string{"--flat-playlist", "-j"}
	if start > 0 {
		args = append(args, "--playlist-start", strconv.Itoa(start+1)) // yt-dlp is 1-based
	}
	if end > 0 {
		args = append(args, "--playlist-end", strconv.Itoa(end))
	}
	args = append(args, pageURL)
	cmd := exec.Command("yt-dlp", args...)
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
	// yt-dlp JSON can exceed bufio.Scanner's default 64KB token limit
	// (e.g. videos with very long descriptions).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
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
			Path:         trackURL,
			Title:        title,
			Artist:       artist,
			Stream:       true,
			DurationSecs: int(e.Duration),
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
	ytdlMu.Lock()
	ytdlTempDirs = append(ytdlTempDirs, tmpDir)
	ytdlMu.Unlock()

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

// DownloadYTDL downloads a single track via yt-dlp to the given directory
// and returns the output file path. Uses yt-dlp's default naming template.
func DownloadYTDL(pageURL, saveDir string) (string, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", fmt.Errorf("yt-dlp not found in PATH")
	}

	outTemplate := filepath.Join(saveDir, "%(artist,uploader)s - %(title)s.%(ext)s")
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
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("yt-dlp: %s", msg)
		}
		return "", fmt.Errorf("yt-dlp: %w", err)
	}

	var e ytdlFullEntry
	if err := json.Unmarshal(stdout, &e); err != nil {
		return "", fmt.Errorf("parsing yt-dlp output: %w", err)
	}
	if e.Filename == "" {
		return "", fmt.Errorf("yt-dlp: no file downloaded for %s", pageURL)
	}
	return e.Filename, nil
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
