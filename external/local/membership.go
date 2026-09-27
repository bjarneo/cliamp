package local

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

// PlaylistMembership reports which playlists contain the track at path. The
// value is true when only a [[dir]] source supplies the track, so it cannot be
// removed on its own. Favorites is included; "Recently Played" never is.
// Directory sources are matched by path alone, so no directory is walked.
func (p *Provider) PlaylistMembership(path string) (map[string]bool, error) {
	member := make(map[string]bool)
	if path == "" {
		return member, nil
	}
	if p.IsFavorited(path) {
		member[favorites.PlaylistName] = false
	}
	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return member, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if isFavoritesName(name) {
			// Playlists() migrates this file; until then it is not a list.
			continue
		}
		doc, err := p.loadDoc(filepath.Join(p.dir, e.Name()))
		if err != nil {
			continue
		}
		if slices.ContainsFunc(doc.tracks, func(t playlist.Track) bool { return t.Path == path }) {
			member[name] = false
		} else if slices.ContainsFunc(doc.dirs, func(d playlist.DirSource) bool { return dirSuppliesFile(d, path) }) {
			member[name] = true
		}
	}
	return member, nil
}

// RemoveTrackByPath removes every explicit [[track]] entry with path from the
// named playlist, keeping [[dir]] sources and section order intact. A track
// that only a directory source supplies cannot be removed this way.
func (p *Provider) RemoveTrackByPath(name, path string) error {
	if isHistoryName(name) {
		return errReservedHistoryName
	}
	if isFavoritesName(name) {
		return errReservedFavoritesName
	}
	doc, err := p.loadDocByName(name)
	if err != nil {
		return err
	}
	out := &playlistDoc{}
	ti, di := 0, 0
	for _, kind := range doc.order {
		if kind == itemDir {
			out.dirs = append(out.dirs, doc.dirs[di])
			out.order = append(out.order, itemDir)
			di++
			continue
		}
		if t := doc.tracks[ti]; t.Path != path {
			out.tracks = append(out.tracks, t)
			out.order = append(out.order, itemTrack)
		}
		ti++
	}
	if len(out.tracks) == len(doc.tracks) {
		return fmt.Errorf("track %q is not an explicit entry in playlist %q", path, name)
	}
	return p.saveDoc(name, out)
}
