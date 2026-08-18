// Package cmd implements CLI subcommands for cliamp.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/internal/sshurl"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// PlaylistList prints all playlists with their track counts.
func PlaylistList() error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	lists, err := prov.Playlists()
	if err != nil {
		return fmt.Errorf("listing playlists: %w", err)
	}
	if len(lists) == 0 {
		fmt.Println("No playlists found.")
		return nil
	}

	maxName := 0
	for _, pl := range lists {
		if len(pl.Name) > maxName {
			maxName = len(pl.Name)
		}
	}
	for _, pl := range lists {
		fmt.Printf("  %-*s  %d tracks\n", maxName, pl.Name, pl.TrackCount)
	}
	return nil
}

// PlaylistCreate creates a new playlist. dirs are referenced as [[dir]] sources
// (scanned at load time) instead of expanding their files; paths are expanded
// into explicit [[track]] entries. If sshHost is non-empty, paths are walked
// remotely via SSH and cannot be combined with dirs.
func PlaylistCreate(name string, paths []string, sshHost string, dirs []string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if prov.Exists(name) {
		return fmt.Errorf("playlist %q already exists (use `add` to append)", name)
	}
	if sshHost != "" && len(dirs) > 0 {
		return fmt.Errorf("--dir cannot be combined with --ssh (SSH playlists do not support directory sources)")
	}
	if len(paths) == 0 && sshHost == "" && len(dirs) == 0 {
		if _, err := prov.CreatePlaylist(context.Background(), name); err != nil {
			return fmt.Errorf("creating playlist: %w", err)
		}
		fmt.Printf("Created empty playlist %q.\n", name)
		return nil
	}

	// Directories-only creation needs no audio collection.
	if len(paths) == 0 && sshHost == "" {
		if err := prov.CreateDirPlaylist(name, dirs); err != nil {
			return fmt.Errorf("creating playlist: %w", err)
		}
		if len(dirs) == 1 {
			fmt.Printf("Created playlist %q referencing directory %s.\n", name, dirs[0])
		} else {
			fmt.Printf("Created playlist %q referencing %d directories.\n", name, len(dirs))
		}
		return nil
	}

	// Collect explicit audio paths before any mutation, so a failure (no audio
	// found, invalid directory) leaves no partially-created playlist behind.
	var audioPaths []string
	if sshHost != "" {
		remotePaths, err := sshFindAudio(sshHost, paths)
		if err != nil {
			return err
		}
		audioPaths = remotePaths
	} else {
		collected, err := collectLocalAudio(paths)
		if err != nil {
			return err
		}
		audioPaths = collected
	}

	if len(audioPaths) == 0 {
		return fmt.Errorf("no audio files found in %s", strings.Join(paths, ", "))
	}

	tracks := make([]playlist.Track, len(audioPaths))
	for i, ap := range audioPaths {
		if sshHost != "" {
			tracks[i] = playlist.TrackFromFilename(ap)
			tracks[i].Path = "ssh://" + sshHost + ap
		} else {
			tracks[i] = playlist.TrackFromPath(ap)
		}
	}

	albumAwareSort(tracks)
	if len(dirs) > 0 {
		if err := prov.CreateDirPlaylist(name, dirs); err != nil {
			return fmt.Errorf("creating playlist: %w", err)
		}
	}
	added, skipped, err := prov.AddTracks(name, tracks)
	if err != nil {
		return fmt.Errorf("writing playlist: %w", err)
	}

	if len(dirs) > 0 {
		if skipped > 0 {
			fmt.Printf("Created playlist %q with %d director%s and %d explicit track%s (%d duplicate skipped).\n", name, len(dirs), plural(len(dirs)), added, pluralS(added), skipped)
		} else {
			fmt.Printf("Created playlist %q with %d director%s and %d explicit track%s.\n", name, len(dirs), plural(len(dirs)), added, pluralS(added))
		}
		return nil
	}
	if skipped > 0 {
		fmt.Printf("Created playlist %q with %d track%s (%d duplicate skipped).\n", name, added, pluralS(added), skipped)
	} else {
		fmt.Printf("Created playlist %q with %d track%s.\n", name, added, pluralS(added))
	}
	return nil
}

