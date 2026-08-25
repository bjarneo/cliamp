package resolve

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// plsEntry holds a single parsed PLS entry.
type plsEntry struct {
	Num       int
	File      string
	Title     string
	Length    int
	HasLength bool
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
	lengths := map[int]int{}

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
		case strings.HasPrefix(lower, "length"):
			if n, err := strconv.Atoi(key[6:]); err == nil {
				if length, err := strconv.Atoi(val); err == nil {
					lengths[n] = length
				}
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
		length, hasLength := lengths[n]
		entries = append(entries, plsEntry{
			Num:       n,
			File:      files[n],
			Title:     titles[n],
			Length:    length,
			HasLength: hasLength,
		})
	}
	return entries, nil
}

// plsEntriesToTracks converts parsed PLS entries to playlist tracks.
// Radio PLS files list multiple mirror servers for the same stream. Explicit
// negative lengths or matching "(#N)" titles identify mirrors that should be
// collapsed to the first URL (matching VLC/Winamp behavior).
func plsEntriesToTracks(entries []plsEntry) []playlist.Track {
	if radioMirrors(entries) {
		e := entries[0]
		title := e.Title
		// Strip the mirror suffix like "(#1)" from radio station titles.
		title = stripMirrorSuffix(title)
		if title == "" {
			track := playlist.TrackFromPath(e.File)
			track.Realtime = true
			return []playlist.Track{track}
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
		realtime := playlist.IsURL(e.File) && e.HasLength && e.Length < 0
		if e.Title != "" {
			tracks = append(tracks, playlist.Track{
				Path:         e.File,
				Title:        e.Title,
				Stream:       playlist.IsURL(e.File),
				Realtime:     realtime,
				DurationSecs: max(e.Length, 0),
			})
		} else {
			track := playlist.TrackFromPath(e.File)
			track.Realtime = realtime
			track.DurationSecs = max(e.Length, 0)
			tracks = append(tracks, track)
		}
	}
	return tracks
}

func radioMirrors(entries []plsEntry) bool {
	if !allStreams(entries) {
		return false
	}
	allIndefinite := true
	for _, entry := range entries {
		if entry.HasLength && entry.Length >= 0 {
			return false
		}
		allIndefinite = allIndefinite && entry.HasLength && entry.Length < 0
	}
	if allIndefinite {
		return true
	}
	if len(entries) < 2 {
		return false
	}
	name := stripMirrorSuffix(entries[0].Title)
	if name == "" || name == entries[0].Title {
		return false
	}
	for _, entry := range entries[1:] {
		if stripMirrorSuffix(entry.Title) != name || stripMirrorSuffix(entry.Title) == entry.Title {
			return false
		}
	}
	return true
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

// resolveLocalPLS opens a local .pls file, parses it, and returns the
// resulting tracks.
func resolveLocalPLS(path string) ([]playlist.Track, error) {
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
