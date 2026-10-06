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
	"sort"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/fuzzy"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.PlaylistWriter           = (*Provider)(nil)
	_ provider.PlaylistBatchWriter      = (*Provider)(nil)
	_ provider.PlaylistTargetFilter     = (*Provider)(nil)
	_ provider.PlaylistPrepender        = (*Provider)(nil)
	_ provider.PlaylistCreator          = (*Provider)(nil)
	_ provider.PlaylistSaver            = (*Provider)(nil)
	_ provider.PlaylistDeleter          = (*Provider)(nil)
	_ provider.PlaylistRenamer          = (*Provider)(nil)
	_ provider.Searcher                 = (*Provider)(nil)
	_ provider.PlaylistDirSourceManager = (*Provider)(nil)
)

// Provider reads and writes TOML-based playlists stored on disk.
type Provider struct {
	dir       string // e.g. ~/.config/cliamp/playlists/
	history   *history.Store
	favorites *favorites.Store

	mu sync.Mutex // see lock
}

// New creates a Provider using ~/.config/cliamp/playlists/ as the base
// directory. It lists the virtual Favorites and Recently Played playlists
// from favs and hist, which the caller owns. Either store can be nil. It
// returns nil when the config directory is unavailable.
func New(favs *favorites.Store, hist *history.Store) *Provider {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Provider{
		dir:       filepath.Join(dir, "playlists"),
		history:   hist,
		favorites: favs,
	}
}

func (p *Provider) Name() string { return "Local" }

// safePath validates a playlist name and returns the absolute path to its TOML
// file, ensuring the result stays within p.dir. This prevents path traversal
// via names containing ".." or path separators.
func (p *Provider) safePath(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("invalid playlist name %q", name)
	}
	resolved := filepath.Join(p.dir, name+".toml")
	if !strings.HasPrefix(resolved, filepath.Clean(p.dir)+string(filepath.Separator)) {
		return "", fmt.Errorf("playlist path escapes base directory")
	}
	return resolved, nil
}

func validateNewName(name string) error {
	if strings.ContainsAny(name, "/\\:<>\"|?*") || name == ".." || name == "." || strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid playlist name %q", name)
	}
	return nil
}

// lock serializes the load-modify-save cycles of playlist writes. The mutex
// covers goroutines that share this Provider. The file lock covers other
// Provider values and other cliamp processes, such as the `cliamp playlist`
// CLI next to the TUI. A method that holds the lock must not call another
// method that takes it.
func (p *Provider) lock() (func(), error) {
	p.mu.Lock()
	path := p.dir + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("creating config dir: %w", err)
	}
	unlock, err := fileutil.LockFile(path)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	return func() {
		_ = unlock()
		p.mu.Unlock()
	}, nil
}

func isHistoryName(name string) bool {
	return name == history.PlaylistName
}

func isFavoritesName(name string) bool {
	return name == favorites.PlaylistName
}

// Playlists scans the directory for .toml files and returns their metadata,
// prepending the virtual "Recently Played" entry when the user has any
// recorded plays. Returns an empty list (not error) when neither exists.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	var lists []playlist.PlaylistInfo
	if info, ok := p.favoritesInfo(); ok {
		lists = append(lists, info)
	}
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
		fileName := e.Name()
		// The virtual Favorites playlist reserves the name. A Favorites.toml
		// that MigrateFavoritesFile could not move stays hidden.
		if fileName == favoritesFileName {
			continue
		}
		name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		doc, err := p.loadDoc(filepath.Join(p.dir, fileName))
		if err != nil {
			continue
		}
		// Count without tag reads so the playlist browser stays fast; durations
		// stay unknown (0) for directory-backed playlists, which the browser
		// omits from display. Directory sources are still walked to count the
		// files they supply.
		tracks := doc.expand(false)
		lists = append(lists, playlist.PlaylistInfo{
			ID:             name,
			Name:           name,
			TrackCount:     len(tracks),
			DurationSecs:   playlist.TotalDurationSecs(tracks),
			DirSourceCount: len(doc.dirs),
		})
	}
	return lists, nil
}