// PlaylistAdd appends tracks from the given paths and/or directory sources to
// an existing playlist.
func PlaylistAdd(name string, paths []string, dirs []string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if !prov.Exists(name) {
		return fmt.Errorf("playlist %q not found", name)
	}
	if len(paths) == 0 && len(dirs) == 0 {
		return fmt.Errorf("no files or directories given")
	}

	// Resolve explicit audio paths before mutating anything.
	var audioPaths []string
	if len(paths) > 0 {
		var err error
		audioPaths, err = collectLocalAudio(paths)
		if err != nil {
			return err
		}
		if len(audioPaths) == 0 {
			return fmt.Errorf("no audio files found in %s", strings.Join(paths, ", "))
		}
	}

	// Validate and persist all directory sources in one atomic step.
	addedDirs, err := prov.AddDirSources(name, dirs)
	if err != nil {
		return fmt.Errorf("adding directory sources: %w", err)
	}

	if len(audioPaths) == 0 {
		if len(addedDirs) == 0 {
			fmt.Printf("No new directories added to %q (already present).\n", name)
		} else if len(addedDirs) == 1 {
			fmt.Printf("Added directory %q to %q.\n", addedDirs[0], name)
		} else {
			fmt.Printf("Added %d directories to %q.\n", len(addedDirs), name)
		}
		return nil
	}

	tracks := make([]playlist.Track, len(audioPaths))
	for i, ap := range audioPaths {
		tracks[i] = playlist.TrackFromPath(ap)
	}

	albumAwareSort(tracks)
	added, skipped, err := prov.AddTracks(name, tracks)
	if err != nil {
		return fmt.Errorf("adding tracks: %w", err)
	}

	if len(addedDirs) > 0 {
		fmt.Printf("Added %d director%s and %d tracks to %q.\n", len(addedDirs), plural(len(addedDirs)), added, name)
	} else if skipped > 0 {
		fmt.Printf("Added %d tracks to %q (%d duplicate skipped).\n", added, name, skipped)
	} else {
		fmt.Printf("Added %d tracks to %q.\n", added, name)
	}
	return nil
}

