package local

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/player"
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

// playlistSection is one [[track]] or [[dir]] section in a rewritten document.
type playlistSection struct {
	kind  uint8 // itemTrack or itemDir
	track playlist.Track
	dir   DirSource
}

// rebuildDoc merges the caller's explicit tracks back into an existing parsed
// document, preserving the interleaving of [[track]] and [[dir]] sections.
//
// Directory sections keep their slots. Explicit tracks are matched back onto
// their original slots by path, so removals drop the slot (without shifting
// siblings) and metadata updates (e.g. enrichment) stay in place. When the
// caller reordered the explicit tracks, the caller's order wins for the track
// slots while directories stay anchored. Explicit tracks without an original
// slot — additions and bookmark materializations — are inserted directly
// before the directory section that would otherwise supply them, so a
// materialized track keeps its position among the directory's tracks; tracks
// no directory provides are appended at the end.
func rebuildDoc(existing *playlistDoc, explicit []playlist.Track) (tracks []playlist.Track, dirs []DirSource, order []uint8) {
	origPaths := make([]string, len(existing.tracks))
	for i, t := range existing.tracks {
		origPaths[i] = t.Path
	}
	origSet := make(map[string]struct{}, len(origPaths))
	for _, p := range origPaths {
		origSet[p] = struct{}{}
	}
	var callerSubseq []string
	for _, t := range explicit {
		if _, ok := origSet[t.Path]; ok {
			callerSubseq = append(callerSubseq, t.Path)
		}
	}
	reordered := !isSubsequence(origPaths, callerSubseq)

	byPath := make(map[string]playlist.Track, len(explicit))
	placed := make(map[string]struct{}, len(explicit))
	for _, t := range explicit {
		byPath[t.Path] = t
	}

	ti, di, used := 0, 0, 0
	var sections []playlistSection
	for _, kind := range existing.order {
		if kind == itemDir {
			sections = append(sections, playlistSection{kind: itemDir, dir: existing.dirs[di]})
			di++
			continue
		}
		orig := existing.tracks[ti]
		ti++
		if reordered {
			if used < len(explicit) {
				t := explicit[used]
				sections = append(sections, playlistSection{kind: itemTrack, track: t})
				placed[t.Path] = struct{}{}
				used++
			}
			continue
		}
		if t, ok := byPath[orig.Path]; ok {
			sections = append(sections, playlistSection{kind: itemTrack, track: t})
			placed[orig.Path] = struct{}{}
			delete(byPath, orig.Path)
		}
	}

	var leftovers []playlist.Track
	for _, t := range explicit {
		if _, ok := placed[t.Path]; !ok {
			leftovers = append(leftovers, t)
		}
	}
	if len(leftovers) > 0 {
		// Map each dir to its section position. dirPos tracks where the next
		// leftover for that directory must be inserted: directly before the
		// directory section.
		dirPos := make(map[int]int, len(existing.dirs))
		di := 0
		for si, sec := range sections {
			if sec.kind == itemDir {
				dirPos[di] = si
				di++
			}
		}
		// supplierOf returns the first directory (in document order) that would
		// supply file in a scan. It is a pure path check, so saves never
		// re-walk the filesystem that the load already scanned.
		supplierOf := func(file string) (int, bool) {
			for di, src := range existing.dirs {
				if dirSuppliesFile(src, file) {
					return di, true
				}
			}
			return 0, false
		}
		// Insert before-dir leftovers in reverse so several targeting the same
		// directory keep their caller order.
		for i := len(leftovers) - 1; i >= 0; i-- {
			t := leftovers[i]
			if d, ok := supplierOf(t.Path); ok {
				pos := dirPos[d]
				sections = append(sections, playlistSection{})
				copy(sections[pos+1:], sections[pos:])
				sections[pos] = playlistSection{kind: itemTrack, track: t}
				// The insertion shifted every later section by one; re-align
				// the map so the next leftover lands in the right slot.
				for dd, p := range dirPos {
					if dd != d && p >= pos {
						dirPos[dd] = p + 1
					}
				}
			}
		}
		// Leftovers no directory supplies are appended in caller order.
		for _, t := range leftovers {
			if _, ok := supplierOf(t.Path); !ok {
				sections = append(sections, playlistSection{kind: itemTrack, track: t})
			}
		}
	}

	for _, sec := range sections {
		if sec.kind == itemDir {
			dirs = append(dirs, sec.dir)
			order = append(order, itemDir)
		} else {
			tracks = append(tracks, sec.track)
			order = append(order, itemTrack)
		}
	}
	return tracks, dirs, order
}

// isSubsequence reports whether sub appears in orig in the same relative order.
func isSubsequence(orig, sub []string) bool {
	i := 0
	for _, p := range sub {
		for i < len(orig) && orig[i] != p {
			i++
		}
		if i == len(orig) {
			return false
		}
		i++
	}
	return true
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

// dirSuppliesFile reports whether a scan of dir would include file: the path
// has a supported audio extension, lives under the expanded directory and, for
// non-recursive sources, not below an immediate subdirectory. The check is
// path-only so save-time rewrites do not repeat the filesystem walk done at
// load.
func dirSuppliesFile(dir DirSource, file string) bool {
	if !player.SupportedExts[strings.ToLower(filepath.Ext(file))] {
		return false
	}
	root := ExpandPath(dir.Path)
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if !dir.Recursive && strings.ContainsRune(rel, filepath.Separator) {
		return false
	}
	return true
}
