package resolve

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cliamp/playlist"
)

// m3uEntry holds a single parsed M3U entry with optional EXTINF metadata.
type m3uEntry struct {
	Path     string
	Title    string
	Duration int // seconds, -1 if unknown
}

// parseM3U reads an M3U stream and extracts entries with EXTINF metadata.
// Relative paths are resolved against baseDir (empty for remote M3U).
// Handles UTF-8 BOM, \r\n line endings, missing #EXTM3U header, and bare
// entries without EXTINF lines.
func parseM3U(r io.Reader, baseDir string) ([]m3uEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1 MB max line — handles large EXTINF metadata
	var entries []m3uEntry
	var pending *m3uEntry // EXTINF parsed, waiting for path line

	for scanner.Scan() {
		line := scanner.Text()
		// Strip UTF-8 BOM if present (common in Windows-created M3U files).
		line = strings.TrimPrefix(line, "\xef\xbb\xbf")
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		// Skip the #EXTM3U header.
		if strings.HasPrefix(line, "#EXTM3U") {
			continue
		}

		// Parse #EXTINF:duration,title
		if strings.HasPrefix(line, "#EXTINF:") {
			info := strings.TrimPrefix(line, "#EXTINF:")
			dur := -1
			title := ""
			if comma := strings.IndexByte(info, ','); comma >= 0 {
				if d, err := strconv.Atoi(strings.TrimSpace(info[:comma])); err == nil {
					dur = d
				}
				title = strings.TrimSpace(info[comma+1:])
			}
			pending = &m3uEntry{Duration: dur, Title: title}
			continue
		}

		// Skip other comment/directive lines.
		if strings.HasPrefix(line, "#") {
			continue
		}

		// This is a path/URL line.
		path := line
		if baseDir != "" && !playlist.IsURL(path) && !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}

		if pending != nil {
			pending.Path = path
			entries = append(entries, *pending)
			pending = nil
		} else {
			entries = append(entries, m3uEntry{Path: path, Duration: -1})
		}
	}

	return entries, scanner.Err()
}

// m3uEntryToTrack converts a parsed M3U entry to a playlist.Track.
func m3uEntryToTrack(e m3uEntry) playlist.Track {
	isURL := playlist.IsURL(e.Path)
	duration := 0
	if e.Duration > 0 {
		duration = e.Duration
	}
	realtime := isURL && e.Duration < 0

	if e.Title != "" {
		return playlist.Track{
			Path:         e.Path,
			Title:        e.Title,
			Stream:       isURL,
			Realtime:     realtime,
			DurationSecs: duration,
		}
	}
	t := playlist.TrackFromPath(e.Path)
	t.Realtime = realtime
	t.DurationSecs = duration
	return t
}

// entriesToTracks converts parsed M3U entries to playlist tracks.
func entriesToTracks(entries []m3uEntry) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(entries))
	for _, e := range entries {
		tracks = append(tracks, m3uEntryToTrack(e))
	}
	return tracks
}

// ResolveLocalM3U opens a local .m3u/.m3u8 file, parses it with EXTINF
// metadata, and returns the resulting tracks. Relative paths in the M3U
// are resolved against the directory containing the M3U file.
func ResolveLocalM3U(path string) ([]playlist.Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := parseM3U(f, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return entriesToTracks(entries), nil
}
