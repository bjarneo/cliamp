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
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
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
	unlock, err := s.lockFile()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()

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

// Import adds each track that is not yet a favorite and writes the file once.
// The new entries go after the existing ones, in the order given. Tracks with
// an empty path and repeated paths are skipped. Returns the number of tracks
// added.
func (s *Store) Import(tracks []playlist.Track) (int, error) {
	if s == nil || len(tracks) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := s.lockFile()
	if err != nil {
		return 0, err
	}
	defer func() { _ = unlock() }()

	entries, err := s.loadLocked()
	if err != nil {
		return 0, fmt.Errorf("load favorites: %w", err)
	}
	seen := make(map[string]bool, len(entries)+len(tracks))
	for _, e := range entries {
		seen[e.Track.Path] = true
	}
	now := time.Now()
	added := 0
	for _, track := range tracks {
		if strings.TrimSpace(track.Path) == "" || seen[track.Path] {
			continue
		}
		seen[track.Path] = true
		entries = append(entries, Entry{Track: track, FavoritedAt: now})
		added++
	}
	if added == 0 {
		return 0, nil
	}
	return added, s.saveLocked(entries)
}

// IsFavorited reports whether the given path is in the favorites store.
// Read helper: load errors report false rather than failing the caller.
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
// Read helper: load errors report 0 rather than failing the caller.
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

// lockFile serializes writers across cliamp processes: the per-instance
// mutex alone cannot stop two processes from rewriting the same file. It
// creates the config directory first, because a write can run before the
// directory exists.
func (s *Store) lockFile() (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("create favorites dir: %w", err)
	}
	return fileutil.LockFile(s.path + ".lock")
}

func (s *Store) loadLocked() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read favorites: %w", err)
	}
	return parse(data), nil
}

func (s *Store) saveLocked(entries []Entry) error {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		writeEntry(&b, e)
	}
	// WriteFileAtomic uses a unique temp file per write and fsyncs before
	// renaming, so a crash or a second process can never leave a torn or
	// half-clobbered favorites.toml behind.
	return fileutil.WriteFileAtomic(s.path, []byte(b.String()), 0o644)
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintln(w, "[[entry]]")
	fmt.Fprintf(w, "favorited_at = %q\n", e.FavoritedAt.UTC().Format(time.RFC3339))
	playlist.WriteTrackTOML(w, e.Track)
}

// parse skips unknown keys to keep the on-disk format forward-compatible.
// It drops entries without a path, the only required field.
func parse(data []byte) []Entry {
	var entries []Entry
	tomlutil.ParseSections(data, "entry", func(f map[string]string) {
		e := Entry{Track: playlist.TrackFromTOML(f)}
		if strings.TrimSpace(e.Track.Path) == "" {
			return
		}
		if t, err := time.Parse(time.RFC3339, f["favorited_at"]); err == nil {
			e.FavoritedAt = t
		}
		entries = append(entries, e)
	})
	return entries
}