// PlaylistDirs lists the directory sources referenced by a playlist.
func PlaylistDirs(name string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	dirs, err := prov.DirSources(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	if len(dirs) == 0 {
		fmt.Printf("Playlist %q has no directory sources.\n", name)
		return nil
	}
	fmt.Printf("Directory sources for %q:\n", name)
	for i, src := range dirs {
		mode := "recursive"
		if !src.Recursive {
			mode = "non-recursive"
		}
		fmt.Printf("  %d. %s (%s)\n", i+1, src.Path, mode)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// PlaylistShow displays the tracks in a playlist. If jsonOutput is true,
// the track list is printed as a JSON array to stdout.
func PlaylistShow(name string, jsonOutput bool) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("playlist %q not found", name)
	}
	if len(tracks) == 0 {
		if jsonOutput {
			fmt.Println("[]")
		} else {
			fmt.Printf("Playlist %q is empty.\n", name)
		}
		return nil
	}

	if jsonOutput {
		type jsonTrack struct {
			Path         string `json:"path"`
			Title        string `json:"title"`
			Artist       string `json:"artist,omitempty"`
			Album        string `json:"album,omitempty"`
			Genre        string `json:"genre,omitempty"`
			Year         int    `json:"year,omitempty"`
			TrackNumber  int    `json:"track_number,omitempty"`
			DurationSecs int    `json:"duration_secs,omitempty"`
			AlbumArtURL  string `json:"album_art_url,omitempty"`
			Bookmark     bool   `json:"bookmark,omitempty"`
		}
		out := make([]jsonTrack, len(tracks))
		for i, t := range tracks {
			out[i] = jsonTrack{
				Path:         t.Path,
				Title:        t.Title,
				Artist:       t.Artist,
				Album:        t.Album,
				Genre:        t.Genre,
				Year:         t.Year,
				TrackNumber:  t.TrackNumber,
				DurationSecs: t.DurationSecs,
				AlbumArtURL:  t.AlbumArtURL,
				Bookmark:     t.Bookmark,
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("Playlist: %s (%d tracks)\n\n", name, len(tracks))
	for i, t := range tracks {
		display := t.Title
		if t.Artist != "" {
			display = t.Artist + " - " + t.Title
		}
		fmt.Printf("  %3d. %s\n", i+1, display)
	}
	return nil
}

// PlaylistRemove removes a track by index from the named playlist.
// The index is 1-based for the user, converted to 0-based internally.
func PlaylistRemove(name string, index int) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if err := prov.RemoveTrack(name, index-1); err != nil {
		return fmt.Errorf("removing track %d from %q: %w", index, name, err)
	}

	fmt.Printf("Removed track %d from %q.\n", index, name)
	return nil
}

// PlaylistDelete deletes an entire playlist.
func PlaylistDelete(name string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if err := prov.DeletePlaylist(name); err != nil {
		return fmt.Errorf("deleting playlist %q: %w", name, err)
	}

	fmt.Printf("Deleted playlist %q.\n", name)
	return nil
}

// PlaylistRename renames a local playlist.
func PlaylistRename(oldName, newName string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	if err := prov.RenamePlaylist(oldName, newName); err != nil {
		return fmt.Errorf("renaming playlist %q to %q: %w", oldName, newName, err)
	}
	fmt.Printf("Renamed playlist %q to %q.\n", oldName, newName)
	return nil
}

// PlaylistDedupe removes duplicate tracks by exact path, keeping first wins.
func PlaylistDedupe(name string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	seen := make(map[string]struct{}, len(tracks))
	kept := tracks[:0]
	removed := 0
	for _, t := range tracks {
		if _, ok := seen[t.Path]; ok {
			removed++
			fmt.Printf("  removed duplicate: %s\n", t.Path)
			continue
		}
		seen[t.Path] = struct{}{}
		kept = append(kept, t)
	}
	if removed == 0 {
		fmt.Printf("No duplicates found in %q.\n", name)
		return nil
	}
	if err := prov.SavePlaylist(name, kept); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}
	fmt.Printf("Removed %d duplicate tracks from %q.\n", removed, name)
	return nil
}

// PlaylistSort sorts a playlist in place by one of the supported metadata keys.
func PlaylistSort(name, by string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	if err := sortTracks(tracks, by); err != nil {
		return err
	}
	if err := prov.SavePlaylist(name, tracks); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}
	fmt.Printf("Sorted %q by %s.\n", name, normalizeSortKey(by))
	if dirs, _ := prov.DirSources(name); len(dirs) > 0 {
		fmt.Println("Note: directory-sourced tracks reload in directory scan order on the next load.")
	}
	return nil
}

// PlaylistDoctor reports missing local files and optionally prunes them.
func PlaylistDoctor(name string, fix bool) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	names := []string{name}
	if name == "" {
		lists, err := prov.Playlists()
		if err != nil {
			return fmt.Errorf("listing playlists: %w", err)
		}
		names = names[:0]
		for _, pl := range lists {
			if pl.Name != "Recently Played" {
				names = append(names, pl.Name)
			}
		}
	}

	totalMissing := 0
	for _, plName := range names {
		tracks, err := prov.Tracks(plName)
		if err != nil {
			return fmt.Errorf("loading playlist %q: %w", plName, err)
		}
		kept := tracks[:0]
		missing := 0
		for _, t := range tracks {
			if missingLocalFile(t) {
				missing++
				totalMissing++
				fmt.Printf("  [%s] missing: %s\n", plName, t.Path)
				if fix {
					continue
				}
			}
			kept = append(kept, t)
		}
		if fix && missing > 0 {
			if err := prov.SavePlaylist(plName, kept); err != nil {
				return fmt.Errorf("saving playlist %q: %w", plName, err)
			}
			fmt.Printf("Pruned %d missing tracks from %q.\n", missing, plName)
		}
	}
	if totalMissing == 0 {
		fmt.Println("No missing local files found.")
	}
	return nil
}

