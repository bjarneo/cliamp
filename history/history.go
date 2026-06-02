// Package history persists the user's recently played tracks to a TOML file
// in the cliamp config directory. Entries are recorded when a track has been
// played past the scrobble threshold (the same heuristic Last.fm and the
// Navidrome scrobbler use) so skipped tracks never enter the list.
//
// The store is safe for concurrent callers and writes atomically (temp file +
// rename) so a crash mid-write cannot leave a half-finished history.toml.
package history

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

	"cliamp/internal/appdir"
	"cliamp/internal/tomlutil"
	"cliamp/playlist"
)

// DefaultCap is the maximum number of entries kept on disk. Older entries are
// dropped FIFO once the cap is exceeded.
const DefaultCap = 200

// dedupWindow is how recently the previous entry must have been recorded for
// a same-path play to be treated as a replay (timestamp updated, no new row)
// rather than a fresh listening event. This filters out cases where a user
// scrubs back to the start of a track that's already 50% played.
const dedupWindow = 5 * time.Minute

// PlaylistName is the virtual playlist name surfaced to the UI by the local
// provider. Browsing this name returns history entries newest-first.
const PlaylistName = "Recently Played"

// Entry pairs a track with the wall-clock time it was played past threshold.
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

// SetCap overrides the entry cap. Values <= 0 leave the cap unchanged.
func (s *Store) SetCap(n int) {
	if n > 0 {
		s.mu.Lock()
		s.cap = n
		s.mu.Unlock()
	}
}

// Path returns the on-disk file path.
func (s *Store) Path() string { return s.path }

// Record appends an entry for track played at playedAt. If the most recent
// entry has the same path and was logged within dedupWindow, its timestamp is
// updated in place instead of duplicating the row. Empty paths are ignored.
func (s *Store) Record(track playlist.Track, playedAt time.Time) error {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		// Don't clobber existing on-disk history on a transient read failure:
		// proceeding would rewrite the file with only the new entry.
		return fmt.Errorf("load history: %w", err)
	}
	if n := len(entries); n > 0 {
		top := entries[0]
		if top.Track.Path == track.Path && playedAt.Sub(top.PlayedAt) < dedupWindow {
			entries[0].PlayedAt = playedAt
			entries[0].Track = mergeTrackMeta(top.Track, track)
			return s.saveLocked(entries)
		}
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
	// Build the full content in memory (writes to a Builder can't fail), then
	// write a temp file and rename so a partial/failed write can never truncate
	// the existing history file.
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

// mergeTrackMeta keeps any non-empty metadata from the previous entry when a
// replay supplies a sparser track (e.g. an ICY title-only update arriving
// after the original tags were captured).
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
	return cur
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintf(w, "[[entry]]\n")
	fmt.Fprintf(w, "played_at = %q\n", e.PlayedAt.UTC().Format(time.RFC3339))
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
		case "played_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				cur.PlayedAt = t
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
