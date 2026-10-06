package local

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
)

// bookmarksMigratedFile records that MigrateBookmarks ran. It is kept next to
// favorites.toml.
const bookmarksMigratedFile = "bookmarks_migrated"

// MigrateBookmarks copies each track with the legacy bookmark flag into the
// favorites store. It runs one time: after a successful run it writes a
// marker file and later calls do nothing. It writes the favorites before the
// marker, so a failed run tries again on the next call. It does not change
// the playlist files. Returns the number of favorites it added.
func (p *Provider) MigrateBookmarks() (int, error) {
	if p == nil || p.favorites == nil {
		return 0, nil
	}
	marker := filepath.Join(filepath.Dir(p.favorites.Path()), bookmarksMigratedFile)
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("check bookmark migration: %w", err)
	}

	tracks, err := p.bookmarkedTracks()
	if err != nil {
		return 0, fmt.Errorf("read bookmarks: %w", err)
	}
	added, err := p.favorites.Import(tracks)
	if err != nil {
		return 0, fmt.Errorf("import bookmarks: %w", err)
	}
	note := fmt.Sprintf("cliamp copied %d bookmarks into favorites.toml. Delete this file to copy them again.\n", added)
	if err := fileutil.WriteFileAtomic(marker, []byte(note), 0o644); err != nil {
		return added, fmt.Errorf("record bookmark migration: %w", err)
	}
	return added, nil
}

// bookmarkedTracks returns the explicit tracks with the bookmark flag from
// every playlist file, in file-name order. A bookmark on a directory track is
// always saved as an explicit entry, so directory sources are not scanned.
// Unreadable files are skipped, as Playlists does.
func (p *Provider) bookmarkedTracks() ([]playlist.Track, error) {
	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []playlist.Track
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		doc, err := p.loadDoc(filepath.Join(p.dir, e.Name()))
		if err != nil {
			continue
		}
		for _, track := range doc.tracks {
			if track.Bookmark {
				track.Bookmark = false
				out = append(out, track)
			}
		}
	}
	return out, nil
}