// PlaylistExport writes a playlist as M3U or PLS.
func PlaylistExport(name, format, output string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "m3u"
	}
	switch format {
	case "m3u", "m3u8", "pls":
	default:
		return fmt.Errorf("unsupported export format %q (use m3u or pls)", format)
	}

	var w io.Writer = os.Stdout
	var f *os.File
	if output != "" {
		f, err = os.Create(output)
		if err != nil {
			return fmt.Errorf("creating %q: %w", output, err)
		}
		defer f.Close()
		w = f
	}

	switch format {
	case "m3u", "m3u8":
		writeM3U(w, tracks)
	case "pls":
		writePLS(w, tracks)
	}
	if output != "" {
		fmt.Printf("Exported %q to %s.\n", name, output)
	}
	return nil
}

// PlaylistImport converts a local M3U/PLS file into a TOML playlist.
func PlaylistImport(path, name string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if prov.Exists(name) {
		return fmt.Errorf("playlist %q already exists", name)
	}
	tracks, err := resolve.LocalPlaylist(path)
	if err != nil {
		return fmt.Errorf("importing %q: %w", path, err)
	}
	if err := prov.SavePlaylist(name, tracks); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}
	fmt.Printf("Imported %d tracks into %q.\n", len(tracks), name)
	return nil
}

// PlaylistBookmark toggles the bookmark flag on a track by index.
func PlaylistBookmark(name string, index int) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}
	if index-1 < 0 || index-1 >= len(tracks) {
		return fmt.Errorf("track index %d out of range (playlist has %d tracks)", index, len(tracks))
	}
	original := tracks[index-1]

	if err := prov.SetBookmark(name, index-1); err != nil {
		return fmt.Errorf("toggling bookmark: %w", err)
	}

	// Reload and report the toggled track. Bookmarking a directory-sourced
	// track materializes it, which can move it within the expanded list, so
	// match by path rather than by index.
	tracks, err = prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("reloading playlist %q: %w", name, err)
	}
	for _, t := range tracks {
		if t.Path == original.Path {
			if t.Bookmark {
				fmt.Printf("★ %s\n", t.DisplayName())
			} else {
				fmt.Printf("☆ %s\n", t.DisplayName())
			}
			return nil
		}
	}
	return fmt.Errorf("track %d no longer exists in playlist (now has %d tracks)", index, len(tracks))
}

// PlaylistBookmarks lists all bookmarked tracks across all playlists.
func PlaylistBookmarks() error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	lists, err := prov.Playlists()
	if err != nil {
		return fmt.Errorf("listing playlists: %w", err)
	}

	total := 0
	for _, pl := range lists {
		tracks, err := prov.Tracks(pl.Name)
		if err != nil {
			continue
		}
		for i, t := range tracks {
			if t.Bookmark {
				fmt.Printf("  ★ [%s] %d. %s\n", pl.Name, i+1, t.DisplayName())
				total++
			}
		}
	}

	if total == 0 {
		fmt.Println("No bookmarks yet. Press f on a track to bookmark it.")
	} else {
		fmt.Printf("\n  %d bookmarks across %d playlists.\n", total, len(lists))
	}
	return nil
}

