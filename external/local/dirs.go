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
	return d.expandWithTagReader(withTags, readTags)
}

func (d *playlistDoc) expandWithTagReader(withTags bool, tagReader func([]string) []playlist.Track) []playlist.Track {
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
		var unseen []string
		for _, f := range files {
			if _, dup := explicit[f]; dup {
				continue
			}
			explicit[f] = struct{}{}
			unseen = append(unseen, f)
		}
		if len(unseen) == 0 {
			continue
		}
		var dirTracks []playlist.Track
		if withTags {
			dirTracks = tagReader(unseen)
		} else {
			dirTracks = make([]playlist.Track, len(unseen))
			for i, f := range unseen {
				dirTracks[i] = playlist.TrackFromFilename(f)
			}
		}
		for _, t := range dirTracks {
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
	if len(leftovers) == 0 {
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

	// Map each dir and each placed caller track to its index in sections.
	dirSecIdx := make([]int, 0, len(existing.dirs))
	callerSecIdx := make(map[int]int, len(sections))
	for si, sec := range sections {
		if sec.kind == itemDir {
			dirSecIdx = append(dirSecIdx, si)
		} else {
			callerSecIdx[sec.caller] = si
		}
	}

	// Pre-expand directory paths once so each leftover check avoids repeated
	// environment and home directory lookups.
	type expandedDir struct {
		root      string
		recursive bool
	}
	expandedDirs := make([]expandedDir, len(existing.dirs))
	for i, src := range existing.dirs {
		expandedDirs[i] = expandedDir{root: ExpandPath(src.Path), recursive: src.Recursive}
	}
	supplierOf := func(file string) (int, bool) {
		if len(expandedDirs) == 0 || !playlist.IsAudioFile(file) {
			return 0, false
		}
		for i, ed := range expandedDirs {
			if expandedDirSuppliesFile(ed.root, ed.recursive, file) {
				return i, true
			}
		}
		return 0, false
	}

	// Compute the next placed caller track for every index in one reverse pass.
	following := make([]int, len(explicit))
	next := -1
	for i := len(explicit) - 1; i >= 0; i-- {
		following[i] = next
		if placed[i] {
			next = i
		}
	}

	// Bucket leftovers by target section index in caller order, then merge in
	// one linear pass.
	buckets := make([][]playlist.Track, len(sections)+1)
	for _, ci := range leftovers {
		pos := len(sections)
		if d, ok := supplierOf(explicit[ci].Path); ok {
			pos = dirSecIdx[d]
		} else if nxt := following[ci]; nxt >= 0 {
			if idx, ok := callerSecIdx[nxt]; ok {
				pos = idx
			}
		}
		buckets[pos] = append(buckets[pos], explicit[ci])
	}

	tracks = make([]playlist.Track, 0, len(explicit))
	dirs = make([]playlist.DirSource, 0, len(existing.dirs))
	order = make([]uint8, 0, len(explicit)+len(existing.dirs))
	for si, sec := range sections {
		for _, t := range buckets[si] {
			tracks = append(tracks, t)
			order = append(order, itemTrack)
		}
		if sec.kind == itemDir {
			dirs = append(dirs, sec.dir)
			order = append(order, itemDir)
		} else {
			tracks = append(tracks, sec.track)
			order = append(order, itemTrack)
		}
	}
	for _, t := range buckets[len(sections)] {
		tracks = append(tracks, t)
		order = append(order, itemTrack)
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
	return expandedDirSuppliesFile(ExpandPath(dir.Path), dir.Recursive, file)
}

func expandedDirSuppliesFile(root string, recursive bool, file string) bool {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if !recursive && strings.ContainsRune(rel, filepath.Separator) {
		return false
	}
	return true
}
