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
	"strconv"
	"strings"

	"cliamp/internal/appdir"
	"cliamp/internal/tomlutil"
	"cliamp/playlist"
	"cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.PlaylistWriter  = (*Provider)(nil)
	_ provider.PlaylistDeleter = (*Provider)(nil)
)

// Provider reads and writes TOML-based playlists stored on disk.
type Provider struct {
	dir string // e.g. ~/.config/cliamp/playlists/
}

// New creates a Provider using ~/.config/cliamp/playlists/ as the base directory.
func New() *Provider {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Provider{dir: filepath.Join(dir, "playlists")}
}

func (p *Provider) Name() string { return "Local Playlists" }

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

// Playlists scans the directory for .toml files and returns their metadata.
// Returns an empty list (not error) when the directory doesn't exist.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var lists []playlist.PlaylistInfo
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
			ID:         name,
			Name:       name,
			TrackCount: len(tracks),
		})
	}
	return lists, nil
}

// Tracks parses the TOML file for the given playlist name and returns its tracks.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	path, err := p.safePath(playlistID)
	if err != nil {
		return nil, err
	}
	return p.loadTOML(path)
}

// AddTrack appends a track to the named playlist, creating the directory and
// file if needed.
func (p *Provider) AddTrack(playlistName string, track playlist.Track) error {
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

// Exists reports whether a playlist with the given name exists on disk.
func (p *Provider) Exists(name string) bool {
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

// SetFavorite toggles the favorite flag on a track and rewrites the playlist.
func (p *Provider) SetFavorite(playlistName string, idx int) error {
	tracks, err := p.loadTOMLByName(playlistName)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(tracks) {
		return fmt.Errorf("index %d out of range (playlist has %d tracks)", idx, len(tracks))
	}
	tracks[idx].Favorite = !tracks[idx].Favorite
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
	return p.savePlaylist(name, tracks)
}

// AddTrackToPlaylist appends a track to the named playlist.
// Implements provider.PlaylistWriter.
func (p *Provider) AddTrackToPlaylist(_ context.Context, playlistID string, track playlist.Track) error {
	return p.AddTrack(playlistID, track)
}

// DeletePlaylist removes the TOML file for the named playlist.
func (p *Provider) DeletePlaylist(name string) error {
	path, err := p.safePath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// RemoveTrack removes a track by index from the named playlist.
// If the playlist becomes empty after removal, the file is deleted.
func (p *Provider) RemoveTrack(name string, index int) error {
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
	if t.Favorite {
		fmt.Fprintln(w, "favorite = true")
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
	var current *playlist.Track

	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)

		// Skip comments and blank lines.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// New track section.
		if line == "[[track]]" {
			if current != nil {
				tracks = append(tracks, *current)
			}
			current = &playlist.Track{}
			continue
		}

		if current == nil {
			continue
		}

		// Parse key = "value" lines.
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = tomlutil.Unquote(val)

		switch key {
		case "path":
			current.Path = val
			current.Stream = playlist.IsURL(val)
		case "feed":
			current.Feed = val == "true"
		case "title":
			current.Title = val
		case "artist":
			current.Artist = val
		case "album":
			current.Album = val
		case "genre":
			current.Genre = val
		case "year":
			if n, err := strconv.Atoi(val); err == nil {
				current.Year = n
			}
		case "track_number":
			if n, err := strconv.Atoi(val); err == nil {
				current.TrackNumber = n
			}
		case "duration_secs":
			if n, err := strconv.Atoi(val); err == nil {
				current.DurationSecs = n
			}
		case "favorite":
			current.Favorite = val == "true"
		}
	}
	if current != nil {
		tracks = append(tracks, *current)
	}
	return tracks, nil
}