// PlaylistEnrich probes duration and derives album metadata for SSH tracks.
func PlaylistEnrich(name string, source string) error {
	src := normalizeSortKey(source)
	switch src {
	case "path", "metadata":
	default:
		return fmt.Errorf("unsupported source key %q (use path or metadata)", source)
	}

	prov, err := newProvider()
	if err != nil {
		return err
	}

	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}

	updated := 0
	dirSourced := 0
	for i, t := range tracks {
		if t.DirSourced {
			dirSourced++
			continue
		}
		changed := false

		if t.DurationSecs == 0 {
			dur := probeDuration(t.Path)
			if dur > 0 {
				tracks[i].DurationSecs = dur
				changed = true
				fmt.Fprintf(os.Stderr, "  %s: %ds\n", t.DisplayName(), dur)
			}
		}

		if t.Album == "" {
			if src == "path" {
				if dir := albumFromPath(t.Path); dir != "" {
					tracks[i].Album = dir
					changed = true
				}
			} else if src == "metadata" {
				if album := probeAlbum(t.Path); album != "" {
					tracks[i].Album = album
					changed = true
				}
			}
		}
		if t.Year == 0 {
			if year := probeYear(t.Path); year != 0 {
				tracks[i].Year = year
				changed = true
				fmt.Fprintf(os.Stderr, "  %s: %d\n", t.DisplayName(), year)
			}
		}

		if changed {
			updated++
		}
	}

	if updated == 0 {
		if dirSourced > 0 {
			fmt.Println("All explicit tracks already enriched; directory-sourced tracks are read from their files at load time.")
		} else {
			fmt.Println("All tracks already enriched.")
		}
		return nil
	}

	if err := prov.SavePlaylist(name, tracks); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}

	if dirSourced > 0 {
		fmt.Printf("Enriched %d tracks in %q (%d directory-sourced tracks left to their files).\n", updated, name, dirSourced)
		return nil
	}
	fmt.Printf("Enriched %d tracks in %q.\n", updated, name)
	return nil
}

func probeRemoteDuration(host, remotePath string) int {
	// Use ffprobe over SSH for cross-platform compatibility (works on Linux and macOS remotes).
	probeCmd := fmt.Sprintf("ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 %s 2>/dev/null", shellQuote(remotePath))
	cmd := exec.Command("ssh",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=5",
		host, probeCmd,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return parseProbeDuration(out)
}

func probeDuration(path string) int {
	if strings.HasPrefix(path, "ssh://") {
		parsed, err := sshurl.Parse(path)
		if err != nil {
			return 0
		}
		return probeRemoteDuration(parsed.Host, parsed.Path)
	}
	if playlist.IsURL(path) || path == "" {
		return 0
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return parseProbeDuration(out)
}

func parseProbeDuration(out []byte) int {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	var dur float64
	fmt.Sscanf(s, "%f", &dur)
	if dur <= 0 {
		return 0
	}
	return int(dur)
}

func probeYear(path string) int {
	t := playlist.TrackFromPath(path)
	return t.Year
}

func probeAlbum(path string) string {
	t := playlist.TrackFromPath(path)
	return t.Album
}

func albumFromPath(path string) string {
	if path == "" || playlist.IsURL(path) {
		return ""
	}
	if strings.HasPrefix(path, "ssh://") {
		parsed, err := sshurl.Parse(path)
		if err != nil {
			return ""
		}
		path = parsed.Path
	}
	dir := filepath.Base(filepath.Dir(path))
	if dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	return dir
}

// collectLocalAudio resolves file/directory paths into audio file paths
// using the canonical supported extensions from the player package.
func collectLocalAudio(paths []string) ([]string, error) {
	var all []string
	for _, p := range paths {
		files, err := resolve.CollectAudioFiles(p)
		if err != nil {
			return nil, fmt.Errorf("scanning %q: %w", p, err)
		}
		for _, f := range files {
			all = append(all, strings.ReplaceAll(f, "\\", "/"))
		}
	}
	return all, nil
}

func albumAwareSort(tracks []playlist.Track) {
	if len(tracks) < 2 {
		return
	}
	for _, t := range tracks {
		if t.Album == "" || t.TrackNumber == 0 {
			return
		}
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		a, b := tracks[i], tracks[j]
		if c := strings.Compare(strings.ToLower(a.Artist), strings.ToLower(b.Artist)); c != 0 {
			return c < 0
		}
		if c := strings.Compare(strings.ToLower(a.Album), strings.ToLower(b.Album)); c != 0 {
			return c < 0
		}
		if a.TrackNumber != b.TrackNumber {
			return a.TrackNumber < b.TrackNumber
		}
		return strings.ToLower(a.Path) < strings.ToLower(b.Path)
	})
}

func normalizeSortKey(by string) string {
	switch strings.ToLower(strings.TrimSpace(by)) {
	case "", "title":
		return "title"
	case "track", "track#", "track_number", "track-number":
		return "track"
	case "artist":
		return "artist"
	case "album":
		return "album"
	case "artist+album", "artist_album", "artist-album":
		return "artist+album"
	case "path":
		return "path"
	default:
		return by
	}
}

func sortTracks(tracks []playlist.Track, by string) error {
	key := normalizeSortKey(by)
	switch key {
	case "title", "track", "artist", "album", "artist+album", "path":
	default:
		return fmt.Errorf("unsupported sort key %q (use track, title, artist, album, artist+album, or path)", by)
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		return compareTracks(tracks[i], tracks[j], key) < 0
	})
	return nil
}