// favoritesFileName is the playlist file that the virtual Favorites
// playlist hides. favoritesLegacyName is the name it moves to.
const (
	favoritesFileName   = favorites.PlaylistName + ".toml"
	favoritesLegacyName = "Favorites (Local)"
)

// MigrateFavoritesFile renames a playlist file named Favorites.toml to
// "Favorites (Local).toml", because the virtual Favorites playlist reserves
// the name. It keeps both files when the new name is taken. Call it once at
// startup, before MigrateBookmarks.
func (p *Provider) MigrateFavoritesFile() error {
	if p == nil {
		return nil
	}
	src := filepath.Join(p.dir, favoritesFileName)
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("check %s: %w", favoritesFileName, err)
	}
	dst := filepath.Join(p.dir, favoritesLegacyName+".toml")
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s.toml: %w", favoritesLegacyName, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move %s: %w", favoritesFileName, err)
	}
	return nil
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

// favoritesInfo returns the synthetic PlaylistInfo entry for "Favorites".
// The entry always appears when the favorites store is available, even when
// empty, so users can discover the feature and see an empty placeholder.
func (p *Provider) favoritesInfo() (playlist.PlaylistInfo, bool) {
	if p.favorites == nil {
		return playlist.PlaylistInfo{}, false
	}
	tracks, err := p.favorites.Tracks()
	if err != nil {
		return playlist.PlaylistInfo{}, false
	}
	return playlist.PlaylistInfo{
		ID:           favorites.PlaylistName,
		Name:         favorites.PlaylistName,
		Section:      "Favorites",
		TrackCount:   len(tracks),
		DurationSecs: playlist.TotalDurationSecs(tracks),
	}, true
}

// Tracks returns the full track list for the named playlist: explicit
// [[track]] entries plus tracks scanned from any [[dir]] sources, in document
// order. The reserved "Recently Played" name is served from the history store.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	if isFavoritesName(playlistID) {
		if p.favorites == nil {
			return nil, nil
		}
		return p.favorites.Tracks()
	}
	if isHistoryName(playlistID) {
		if p.history == nil {
			return nil, nil
		}
		return p.history.Tracks(0)
	}
	doc, err := p.loadDocByName(playlistID)
	if err != nil {
		return nil, err
	}
	return doc.expand(true), nil
}

// AddTrack appends a track to the named playlist, creating the directory and
// file if needed.
func (p *Provider) AddTrack(playlistName string, track playlist.Track) error {
	_, _, err := p.AddTracks(playlistName, []playlist.Track{track})
	return err
}

// AddTracks appends multiple tracks, skipping exact path duplicates already
// provided by the playlist (explicit [[track]] entries or [[dir]] sources) or
// repeated in the input. It creates the playlist file if needed.
func (p *Provider) AddTracks(playlistName string, tracks []playlist.Track) (added, skipped int, err error) {
	if err := writable(playlistName); err != nil {
		return 0, 0, err
	}
	unlock, err := p.lock()
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return 0, 0, err
	}
	path, err := p.safePath(playlistName)
	if err != nil {
		return 0, 0, err
	}

	doc, err := p.loadDoc(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, err
		}
		if err := validateNewName(playlistName); err != nil {
			return 0, 0, err
		}
		doc = &playlistDoc{}
	}

	seen := make(map[string]struct{}, len(doc.tracks)+len(tracks))
	for _, t := range doc.tracks {
		seen[t.Path] = struct{}{}
	}

	existing := doc.tracks
	for _, t := range tracks {
		if _, ok := seen[t.Path]; ok || doc.suppliesFile(t.Path) {
			skipped++
			continue
		}
		// Incoming tracks may carry the DirSourced flag from a directory-backed
		// playlist. Added to a different playlist here, they must persist as
		// explicit [[track]] entries; the save would otherwise drop them since
		// this document has no owning [[dir]] section for them.
		t.DirSourced = false
		seen[t.Path] = struct{}{}
		existing = append(existing, t)
		added++
	}

	if added == 0 {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return 0, skipped, p.savePlaylist(playlistName, existing)
		} else if err != nil {
			return 0, skipped, err
		}
		return 0, skipped, nil
	}
	return added, skipped, p.savePlaylist(playlistName, existing)
}

