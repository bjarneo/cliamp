// Package local implements a playlist.Provider backed by TOML files in
// ~/.config/cliamp/playlists/.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"cliamp/history"
	"cliamp/internal/appdir"
	"cliamp/internal/fuzzy"
	"cliamp/internal/tomlutil"
	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.PlaylistWriter  = (*Provider)(nil)
	_ provider.PlaylistDeleter = (*Provider)(nil)
	_ provider.PlaylistRenamer = (*Provider)(nil)
	_ provider.Searcher        = (*Provider)(nil)
)

// Provider reads and writes TOML-based playlists stored on disk.
type Provider struct {
	dir     string // e.g. ~/.config/cliamp/playlists/
	history *history.Store
}

// New creates a Provider using ~/.config/cliamp/playlists/ as the base directory.
func New() *Provider {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Provider{
		dir:     filepath.Join(dir, "playlists"),
		history: history.New(),
	}
}

func (p *Provider) Name() string { return "Local" }

// safePath validates a playlist name and returns the absolute path to its TOML
// file, ensuring the result stays within p.dir. This prevents path traversal
// via names containing ".." or path separators.
func (p *Provider) safePath(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") || name == ".." || name == "." || name == "" {
		return "", fmt.Errorf("invalid playlist name %q", name)
	}
	resolved := filepath.Join(p.dir, name+".toml")
	if !strings.HasPrefix(resolved, filepath.Clean(p.dir)+string(filepath.Separator)) {
		return "", fmt.Errorf("playlist path escapes base directory")
	}
	return resolved, nil
}

func isHistoryName(name string) bool {
	return name == history.PlaylistName
}

// Playlists scans the directory for .toml files and returns their metadata,
// prepending the virtual "Recently Played" entry when the user has any
// recorded plays. Returns an empty list (not error) when neither exists.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	var lists []playlist.PlaylistInfo
	if info, ok := p.historyInfo(); ok {
		lists = append(lists, info)
	}

	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return lists, nil
	}
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		tracks, err := p.loadTOML(filepath.Join(p.dir, e.Name()))
		if err != nil {
			continue
		}
		lists = append(lists, playlist.PlaylistInfo{
			ID:           name,
			Name:         name,
			TrackCount:   len(tracks),
			DurationSecs: playlist.TotalDurationSecs(tracks),
		})
	}
	return lists, nil
}

// historyInfo returns the synthetic PlaylistInfo entry for "Recently Played",
// or ok=false when the history store is unavailable or empty.
func (p *Provider) historyInfo() (playlist.PlaylistInfo, bool) {
	if p.history == nil {
		return playlist.PlaylistInfo{}, false
	}
	tracks, err := p.history.Tracks(0)
	if err != nil || len(tracks) == 0 {
		return playlist.PlaylistInfo{}, false
	}
	return playlist.PlaylistInfo{
		ID:           history.PlaylistName,
		Name:         history.PlaylistName,
		TrackCount:   len(tracks),
		DurationSecs: playlist.TotalDurationSecs(tracks),
	}, true
}

// Tracks parses the TOML file for the given playlist name and returns its tracks.
// The reserved "Recently Played" name is served from the history store.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	if isHistoryName(playlistID) {
		if p.history == nil {
			return nil, nil
		}
		return p.history.Tracks(0)
	}
	path, err := p.safePath(playlistID)
	if err != nil {
		return nil, err
	}
	return p.loadTOML(path)
}

// AddTrack appends a track to the named playlist, creating the directory and
// file if needed.
func (p *Provider) AddTrack(playlistName string, track playlist.Track) error {
	if isHistoryName(playlistName) {
		return errReservedHistoryName
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return err
	}

	path, err := p.safePath(playlistName)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Add a blank line before the section if file is non-empty.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		fmt.Fprintln(f)
	}

	writeTrack(f, track)
	return nil
}

