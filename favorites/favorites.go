// Package favorites persists the user's favorite tracks to a TOML file in the
// cliamp config directory. Favorites are explicitly toggled by the user and
// span all playlists — a track favorited in playlist A appears when browsing
// the virtual "Favorites" playlist regardless of where it was starred.
//
// The store is safe for concurrent callers and writes atomically (temp file +
// rename) so a crash mid-write cannot leave a half-finished favorites.toml.
package favorites

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
)

// PlaylistName is the virtual playlist name surfaced to the UI by the local
// provider. Browsing this name returns favorite tracks newest-first.
const PlaylistName = "Favorites"

// Entry pairs a track with the wall-clock time it was favorited.
type Entry struct {
	Track       playlist.Track
	FavoritedAt time.Time
}

// Store reads and writes the favorites TOML file.
type Store struct {
	path string

	mu sync.Mutex
}

// New returns a Store backed by ~/.config/cliamp/favorites.toml. Returns nil if
// the config directory cannot be resolved.
func New() *Store {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Store{path: filepath.Join(dir, "favorites.toml")}
}

// NewAt returns a Store rooted at an explicit file path. Used by tests.
func NewAt(path string) *Store {
	return &Store{path: path}
}

// Path returns the on-disk file path.
func (s *Store) Path() string { return s.path }

// Toggle favorites a track. If the track is already favorited, it is removed
// (unfavorited). Returns true when the track is now favorited after the call.
// Empty paths are ignored and return false.
func (s *Store) Toggle(track playlist.Track) (bool, error) {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	idx := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Track.Path == track.Path
	})

	if idx >= 0 {
		// Already favorited — remove it.
		entries = slices.Delete(entries, idx, idx+1)
		return false, s.saveLocked(entries)
	}

	// Not yet favorited — add it at the front (newest first).
	entry := Entry{Track: track, FavoritedAt: time.Now()}
	entries = append([]Entry{entry}, entries...)
	return true, s.saveLocked(entries)
}

// Favorite adds a track to favorites. No-op if already present.
// Returns true when the track was newly added.
func (s *Store) Favorite(track playlist.Track) (bool, error) {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	if slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Track.Path == track.Path
	}) {
		return false, nil
	}

	entry := Entry{Track: track, FavoritedAt: time.Now()}
	entries = append([]Entry{entry}, entries...)
	return true, s.saveLocked(entries)
}

// Remove unfavorites a track by path. Returns true when the track was present
// and removed.
func (s *Store) Remove(path string) (bool, error) {
	if s == nil || strings.TrimSpace(path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	idx := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Track.Path == path
	})
	if idx < 0 {
		return false, nil
	}

	entries = slices.Delete(entries, idx, idx+1)
	return true, s.saveLocked(entries)
}

// IsFavorited reports whether the given path is in the favorites store.
func (s *Store) IsFavorited(path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Track.Path == path
	})
}

// Count returns the number of favorited tracks.
func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return 0
	}
	return len(entries)
}

// Tracks returns all favorite tracks, newest-first, suitable for handing to a
// playlist.Playlist. The FavoritedAt timestamp is dropped.
func (s *Store) Tracks() ([]playlist.Track, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]playlist.Track, len(entries))
	for i, e := range entries {
		out[i] = e.Track
	}
	return out, nil
}

// Clear deletes the favorites file. Returns nil if the file does not exist.
func (s *Store) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) loadLocked() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(data), nil
}

func (s *Store) saveLocked(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		writeEntry(&b, e)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintf(w, "[[entry]]\n")
	fmt.Fprintf(w, "favorited_at = %q\n", e.FavoritedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "path = %q\n", e.Track.Path)
	fmt.Fprintf(w, "title = %q\n", e.Track.Title)
	if e.Track.Artist != "" {
		fmt.Fprintf(w, "artist = %q\n", e.Track.Artist)
	}
	if e.Track.Album != "" {
		fmt.Fprintf(w, "album = %q\n", e.Track.Album)
	}
	if e.Track.Genre != "" {
		fmt.Fprintf(w, "genre = %q\n", e.Track.Genre)
	}
	if e.Track.Year != 0 {
		fmt.Fprintf(w, "year = %d\n", e.Track.Year)
	}
	if e.Track.TrackNumber != 0 {
		fmt.Fprintf(w, "track_number = %d\n", e.Track.TrackNumber)
	}
	if e.Track.DurationSecs != 0 {
		fmt.Fprintf(w, "duration_secs = %d\n", e.Track.DurationSecs)
	}
}

// parse skips unknown keys to keep the on-disk format forward-compatible.
func parse(data []byte) []Entry {
	var entries []Entry
	var cur *Entry

	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
		}
	}

	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[entry]]" {
			flush()
			cur = &Entry{}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = tomlutil.Unquote(strings.TrimSpace(val))
		switch key {
		case "favorited_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				cur.FavoritedAt = t
			}
		case "path":
			cur.Track.Path = val
			cur.Track.Stream = playlist.IsURL(val)
		case "title":
			cur.Track.Title = val
		case "artist":
			cur.Track.Artist = val
		case "album":
			cur.Track.Album = val
		case "genre":
			cur.Track.Genre = val
		case "year":
			if n, err := strconv.Atoi(val); err == nil {
				cur.Track.Year = n
			}
		case "track_number":
			if n, err := strconv.Atoi(val); err == nil {
				cur.Track.TrackNumber = n
			}
		case "duration_secs":
			if n, err := strconv.Atoi(val); err == nil {
				cur.Track.DurationSecs = n
			}
		}
	}
	flush()

	// Drop entries that failed to parse a path (the only required field).
	entries = slices.DeleteFunc(entries, func(e Entry) bool {
		return strings.TrimSpace(e.Track.Path) == ""
	})
	return entries
}