// PrependTracks inserts tracks at the front of a playlist, in the order given.
//
// A track the playlist already lists explicitly moves to the front instead of
// being duplicated, because prepending is an ordering request: leaving the old
// copy in place would ignore it. A track the playlist only holds through a
// [[dir]] source cannot move, since the entry is generated at load time, so it
// is skipped the way AddTracks skips it.
func (p *Provider) PrependTracks(playlistName string, tracks []playlist.Track) (added, moved, skipped int, err error) {
	if err := writable(playlistName); err != nil {
		return 0, 0, 0, err
	}
	if len(tracks) == 0 {
		return 0, 0, 0, nil
	}
	unlock, err := p.lock()
	if err != nil {
		return 0, 0, 0, err
	}
	defer unlock()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return 0, 0, 0, fmt.Errorf("creating playlist dir: %w", err)
	}
	path, err := p.safePath(playlistName)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("resolving playlist path: %w", err)
	}

	doc, err := p.loadDoc(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, 0, fmt.Errorf("loading playlist %q: %w", playlistName, err)
		}
		if err := validateNewName(playlistName); err != nil {
			return 0, 0, 0, err
		}
		doc = &playlistDoc{}
	}

	// A track already in the playlist keeps its stored value when it moves:
	// the incoming copy may carry less, and a move must not shed a bookmark
	// or the podcast identity the file already holds.
	explicit := make(map[string]playlist.Track, len(doc.tracks))
	for _, t := range doc.tracks {
		explicit[t.Path] = t
	}

	front := make([]playlist.Track, 0, len(tracks))
	relocated := make(map[string]struct{}, len(tracks))
	for _, t := range tracks {
		if _, ok := relocated[t.Path]; ok {
			// The same track twice in one batch keeps its first position.
			skipped++
			continue
		}
		if stored, ok := explicit[t.Path]; ok {
			moved++
			t = stored
		} else if doc.suppliesFile(t.Path) {
			skipped++
			continue
		} else {
			added++
			// An incoming track may carry DirSourced from the playlist it
			// came from. It must persist as an explicit entry here, since
			// this document has no owning [[dir]] section for it.
			t.DirSourced = false
		}
		relocated[t.Path] = struct{}{}
		front = append(front, t)
	}

	if added == 0 && moved == 0 {
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			if err := p.saveDoc(playlistName, doc); err != nil {
				return 0, 0, skipped, fmt.Errorf("saving playlist %q: %w", playlistName, err)
			}
			return 0, 0, skipped, nil
		} else if statErr != nil {
			return 0, 0, skipped, fmt.Errorf("stat playlist %q: %w", playlistName, statErr)
		}
		return 0, 0, skipped, nil
	}

	// saveDoc writes the given order verbatim. savePlaylist cannot be used
	// here: it treats a pure addition as "keep the old positions and append",
	// which is the opposite of what prepending asks for.
	ordered := append([]playlist.Track(nil), front...)
	order := make([]uint8, 0, len(doc.order)+len(front))
	for range front {
		order = append(order, itemTrack)
	}
	ti := 0
	for _, kind := range doc.order {
		if kind == itemDir {
			order = append(order, itemDir)
			continue
		}
		t := doc.tracks[ti]
		ti++
		if _, ok := relocated[t.Path]; ok {
			// Already placed at the front; drop the old position.
			continue
		}
		ordered = append(ordered, t)
		order = append(order, itemTrack)
	}
	if err := p.saveDoc(playlistName, &playlistDoc{tracks: ordered, dirs: doc.dirs, order: order}); err != nil {
		return added, moved, skipped, fmt.Errorf("saving playlist %q: %w", playlistName, err)
	}
	return added, moved, skipped, nil
}

// PrependTracksToPlaylist implements provider.PlaylistPrepender.
func (p *Provider) PrependTracksToPlaylist(_ context.Context, playlistID string, tracks []playlist.Track) (int, int, int, error) {
	return p.PrependTracks(playlistID, tracks)
}