// AddTracks appends multiple tracks in a single file open/close cycle.
func (p *Provider) AddTracks(playlistName string, tracks []playlist.Track) error {
	if isHistoryName(playlistName) {
		return errReservedHistoryName
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return err
	}
	path, err := p.safePath(playlistName)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	nonEmpty := info.Size() > 0
	for _, t := range tracks {
		if nonEmpty {
			fmt.Fprintln(f)
		}
		writeTrack(f, t)
		nonEmpty = true
	}
	return nil
}

// Exists reports whether a playlist with the given name exists on disk, or
// whether it refers to the virtual "Recently Played" history with at least
// one entry recorded.
func (p *Provider) Exists(name string) bool {
	if isHistoryName(name) {
		_, ok := p.historyInfo()
		return ok
	}
	path, err := p.safePath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// savePlaylist overwrites the named playlist with the given tracks.
func (p *Provider) savePlaylist(name string, tracks []playlist.Track) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return err
	}

	path, err := p.safePath(name)
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	for i, t := range tracks {
		if i > 0 {
			fmt.Fprintln(f)
		}
		writeTrack(f, t)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// errReservedHistoryName is returned when a caller tries to write to or
// otherwise mutate the synthetic history playlist.
var errReservedHistoryName = errors.New(`"Recently Played" is a virtual history playlist and cannot be modified`)

// SetBookmark toggles the bookmark flag on a track and rewrites the playlist.
func (p *Provider) SetBookmark(playlistName string, idx int) error {
	if isHistoryName(playlistName) {
		return errReservedHistoryName
	}
	tracks, err := p.loadTOMLByName(playlistName)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(tracks) {
		return fmt.Errorf("index %d out of range (playlist has %d tracks)", idx, len(tracks))
	}
	tracks[idx].Bookmark = !tracks[idx].Bookmark
	return p.savePlaylist(playlistName, tracks)
}

// loadTOMLByName loads tracks for a named playlist.
func (p *Provider) loadTOMLByName(name string) ([]playlist.Track, error) {
	path, err := p.safePath(name)
	if err != nil {
		return nil, err
	}
	return p.loadTOML(path)
}

// SavePlaylist overwrites a playlist with the given tracks.
func (p *Provider) SavePlaylist(name string, tracks []playlist.Track) error {
	if isHistoryName(name) {
		return errReservedHistoryName
	}
	return p.savePlaylist(name, tracks)
}

// AddTrackToPlaylist appends a track to the named playlist.
// Implements provider.PlaylistWriter.
func (p *Provider) AddTrackToPlaylist(_ context.Context, playlistID string, track playlist.Track) error {
	return p.AddTrack(playlistID, track)
}

// SearchTracks does a case-insensitive fuzzy search across every saved playlist
// for tracks whose title, artist, or album match query, ranked by relevance
// (best match first). Returns up to limit results (limit <= 0 means no cap).
// Implements provider.Searcher.
func (p *Provider) SearchTracks(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	type scored struct {
		track playlist.Track
		score int
	}
	var matches []scored
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		tracks, err := p.loadTOML(filepath.Join(p.dir, e.Name()))
		if err != nil {
			continue
		}
		for _, t := range tracks {
			if _, dup := seen[t.Path]; dup {
				continue
			}
			score, ok := trackMatchScore(t, q)
			if !ok {
				continue
			}
			seen[t.Path] = struct{}{}
			matches = append(matches, scored{t, score})
		}
	}

	sort.SliceStable(matches, func(a, b int) bool {
		return matches[a].score > matches[b].score
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	out := make([]playlist.Track, len(matches))
	for i, m := range matches {
		out[i] = m.track
	}
	return out, nil
}

// trackMatchScore returns the best fuzzy score for query across the track's
// title, artist, and album, and whether any of them matched.
func trackMatchScore(t playlist.Track, query string) (int, bool) {
	best, ok := 0, false
	for _, field := range [...]string{t.Title, t.Artist, t.Album} {
		if field == "" {
			continue
		}
		if s, matched := fuzzy.Match(query, field); matched && (!ok || s > best) {
			best, ok = s, true
		}
	}
	return best, ok
}

// RenamePlaylist renames a playlist by renaming its TOML file.
// The reserved "Recently Played" history playlist cannot be renamed.
func (p *Provider) RenamePlaylist(oldName, newName string) error {
	if isHistoryName(oldName) || isHistoryName(newName) {
		return errReservedHistoryName
	}
	oldPath, err := p.safePath(oldName)
	if err != nil {
		return fmt.Errorf("invalid playlist name %q: %w", oldName, err)
	}
	newPath, err := p.safePath(newName)
	if err != nil {
		return fmt.Errorf("invalid playlist name %q: %w", newName, err)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("playlist %q already exists", newName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination playlist %q: %w", newName, err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename playlist %q to %q: %w", oldName, newName, err)
	}
	return nil
}

// DeletePlaylist removes the TOML file for the named playlist.
// "Recently Played" cannot be deleted via this method — use ClearHistory.
func (p *Provider) DeletePlaylist(name string) error {
	if isHistoryName(name) {
		return errReservedHistoryName
	}
	path, err := p.safePath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// ClearHistory wipes the recorded play history. Returns nil if no history
// exists yet.
func (p *Provider) ClearHistory() error {
	if p.history == nil {
		return nil
	}
	return p.history.Clear()
}

// RemoveTrack removes a track by index from the named playlist.
// If the playlist becomes empty after removal, the file is deleted.
func (p *Provider) RemoveTrack(name string, index int) error {
	if isHistoryName(name) {
		return errReservedHistoryName
	}
	tracks, err := p.Tracks(name)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(tracks) {
		return fmt.Errorf("track index %d out of range", index)
	}
	tracks = slices.Delete(tracks, index, index+1)
	if len(tracks) == 0 {
		return p.DeletePlaylist(name)
	}
	return p.savePlaylist(name, tracks)
}

// writeTrack writes a single [[track]] TOML section to w.
func writeTrack(w io.Writer, t playlist.Track) {
	fmt.Fprintln(w, "[[track]]")
	fmt.Fprintf(w, "path = %q\n", t.Path)
	fmt.Fprintf(w, "title = %q\n", t.Title)
	if t.Feed {
		fmt.Fprintln(w, "feed = true")
	}
	if t.Artist != "" {
		fmt.Fprintf(w, "artist = %q\n", t.Artist)
	}
	if t.Album != "" {
		fmt.Fprintf(w, "album = %q\n", t.Album)
	}
	if t.Genre != "" {
		fmt.Fprintf(w, "genre = %q\n", t.Genre)
	}
	if t.Year != 0 {
		fmt.Fprintf(w, "year = %d\n", t.Year)
	}
	if t.TrackNumber != 0 {
		fmt.Fprintf(w, "track_number = %d\n", t.TrackNumber)
	}
	if t.DurationSecs != 0 {
		fmt.Fprintf(w, "duration_secs = %d\n", t.DurationSecs)
	}
	if t.Bookmark {
		fmt.Fprintln(w, "bookmark = true")
	}
}

// loadTOML parses a minimal TOML file with [[track]] sections.
// Each section supports path, title, and artist keys.
func (p *Provider) loadTOML(path string) ([]playlist.Track, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var tracks []playlist.Track
	tomlutil.ParseSections(data, "track", func(f map[string]string) {
		t := playlist.Track{
			Path:   f["path"],
			Title:  f["title"],
			Artist: f["artist"],
			Album:  f["album"],
			Genre:  f["genre"],
			Feed:   f["feed"] == "true",
		}
		t.Stream = playlist.IsURL(t.Path)
		// "favorite" is the pre-rename alias for "bookmark"; prefer bookmark.
		bookmark, ok := f["bookmark"]
		if !ok {
			bookmark = f["favorite"]
		}
		t.Bookmark = bookmark == "true"
		if n, err := strconv.Atoi(f["year"]); err == nil {
			t.Year = n
		}
		if n, err := strconv.Atoi(f["track_number"]); err == nil {
			t.TrackNumber = n
		}
		if n, err := strconv.Atoi(f["duration_secs"]); err == nil {
			t.DurationSecs = n
		}
		tracks = append(tracks, t)
	})
	return tracks, nil
}