func compareTracks(a, b playlist.Track, key string) int {
	cmpString := func(x, y string) int {
		return strings.Compare(strings.ToLower(x), strings.ToLower(y))
	}
	firstNonZero := func(values ...int) int {
		for _, v := range values {
			if v != 0 {
				return v
			}
		}
		return 0
	}
	switch key {
	case "track":
		return firstNonZero(a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist":
		return firstNonZero(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "album":
		return firstNonZero(cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist+album":
		return firstNonZero(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "path":
		return cmpString(a.Path, b.Path)
	default:
		return firstNonZero(cmpString(a.Title, b.Title), cmpString(a.Artist, b.Artist), cmpString(a.Path, b.Path))
	}
}

func missingLocalFile(t playlist.Track) bool {
	if t.Path == "" || t.Stream || playlist.IsURL(t.Path) || strings.HasPrefix(t.Path, "ssh://") {
		return false
	}
	_, err := os.Stat(t.Path)
	return errors.Is(err, os.ErrNotExist)
}

func writeM3U(w io.Writer, tracks []playlist.Track) {
	fmt.Fprintln(w, "#EXTM3U")
	for _, t := range tracks {
		title := t.DisplayName()
		if title == "" {
			title = t.Path
		}
		duration := t.DurationSecs
		if duration <= 0 {
			duration = -1
		}
		fmt.Fprintf(w, "#EXTINF:%d,%s\n", duration, title)
		fmt.Fprintln(w, t.Path)
	}
}

func writePLS(w io.Writer, tracks []playlist.Track) {
	fmt.Fprintln(w, "[playlist]")
	for i, t := range tracks {
		n := i + 1
		fmt.Fprintf(w, "File%d=%s\n", n, t.Path)
		if title := t.DisplayName(); title != "" {
			fmt.Fprintf(w, "Title%d=%s\n", n, title)
		}
		length := t.DurationSecs
		if length <= 0 {
			length = -1
		}
		fmt.Fprintf(w, "Length%d=%d\n", n, length)
	}
	fmt.Fprintf(w, "NumberOfEntries=%d\nVersion=2\n", len(tracks))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func sshFindAudio(host string, paths []string) ([]string, error) {
	var nameArgs []string
	first := true
	for ext := range player.SupportedExts {
		if !first {
			nameArgs = append(nameArgs, "-o")
		}
		nameArgs = append(nameArgs, "-name", "'*"+ext+"'")
		first = false
	}

	var allFiles []string
	for _, p := range paths {
		findCmd := fmt.Sprintf("find %s -type f \\( %s \\) | sort",
			shellQuote(p), strings.Join(nameArgs, " "))

		sshArgs := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", host, findCmd}
		cmd := exec.Command("ssh", sshArgs...)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ssh find on %s:%s: %w", host, p, err)
		}

		lines := strings.SplitSeq(strings.TrimSpace(string(out)), "\n")
		for line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				allFiles = append(allFiles, line)
			}
		}
	}

	return allFiles, nil
}

func newProvider() (*local.Provider, error) {
	p := local.New()
	if p == nil {
		return nil, fmt.Errorf("failed to initialize local playlist provider")
	}
	return p, nil
}
