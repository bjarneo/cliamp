package local

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

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

// scanDir lists the audio files of a [[dir]] source and readTags reads their
// tags. Tests replace them to check that a write does not do this work while
// it holds the playlist lock.
var (
	scanDir  = resolve.AudioFiles
	readTags = resolve.TracksFromPaths
)

// Section kinds tracked in playlistDoc.order.
const (
	itemTrack uint8 = iota
	itemDir
)

// playlistDoc is a parsed playlist file: explicit [[track]] entries and
// [[dir]] sources, with section order preserved for ordered expansion.
type playlistDoc struct {
	tracks []playlist.Track
	dirs   []playlist.DirSource
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
			doc.dirs = append(doc.dirs, playlist.DirSource{
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
		files, err := scanDir(ExpandPath(src.Path), src.Recursive)
		if err != nil {
			continue
		}
		var dirTracks []playlist.Track
		if withTags {
			dirTracks = readTags(files)
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
func writeDir(w io.Writer, src playlist.DirSource) {
	fmt.Fprintln(w, "[[dir]]")
	fmt.Fprintf(w, "path = %q\n", src.Path)
	if !src.Recursive {
		fmt.Fprintln(w, "recursive = false")
	}
}

// playlistSection is one [[track]] or [[dir]] section in a rewritten document.
type playlistSection struct {
	kind   uint8 // itemTrack or itemDir
	track  playlist.Track
	dir    playlist.DirSource
	caller int // index of track in the caller's list, for a track section
}

// rebuildDoc merges the caller's explicit tracks back into an existing parsed
// document, preserving the interleaving of [[track]] and [[dir]] sections.
//
// Directory sections keep their slots. Explicit tracks are matched back onto
// their original slots by path and occurrence, so removals drop the slot
// (without shifting siblings), metadata updates (e.g. enrichment) stay in
// place, and a path that the document lists more than once keeps each copy.
// When the caller reordered the explicit tracks, the caller's order wins for
// the track slots while directories stay anchored. Explicit tracks without an
// original slot — additions and bookmark materializations — are inserted
// directly before the directory section that would otherwise supply them, so
// a materialized track keeps its position among the directory's tracks;
// tracks no directory provides go before the next caller track that kept its
// slot, or at the end when none follows.
func rebuildDoc(existing *playlistDoc, explicit []playlist.Track) (tracks []playlist.Track, dirs []playlist.DirSource, order []uint8) {
	// A caller track can take an original slot while the document has a copy
	// of its path left. A copy beyond that count is an addition.
	remaining := make(map[string]int, len(existing.tracks))
	for _, t := range existing.tracks {
		remaining[t.Path]++
	}
	var matchable []int
	for i, t := range explicit {
		if remaining[t.Path] > 0 {
			remaining[t.Path]--
			matchable = append(matchable, i)
		}
	}
	// slotOf maps each original track slot to the caller track that keeps
	// it, or -1 when the caller removed it. The caller reordered the tracks
	// when the matchable tracks are not a subsequence of the original slots.
	slotOf := make([]int, len(existing.tracks))
	matched := 0
	for ti, orig := range existing.tracks {
		slotOf[ti] = -1
		if matched < len(matchable) && explicit[matchable[matched]].Path == orig.Path {
			slotOf[ti] = matchable[matched]
			matched++
		}
	}
	reordered := matched < len(matchable)

	placed := make([]bool, len(explicit))
	ti, di, used := 0, 0, 0
	var sections []playlistSection
	for _, kind := range existing.order {
		if kind == itemDir {
			sections = append(sections, playlistSection{kind: itemDir, dir: existing.dirs[di]})
			di++
			continue
		}
		keep := slotOf[ti]
		ti++
		if reordered {
			if used < len(explicit) {
				sections = append(sections, playlistSection{kind: itemTrack, track: explicit[used], caller: used})
				placed[used] = true
				used++
			}
			continue
		}
		if keep >= 0 {
			sections = append(sections, playlistSection{kind: itemTrack, track: explicit[keep], caller: keep})
			placed[keep] = true
		}
	}

	var leftovers []int
	for i := range explicit {
		if !placed[i] {
			leftovers = append(leftovers, i)
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
			ci := leftovers[i]
			if d, ok := supplierOf(explicit[ci].Path); ok {
				pos := dirPos[d]
				sections = append(sections, playlistSection{})
				copy(sections[pos+1:], sections[pos:])
				sections[pos] = playlistSection{kind: itemTrack, track: explicit[ci], caller: ci}
				// The insertion shifted every later section by one; re-align
				// the map so the next leftover lands in the right slot.
				for dd, p := range dirPos {
					if dd != d && p >= pos {
						dirPos[dd] = p + 1
					}
				}
			}
		}
		// A leftover that no directory supplies goes directly before the
		// next track in caller order that kept its slot, so a track put
		// between saved tracks stays there. Without such a track, it is
		// appended in caller order.
		following := make([]int, len(explicit))
		next := -1
		for i := len(explicit) - 1; i >= 0; i-- {
			following[i] = next
			if placed[i] {
				next = i
			}
		}
		for _, ci := range leftovers {
			if _, ok := supplierOf(explicit[ci].Path); ok {
				continue
			}
			pos := -1
			if next := following[ci]; next >= 0 {
				pos = slices.IndexFunc(sections, func(sec playlistSection) bool {
					return sec.kind == itemTrack && sec.caller == next
				})
			}
			if pos < 0 {
				pos = len(sections)
			}
			sections = slices.Insert(sections, pos, playlistSection{kind: itemTrack, track: explicit[ci], caller: ci})
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

// suppliesFile reports whether a [[dir]] source of d supplies file. It checks
// the path against each source and stats the file, so a write that holds the
// playlist lock does not walk the directories.
func (d *playlistDoc) suppliesFile(file string) bool {
	if !slices.ContainsFunc(d.dirs, func(src playlist.DirSource) bool { return dirSuppliesFile(src, file) }) {
		return false
	}
	info, err := os.Stat(file)
	return err == nil && !info.IsDir()
}

// dirSuppliesFile reports whether a scan of dir would include file: the path
// has a supported audio extension, lives under the expanded directory and, for
// non-recursive sources, not below an immediate subdirectory. The check is
// path-only so save-time rewrites do not repeat the filesystem walk done at
// load.
func dirSuppliesFile(dir playlist.DirSource, file string) bool {
	if !playlist.IsAudioFile(file) {
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