// CreatePlaylist creates an empty playlist file.
func (p *Provider) CreatePlaylist(_ context.Context, name string) (string, error) {
	if err := writable(name); err != nil {
		return "", err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", err
	}
	if err := validateNewName(name); err != nil {
		return "", err
	}
	path, err := p.safePath(name)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("playlist %q already exists", name)
		}
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return name, nil
}

// CreateDirPlaylist creates a new playlist that references the given
// directories via [[dir]] sections instead of expanding their files. Fails
// when the playlist already exists or a directory does not exist, leaving no
// file behind.
func (p *Provider) CreateDirPlaylist(name string, dirs []string) error {
	if err := writable(name); err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("creating playlist dir: %w", err)
	}
	if err := validateNewName(name); err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := validateDirSource(dir); err != nil {
			return err
		}
	}
	path, err := p.safePath(name)
	if err != nil {
		return fmt.Errorf("resolving playlist path: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("playlist %q already exists", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat playlist %q: %w", name, err)
	}

	var b strings.Builder
	for i, dir := range dirs {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeDir(&b, playlist.DirSource{Path: dir, Recursive: true})
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing playlist %q: %w", name, err)
	}
	// Link is atomic and fails when the final path already exists, preserving
	// the create-only semantics without a partial-write window.
	if err := os.Link(tmp, path); err != nil {
		os.Remove(tmp)
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("playlist %q already exists", name)
		}
		return fmt.Errorf("creating playlist %q: %w", name, err)
	}
	return os.Remove(tmp)
}

// AddDirSources appends [[dir]] sections for every directory in dirs that the
// named playlist does not already reference, creating the playlist if needed.
// All directories are validated before anything is persisted, so a failing
// input leaves the playlist untouched. Returns the directories that were added.
func (p *Provider) AddDirSources(name string, dirs []string) ([]string, error) {
	if err := writable(name); err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if err := validateDirSource(dir); err != nil {
			return nil, err
		}
	}
	unlock, err := p.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating playlist dir: %w", err)
	}
	path, err := p.safePath(name)
	if err != nil {
		return nil, fmt.Errorf("resolving playlist path: %w", err)
	}
	doc, err := p.loadDoc(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := validateNewName(name); err != nil {
			return nil, err
		}
		doc = &playlistDoc{}
	}

	known := make(map[string]struct{}, len(doc.dirs))
	for _, src := range doc.dirs {
		known[ExpandPath(src.Path)] = struct{}{}
	}
	var added []string
	for _, dir := range dirs {
		target := ExpandPath(dir)
		if _, ok := known[target]; ok {
			continue
		}
		known[target] = struct{}{}
		added = append(added, dir)
		doc.dirs = append(doc.dirs, playlist.DirSource{Path: dir, Recursive: true})
		doc.order = append(doc.order, itemDir)
	}
	if len(added) == 0 {
		return nil, nil
	}
	return added, p.saveDoc(name, doc)
}

// AddDirSource appends a [[dir]] section referencing dir to the named
// playlist, creating the playlist if needed. Returns false when the playlist
// already references the same directory. The directory must exist.
func (p *Provider) AddDirSource(name, dir string) (bool, error) {
	added, err := p.AddDirSources(name, []string{dir})
	if err != nil {
		return false, err
	}
	return len(added) == 1, nil
}

// DirSources returns the directory sources referenced by a playlist.
func (p *Provider) DirSources(name string) ([]playlist.DirSource, error) {
	if err := writable(name); err != nil {
		return nil, err
	}
	doc, err := p.loadDocByName(name)
	if err != nil {
		return nil, err
	}
	return doc.dirs, nil
}

// dirIndexByPath returns the index in doc.dirs of the source whose expanded
// path matches dir (also expanded), or -1 when none matches. Comparison uses
// cleaned filesystem paths so "~/Music" and "/home/user/Music" align. It is a
// pure path check, so it never re-walks the filesystem the load already did.
func dirIndexByPath(doc *playlistDoc, dir string) int {
	target := filepath.Clean(ExpandPath(dir))
	for i, src := range doc.dirs {
		if filepath.Clean(ExpandPath(src.Path)) == target {
			return i
		}
	}
	return -1
}

