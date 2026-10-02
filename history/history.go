// Package history persists the user's recently played tracks to a TOML file
// in the cliamp config directory. cliamp records a track when the track
// starts to play, in the TUI and in headless mode. Skipped tracks and live
// streams also enter the list. The list holds each path one time only.
//
// The store is safe for concurrent callers. Writers also take a file lock, so
// two cliamp processes cannot overwrite each other's entries. It writes
// atomically (temp file + rename) so a crash mid-write cannot leave a
// half-finished history.toml.
package history

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
)

// DefaultCap is the maximum number of entries kept on disk. Older entries are
// dropped FIFO once the cap is exceeded.
const DefaultCap = 200

// PlaylistName is the virtual playlist name surfaced to the UI by the local
// provider. Browsing this name returns history entries newest-first.
const PlaylistName = "Recently Played"

// Entry pairs a track with the wall-clock time it started to play.
type Entry struct {
	Track    playlist.Track
	PlayedAt time.Time
}

// Store reads and writes the history TOML file.
type Store struct {
	path string
	cap  int

	mu sync.Mutex
}

// New returns a Store backed by ~/.config/cliamp/history.toml. Returns nil if
// the config directory cannot be resolved (rare; same failure mode as the
// local playlist provider).
func New() *Store {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Store{path: filepath.Join(dir, "history.toml"), cap: DefaultCap}
}

// NewAt returns a Store rooted at an explicit file path. Used by tests.
func NewAt(path string) *Store {
	return &Store{path: path, cap: DefaultCap}
}

// Path returns the on-disk file path.
func (s *Store) Path() string { return s.path }

// Record puts track at the top of the list with playedAt as its time. When
// the path is already in the list, the entry moves to the top, however long
// ago it played, and keeps its stored metadata where track has none. So
// Recently Played shows distinct tracks in listen order, not play counts.
// Record ignores an empty path. It does not check the duration or Realtime.
func (s *Store) Record(track playlist.Track, playedAt time.Time) error {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	entries, err := s.loadLocked()
	if err != nil {
		// Don't clobber existing on-disk history on a transient read failure:
		// proceeding would rewrite the file with only the new entry.
		return fmt.Errorf("load history: %w", err)
	}
	for i, e := range entries {
		if e.Track.Path != track.Path {
			continue
		}
		merged := mergeTrackMeta(e.Track, track)
		entries = append(entries[:i], entries[i+1:]...)
		entries = append([]Entry{{Track: merged, PlayedAt: playedAt}}, entries...)
		return s.saveLocked(entries)
	}
	entry := Entry{Track: track, PlayedAt: playedAt}
	entries = append([]Entry{entry}, entries...)
	if s.cap > 0 && len(entries) > s.cap {
		entries = entries[:s.cap]
	}
	return s.saveLocked(entries)
}

// Recent returns up to limit entries, newest first. limit <= 0 returns all.
func (s *Store) Recent(limit int) ([]Entry, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// Tracks returns up to limit recent tracks, newest first, suitable for handing
// to a playlist.Playlist. The PlayedAt timestamp is dropped.
func (s *Store) Tracks(limit int) ([]playlist.Track, error) {
	entries, err := s.Recent(limit)
	if err != nil {
		return nil, err
	}
	out := make([]playlist.Track, len(entries))
	for i, e := range entries {
		out[i] = e.Track
	}
	return out, nil
}

// Clear deletes the history file. Returns nil if the file does not exist.
func (s *Store) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	err = os.Remove(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// lockFile serializes writers across cliamp processes: the per-instance
// mutex alone cannot stop two processes from rewriting the same file. It
// creates the config directory first, because Record creates history.toml
// there when the directory does not exist yet.
func (s *Store) lockFile() (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}
	return fileutil.LockFile(s.path + ".lock")
}

func (s *Store) loadLocked() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dedupeNewestFirst(parse(data)), nil
}

// dedupeNewestFirst collapses repeated paths, keeping the newest occurrence.
// History written by older versions could contain duplicate rows for the same
// track; this heals them on read so the list always shows distinct tracks.
func dedupeNewestFirst(entries []Entry) []Entry {
	seen := make(map[string]struct{}, len(entries))
	clean := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if _, dup := seen[e.Track.Path]; dup {
			continue
		}
		seen[e.Track.Path] = struct{}{}
		clean = append(clean, e)
	}
	return clean
}

func (s *Store) saveLocked(entries []Entry) error {
	// Build the full content in memory (writes to a Builder can't fail), then
	// write a unique temp file and rename so a partial/failed write — or a
	// second cliamp process — can never truncate or clobber existing history.
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		writeEntry(&b, e)
	}
	return fileutil.WriteFileAtomic(s.path, []byte(b.String()), 0o644)
}

// mergeTrackMeta keeps any non-empty metadata from the previous entry when a
// replay supplies a sparser track (e.g. an ICY title-only update arriving
// after the original tags were captured). A replay with provider meta comes
// from its provider, so its Realtime and Restricted flags replace the stored
// ones. Stream and Feed follow from the path and are always kept.
func mergeTrackMeta(prev, cur playlist.Track) playlist.Track {
	if cur.Title == "" {
		cur.Title = prev.Title
	}
	if cur.Artist == "" {
		cur.Artist = prev.Artist
	}
	if cur.Album == "" {
		cur.Album = prev.Album
	}
	if cur.Genre == "" {
		cur.Genre = prev.Genre
	}
	if cur.Year == 0 {
		cur.Year = prev.Year
	}
	if cur.TrackNumber == 0 {
		cur.TrackNumber = prev.TrackNumber
	}
	if cur.DurationSecs == 0 {
		cur.DurationSecs = prev.DurationSecs
	}
	if cur.AlbumArtURL == "" {
		cur.AlbumArtURL = prev.AlbumArtURL
	}
	cur.Stream = cur.Stream || prev.Stream
	cur.Feed = cur.Feed || prev.Feed
	if len(cur.ProviderMeta) == 0 {
		cur.Realtime = cur.Realtime || prev.Realtime
		cur.Restricted = cur.Restricted || prev.Restricted
		cur.ProviderMeta = prev.ProviderMeta
	}
	return cur
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintln(w, "[[entry]]")
	fmt.Fprintf(w, "played_at = %q\n", e.PlayedAt.UTC().Format(time.RFC3339))
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
		if t, err := time.Parse(time.RFC3339, f["played_at"]); err == nil {
			e.PlayedAt = t
		}
		entries = append(entries, e)
	})
	return entries
}
