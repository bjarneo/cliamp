package resolve

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"cliamp/playlist"
)

// plsEntry holds a single parsed PLS entry.
type plsEntry struct {
	Num   int
	File  string
	Title string
}

// parsePLS reads a PLS (INI-style) playlist and returns entries sorted by number.
//
// Format:
//
//	[playlist]
//	File1=http://example.com/stream
//	Title1=Station Name
//	Length1=-1
//	NumberOfEntries=1
//	Version=2
func parsePLS(r io.Reader) ([]plsEntry, error) {
	files := map[int]string{}
	titles := map[int]string{}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, ";") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		lower := strings.ToLower(key)
		switch {
		case strings.HasPrefix(lower, "file"):
			if n, err := strconv.Atoi(key[4:]); err == nil {
				files[n] = val
			}
		case strings.HasPrefix(lower, "title"):
			if n, err := strconv.Atoi(key[5:]); err == nil {
				titles[n] = val
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no entries found in PLS playlist")
	}

	nums := make([]int, 0, len(files))
	for n := range files {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	entries := make([]plsEntry, 0, len(nums))
	for _, n := range nums {
		entries = append(entries, plsEntry{
			Num:   n,
			File:  files[n],
			Title: titles[n],
		})
	}
	return entries, nil
}

// plsEntriesToTracks converts parsed PLS entries to playlist tracks.
// Radio PLS files list multiple mirror servers for the same stream;
// when all entries are stream URLs we collapse them to a single track
// using the first URL (matching VLC/Winamp behavior).
func plsEntriesToTracks(entries []plsEntry) []playlist.Track {
	if allStreams(entries) {
		e := entries[0]
		title := e.Title
		// Strip the mirror suffix like "(#1)" from radio station titles.
		title = stripMirrorSuffix(title)
		if title == "" {
			return []playlist.Track{playlist.TrackFromPath(e.File)}
		}
		return []playlist.Track{{
			Path:     e.File,
			Title:    title,
			Stream:   true,
			Realtime: true,
		}}
	}

	tracks := make([]playlist.Track, 0, len(entries))
	for _, e := range entries {
		if e.Title != "" {
			tracks = append(tracks, playlist.Track{
				Path:   e.File,
				Title:  e.Title,
				Stream: playlist.IsURL(e.File),
			})
		} else {
			tracks = append(tracks, playlist.TrackFromPath(e.File))
		}
	}
	return tracks
}

// allStreams reports whether every PLS entry is an HTTP stream URL.
func allStreams(entries []plsEntry) bool {
	for _, e := range entries {
		if !playlist.IsURL(e.File) {
			return false
		}
	}
	return len(entries) >= 1
}

// stripMirrorSuffix removes a trailing " (#N)" or " #N" suffix that radio
// PLS files use to distinguish mirror servers (e.g. "Groove Salad (#3)").
func stripMirrorSuffix(s string) string {
	// Handle "(#N)" suffix.
	if i := strings.LastIndex(s, "(#"); i >= 0 && strings.HasSuffix(s, ")") {
		return strings.TrimRight(s[:i], " :")
	}
	return s
}

// ResolveLocalPLS opens a local .pls file, parses it, and returns the
// resulting tracks.
func ResolveLocalPLS(path string) ([]playlist.Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := parsePLS(f)
	if err != nil {
		return nil, err
	}
	return plsEntriesToTracks(entries), nil
}