// RemoveDirSource removes the [[dir]] section whose path matches dir from the
// named playlist. Explicit [[track]] sections keep their slots and order. A
// missing source (or a missing playlist) is a no-op rather than an error, so
// callers can remove without first checking existence.
func (p *Provider) RemoveDirSource(name, dir string) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	path, err := p.safePath(name)
	if err != nil {
		return fmt.Errorf("resolving playlist path: %w", err)
	}
	doc, err := p.loadDoc(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	di := dirIndexByPath(doc, dir)
	if di < 0 {
		return nil
	}
	// Drop the dir and its corresponding itemDir slot from doc.order so the
	// ti/di counters used by saveDoc stay aligned with the remaining sections.
	doc.dirs = append(doc.dirs[:di], doc.dirs[di+1:]...)
	seen := 0
	for i, kind := range doc.order {
		if kind != itemDir {
			continue
		}
		if seen == di {
			doc.order = append(doc.order[:i], doc.order[i+1:]...)
			break
		}
		seen++
	}
	return p.saveDoc(name, doc)
}

// SetDirRecursive sets the recursive flag on the [[dir]] section whose path
// matches dir in the named playlist. It is a no-op (not an error) when the
// source is missing, the playlist is missing, or the flag is already the
// requested value, so callers can toggle without first checking state.
func (p *Provider) SetDirRecursive(name, dir string, recursive bool) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	path, err := p.safePath(name)
	if err != nil {
		return fmt.Errorf("resolving playlist path: %w", err)
	}
	doc, err := p.loadDoc(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	di := dirIndexByPath(doc, dir)
	if di < 0 {
		return nil
	}
	if doc.dirs[di].Recursive == recursive {
		return nil
	}
	doc.dirs[di].Recursive = recursive
	return p.saveDoc(name, doc)
}

// saveDoc writes a parsed document back to disk, preserving section order.
// The full document is rendered in memory before the atomic rename so a
// partial write can never clobber the existing playlist.
func (p *Provider) saveDoc(name string, doc *playlistDoc) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("creating playlist dir: %w", err)
	}
	path, err := p.safePath(name)
	if err != nil {
		return fmt.Errorf("resolving playlist path: %w", err)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := validateNewName(name); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat playlist %q: %w", name, err)
	}

	var b strings.Builder
	ti, di, sections := 0, 0, 0
	for _, kind := range doc.order {
		if sections > 0 {
			b.WriteByte('\n')
		}
		if kind == itemTrack {
			writeTrack(&b, doc.tracks[ti])
			ti++
		} else {
			writeDir(&b, doc.dirs[di])
			di++
		}
		sections++
	}

	if err := fileutil.WriteFileAtomicInExistingDir(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}
	return nil
}

// Exists reports whether a playlist with the given name exists on disk, or
// whether it refers to the virtual "Recently Played" history with at least
// one entry recorded.
func (p *Provider) Exists(name string) bool {
	if isFavoritesName(name) {
		_, ok := p.favoritesInfo()
		return ok
	}
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

// savePlaylist overwrites the named playlist with the given tracks, preserving
// [[dir]] sections and their interleaving with explicit [[track]] sections.
// Tracks marked DirSourced are not persisted; they are re-derived from the
// directory sources on the next load.
func (p *Provider) savePlaylist(name string, tracks []playlist.Track) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("creating playlist dir: %w", err)
	}

	path, err := p.safePath(name)
	if err != nil {
		return fmt.Errorf("resolving playlist path: %w", err)
	}
	var existing *playlistDoc
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := validateNewName(name); err != nil {
			return err
		}
		existing = &playlistDoc{}
	} else if err != nil {
		return fmt.Errorf("stat playlist %q: %w", name, err)
	} else {
		existing, err = p.existingDoc(path)
		if err != nil {
			return err
		}
	}

	var explicit []playlist.Track
	for _, t := range tracks {
		if t.DirSourced {
			continue
		}
		explicit = append(explicit, t)
	}
	tracks, dirs, order := rebuildDoc(existing, explicit)
	return p.saveDoc(name, &playlistDoc{tracks: tracks, dirs: dirs, order: order})
}

