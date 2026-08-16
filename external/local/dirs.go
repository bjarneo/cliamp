package local

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// DirSource is a [[dir]] section in a playlist file: a directory that is
// scanned for audio files every time the playlist loads, instead of listing
// every file explicitly.
type DirSource struct {
	Path      string // directory path; supports ~ and environment variables
	Recursive bool   // scan subdirectories too (default true)
}

// ExpandPath expands a leading ~ and environment variables in p.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	expanded := os.ExpandEnv(p)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(expanded, "~"))
		}
	}
	return expanded
}

// Section kinds tracked in playlistDoc.order.
const (
	itemTrack uint8 = iota
	itemDir
)

// playlistDoc is a parsed playlist file: explicit [[track]] entries and
// [[dir]] sources, with section order preserved for ordered expansion.
type playlistDoc struct {
	tracks []playlist.Track
	dirs   []DirSource
	order  []uint8 // itemTrack or itemDir per section, in document order
}

// parsePlaylistDoc parses a playlist TOML document. Directory sources are
// parsed but not scanned; call expand to resolve them into tracks.
func parsePlaylistDoc(data []byte) *playlistDoc {
	doc := &playlistDoc{}
	tomlutil.ParseNamedSections(data, []string{"track", "dir"}, func(section string, f map[string]string) {
		switch section {
		case "track":
			doc.tracks = append(doc.tracks, parseTrackFields(f))
			doc.order = append(doc.order, itemTrack)
		case "dir":
			if f["path"] == "" {
				return
			}
			doc.dirs = append(doc.dirs, DirSource{
				Path:      f["path"],
				Recursive: f["recursive"] != "false",
			})
			doc.order = append(doc.order, itemDir)
		}
	})
	return doc
}

// expand returns the full track list: explicit [[track]] entries plus tracks
// scanned from [[dir]] sources, in document order. A file supplied by a
// directory scan is skipped when an explicit [[track]] with the same path
// exists anywhere in the document, so explicit entries (with their custom
// metadata and bookmarks) always win. Directory-scanned tracks are marked
// DirSourced. Unreadable or missing directories contribute no tracks.
//
// When withTags is false, directory tracks are returned without reading
// their tags (titles fall back to filename parsing), for cheap operations
// such as counting.
func (d *playlistDoc) expand(withTags bool) []playlist.Track {
	explicit := make(map[string]struct{}, len(d.tracks))
	for _, t := range d.tracks {
		explicit[t.Path] = struct{}{}
	}
	ti, di := 0, 0
	var out []playlist.Track
	for _, kind := range d.order {
		if kind == itemTrack {
			out = append(out, d.tracks[ti])
			ti++
			continue
		}
		src := d.dirs[di]
		di++
		files, err := resolve.AudioFiles(ExpandPath(src.Path), src.Recursive)
		if err != nil {
			continue
		}
		var dirTracks []playlist.Track
		if withTags {
			dirTracks = resolve.TracksFromPaths(files)
		} else {
			dirTracks = make([]playlist.Track, len(files))
			for i, f := range files {
				dirTracks[i] = playlist.TrackFromFilename(f)
			}
		}
		for _, t := range dirTracks {
			if _, dup := explicit[t.Path]; dup {
				continue
			}
			explicit[t.Path] = struct{}{}
			t.DirSourced = true
			out = append(out, t)
		}
	}
	return out
}

// writeDir writes a single [[dir]] TOML section to w.
func writeDir(w io.Writer, src DirSource) {
	fmt.Fprintln(w, "[[dir]]")
	fmt.Fprintf(w, "path = %q\n", src.Path)
	if !src.Recursive {
		fmt.Fprintln(w, "recursive = false")
	}
}

// validateDirSource expands dir and verifies it exists and is a directory.
func validateDirSource(dir string) error {
	info, err := os.Stat(ExpandPath(dir))
	if err != nil {
		return fmt.Errorf("directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	return nil
}