// existingDoc parses path into a document, returning an empty document only
// when the file does not exist. Read failures are wrapped and propagated so a
// broken playlist is never silently rewritten without its [[dir]] sections.
func (p *Provider) existingDoc(path string) (*playlistDoc, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &playlistDoc{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read playlist %q: %w", path, err)
	}
	return parsePlaylistDoc(data), nil
}

// errReservedHistoryName is returned when a caller tries to write to or
// otherwise mutate the synthetic history playlist.
var errReservedHistoryName = errors.New(`"Recently Played" is a virtual history playlist and cannot be modified`)

// errReservedFavoritesName is returned when a caller tries to write to or
// otherwise mutate the synthetic favorites playlist.
var errReservedFavoritesName = errors.New(`"Favorites" is a virtual favorites playlist and cannot be modified`)

// writable returns the error for a name that belongs to a virtual playlist,
// or nil when a write to the name can go to a playlist file. Every method
// that writes a playlist file checks it first, because a file under a
// virtual name is hidden behind the virtual playlist.
func writable(name string) error {
	switch {
	case isHistoryName(name):
		return errReservedHistoryName
	case isFavoritesName(name):
		return errReservedFavoritesName
	}
	return nil
}

// loadDocByName loads a named playlist's parsed document.
func (p *Provider) loadDocByName(name string) (*playlistDoc, error) {
	path, err := p.safePath(name)
	if err != nil {
		return nil, err
	}
	return p.loadDoc(path)
}

// SavePlaylist overwrites a playlist with the given tracks.
func (p *Provider) SavePlaylist(name string, tracks []playlist.Track) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return p.savePlaylist(name, tracks)
}

// UpdatePlaylist runs fn on the tracks of the named playlist and saves the
// tracks that fn returns. The read, fn and the save run under the playlist
// lock, so no other writer can change the file in between. fn gets the
// explicit [[track]] entries in document order and may change the slice.
// It gets no tracks from directory sources, because the save drops them and
// a directory scan must not hold the lock. The save keeps the [[dir]]
// sections. When fn returns playlist.ErrPlaylistUnchanged, nothing is saved
// and the result is nil.
func (p *Provider) UpdatePlaylist(name string, fn func([]playlist.Track) ([]playlist.Track, error)) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := p.loadDocByName(name)
	if err != nil {
		return err
	}
	tracks, err := fn(doc.tracks)
	if errors.Is(err, playlist.ErrPlaylistUnchanged) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.savePlaylist(name, tracks)
}

// AddTrackToPlaylist appends a track to the named playlist.
// Implements provider.PlaylistWriter.
func (p *Provider) AddTrackToPlaylist(_ context.Context, playlistID string, track playlist.Track) error {
	return p.AddTrack(playlistID, track)
}

// AddTracksToPlaylist appends multiple tracks to the named playlist.
// Implements provider.PlaylistBatchWriter.
func (p *Provider) AddTracksToPlaylist(_ context.Context, playlistID string, tracks []playlist.Track) (int, int, error) {
	return p.AddTracks(playlistID, tracks)
}

// CanAddToPlaylist reports whether tracks can be added to pl. Recently
// Played and Favorites are virtual and reject every add.
// Implements provider.PlaylistTargetFilter.
func (p *Provider) CanAddToPlaylist(pl playlist.PlaylistInfo) bool {
	return writable(pl.ID) == nil
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
		// Playlists hides this file, so a search must not show its tracks.
		if e.Name() == favoritesFileName {
			continue
		}
		doc, err := p.loadDoc(filepath.Join(p.dir, e.Name()))
		if err != nil {
			continue
		}
		for _, t := range doc.expand(true) {
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
	if err := writable(oldName); err != nil {
		return err
	}
	if err := writable(newName); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	oldPath, err := p.safePath(oldName)
	if err != nil {
		return fmt.Errorf("invalid playlist name %q: %w", oldName, err)
	}
	if err := validateNewName(newName); err != nil {
		return err
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
// The virtual playlists cannot be deleted. Clear Recently Played with
// history.Store.Clear. A track leaves Favorites only through
// favorites.Store.Toggle.
func (p *Provider) DeletePlaylist(name string) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	path, err := p.safePath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// PlaylistDocument returns the playlist's raw TOML bytes, including sections
// the expanded track list cannot represent (e.g. [[dir]] sources).
func (p *Provider) PlaylistDocument(name string) ([]byte, error) {
	path, err := p.safePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read playlist %q: %w", name, err)
	}
	return data, nil
}

// RestorePlaylistDocument overwrites the playlist with raw TOML bytes so an
// undo can put back exactly what a delete removed, [[dir]] sections included.
func (p *Provider) RestorePlaylistDocument(name string, data []byte) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("creating playlist dir: %w", err)
	}
	path, err := p.safePath(name)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomicInExistingDir(path, data, 0o644); err != nil {
		return fmt.Errorf("replacing playlist %q: %w", name, err)
	}
	return nil
}

// RemoveTrack removes a track by index from the named playlist.
// The index refers to the expanded track list. Directory-scanned tracks
// cannot be removed: they are re-derived from the [[dir]] source on every
// load. Empty playlists are kept on disk; deleting a playlist remains explicit.
func (p *Provider) RemoveTrack(name string, index int) error {
	if err := writable(name); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := p.loadDocByName(name)
	if err != nil {
		return err
	}
	// The index needs only the paths and the DirSourced flags. A tag read
	// of every directory file must not hold the lock.
	tracks := doc.expand(false)
	if index < 0 || index >= len(tracks) {
		return fmt.Errorf("track index %d out of range", index)
	}
	if tracks[index].DirSourced {
		return fmt.Errorf("track %d (%s) is supplied by a directory source; remove the file from the directory or edit the playlist's [[dir]] section", index+1, tracks[index].Path)
	}

	kept := tracks[:0]
	removed := false
	for i, t := range tracks {
		if t.DirSourced {
			continue
		}
		if i == index {
			removed = true
			continue
		}
		kept = append(kept, t)
	}
	if !removed {
		return fmt.Errorf("track index %d out of range", index)
	}
	return p.savePlaylist(name, kept)
}

// writeTrack writes a single [[track]] TOML section to w: the track fields
// that favorites and history also keep, then the keys only playlists keep.
func writeTrack(w io.Writer, t playlist.Track) {
	fmt.Fprintln(w, "[[track]]")
	playlist.WriteTrackTOML(w, t)
	if t.EmbeddedLyrics != "" {
		fmt.Fprintf(w, "embedded_lyrics = %q\n", t.EmbeddedLyrics)
	}
	if t.Bookmark {
		fmt.Fprintln(w, "bookmark = true")
	}
}

// parseTrackFields converts a parsed [[track]] section into a Track.
func parseTrackFields(f map[string]string) playlist.Track {
	t := playlist.TrackFromTOML(f)
	t.EmbeddedLyrics = f["embedded_lyrics"]
	// "favorite" is the pre-rename alias for "bookmark"; prefer bookmark.
	bookmark, ok := f["bookmark"]
	if !ok {
		bookmark = f["favorite"]
	}
	t.Bookmark = bookmark == "true"
	// Older versions kept only the podcast feed and GUID, as podcast_feed and
	// podcast_guid. A GUID without its feed was never read back.
	if feed := f["podcast_feed"]; feed != "" && t.Meta(provider.MetaPodcastFeed) == "" {
		if t.ProviderMeta == nil {
			t.ProviderMeta = make(map[string]string, 2)
		}
		t.ProviderMeta[provider.MetaPodcastFeed] = feed
		if guid := f["podcast_guid"]; guid != "" && t.Meta(provider.MetaPodcastGUID) == "" {
			t.ProviderMeta[provider.MetaPodcastGUID] = guid
		}
	}
	return t
}

// loadDoc reads and parses a playlist file into explicit tracks and
// directory sources, without scanning any directories.
func (p *Provider) loadDoc(path string) (*playlistDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePlaylistDoc(data), nil
}
