// Package cmd implements CLI subcommands for cliamp.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cliamp/external/local"
	"cliamp/playlist"
)

// audioExtensions lists file extensions recognized as audio files.
var audioExtensions = map[string]bool{
	".mp3":  true,
	".flac": true,
	".m4a":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".wma":  true,
}

// isAudioFile reports whether the path has a recognized audio extension.
func isAudioFile(path string) bool {
	return audioExtensions[strings.ToLower(filepath.Ext(path))]
}

// PlaylistList prints all playlists with their track counts.
func PlaylistList() error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	lists, err := prov.Playlists()
	if err != nil {
		return fmt.Errorf("listing playlists: %w", err)
	}
	if len(lists) == 0 {
		fmt.Println("No playlists found.")
		return nil
	}

	// Find the longest name for alignment.
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

// PlaylistCreate creates a new playlist from the given file and directory paths.
// If sshHost is non-empty, remote paths are walked via SSH.
func PlaylistCreate(name string, paths []string, sshHost string) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	// Check if playlist already exists.
	existing, err := prov.Playlists()
	if err != nil {
		return fmt.Errorf("checking existing playlists: %w", err)
	}
	for _, pl := range existing {
		if pl.Name == name {
			return fmt.Errorf("playlist %q already exists (use `add` to append)", name)
		}
	}

	var audioPaths []string

	if sshHost != "" {
		// Remote directory walking via SSH.
		remotePaths, err := sshFindAudio(sshHost, paths)
		if err != nil {
			return err
		}
		audioPaths = remotePaths
	} else {
		// Local directory walking.
		for _, p := range paths {
			info, err := os.Stat(p)
			if err != nil {
				return fmt.Errorf("stat %q: %w", p, err)
			}
			if info.IsDir() {
				found, err := walkDir(p)
				if err != nil {
					return fmt.Errorf("walking %q: %w", p, err)
				}
				audioPaths = append(audioPaths, found...)
			} else if isAudioFile(p) {
				audioPaths = append(audioPaths, p)
			}
		}
	}

	if len(audioPaths) == 0 {
		dirs := strings.Join(paths, ", ")
		return fmt.Errorf("no audio files found in %s", dirs)
	}

	for _, ap := range audioPaths {
		var t playlist.Track
		if sshHost != "" {
			// For SSH paths, derive title from filename (no tag reading over SSH).
			t = trackFromFilename(ap)
			t.Path = "ssh://" + sshHost + ap
		} else {
			t = playlist.TrackFromPath(ap)
		}
		if err := prov.AddTrack(name, t); err != nil {
			return fmt.Errorf("adding track %q: %w", ap, err)
		}
	}

	fmt.Printf("Created playlist %q with %d tracks.\n", name, len(audioPaths))
	return nil
}

// PlaylistAdd appends tracks from the given paths to an existing playlist.
func PlaylistAdd(name string, paths []string) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	// Verify playlist exists.
	exists, err := playlistExists(prov, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("playlist %q not found", name)
	}

	var audioPaths []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("stat %q: %w", p, err)
		}
		if info.IsDir() {
			found, err := walkDir(p)
			if err != nil {
				return fmt.Errorf("walking %q: %w", p, err)
			}
			audioPaths = append(audioPaths, found...)
		} else if isAudioFile(p) {
			audioPaths = append(audioPaths, p)
		}
	}

	if len(audioPaths) == 0 {
		dirs := strings.Join(paths, ", ")
		return fmt.Errorf("no audio files found in %s", dirs)
	}

	for _, ap := range audioPaths {
		t := playlist.TrackFromPath(ap)
		if err := prov.AddTrack(name, t); err != nil {
			return fmt.Errorf("adding track %q: %w", ap, err)
		}
	}

	fmt.Printf("Added %d tracks to %q.\n", len(audioPaths), name)
	return nil
}

// PlaylistShow displays the tracks in a playlist. If jsonOutput is true,
// the track list is printed as a JSON array to stdout.
func PlaylistShow(name string, jsonOutput bool) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
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
			Favorite     bool   `json:"favorite,omitempty"`
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
				Favorite:     t.Favorite,
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
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	// Convert 1-based user index to 0-based.
	zeroIndex := index - 1
	if err := prov.RemoveTrack(name, zeroIndex); err != nil {
		return fmt.Errorf("removing track %d from %q: %w", index, name, err)
	}

	fmt.Printf("Removed track %d from %q.\n", index, name)
	return nil
}

// PlaylistDelete deletes an entire playlist.
func PlaylistDelete(name string) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	if err := prov.DeletePlaylist(name); err != nil {
		return fmt.Errorf("deleting playlist %q: %w", name, err)
	}

	fmt.Printf("Deleted playlist %q.\n", name)
	return nil
}

// PlaylistFavorite toggles the favorite flag on a track by index.
func PlaylistFavorite(name string, index int) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	if err := prov.SetFavorite(name, index-1); err != nil { // 1-based to 0-based
		return fmt.Errorf("toggling favorite: %w", err)
	}

	// Re-read to show the result.
	tracks, err := prov.Tracks(name)
	if err != nil {
		return err
	}
	if index-1 < 0 || index-1 >= len(tracks) {
		return fmt.Errorf("track %d no longer exists in playlist (now has %d tracks)", index, len(tracks))
	}
	t := tracks[index-1]
	if t.Favorite {
		fmt.Printf("★ %s\n", t.DisplayName())
	} else {
		fmt.Printf("☆ %s\n", t.DisplayName())
	}
	return nil
}

// PlaylistFavorites lists all favorited tracks across all playlists.
func PlaylistFavorites() error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
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
			if t.Favorite {
				fmt.Printf("  ★ [%s] %d. %s\n", pl.Name, i+1, t.DisplayName())
				total++
			}
		}
	}

	if total == 0 {
		fmt.Println("No favorites yet. Press * on a track to favorite it.")
	} else {
		fmt.Printf("\n  %d favorites across %d playlists.\n", total, len(lists))
	}
	return nil
}

// PlaylistEnrich probes duration and derives album metadata for SSH tracks in a playlist.
// Uses afinfo (macOS) on the remote host to probe duration, and derives album from
// the parent directory name.
func PlaylistEnrich(name string) error {
	prov := local.New()
	if prov == nil {
		return fmt.Errorf("failed to initialize local playlist provider")
	}

	tracks, err := prov.Tracks(name)
	if err != nil {
		return fmt.Errorf("loading playlist %q: %w", name, err)
	}

	updated := 0
	for i, t := range tracks {
		if !strings.HasPrefix(t.Path, "ssh://") {
			continue
		}

		// Parse ssh://host/path
		rest := strings.TrimPrefix(t.Path, "ssh://")
		slashIdx := strings.IndexByte(rest, '/')
		if slashIdx < 0 {
			continue
		}
		host := rest[:slashIdx]
		remotePath := rest[slashIdx:]

		changed := false

		// Probe duration if missing.
		if t.DurationSecs == 0 {
			dur := probeRemoteDuration(host, remotePath)
			if dur > 0 {
				tracks[i].DurationSecs = dur
				changed = true
				fmt.Fprintf(os.Stderr, "  %s: %ds\n", t.DisplayName(), dur)
			}
		}

		// Derive album from parent directory if missing.
		if t.Album == "" {
			dir := filepath.Base(filepath.Dir(remotePath))
			if dir != "" && dir != "." {
				tracks[i].Album = dir
				changed = true
			}
		}

		if changed {
			updated++
		}
	}

	if updated == 0 {
		fmt.Println("All tracks already enriched.")
		return nil
	}

	if err := prov.SavePlaylist(name, tracks); err != nil {
		return fmt.Errorf("saving playlist %q: %w", name, err)
	}

	fmt.Printf("Enriched %d tracks in %q.\n", updated, name)
	return nil
}

// probeRemoteDuration uses afinfo on the remote host to get track duration in seconds.
func probeRemoteDuration(host, remotePath string) int {
	cmd := exec.Command("ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		host, fmt.Sprintf("afinfo %s 2>/dev/null | grep 'estimated duration' | awk '{print int($3)}'", shellQuote(remotePath)),
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	var dur int
	fmt.Sscanf(s, "%d", &dur)
	return dur
}

// walkDir recursively finds audio files in a directory, returning sorted paths.
func walkDir(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && isAudioFile(path) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// sshFindAudio uses SSH to find audio files on a remote host.
func sshFindAudio(host string, paths []string) ([]string, error) {
	// Build the find command with all audio extensions.
	var nameArgs []string
	first := true
	for ext := range audioExtensions {
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

		cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", host, findCmd)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ssh find on %s:%s: %w", host, p, err)
		}

		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				allFiles = append(allFiles, line)
			}
		}
	}

	return allFiles, nil
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// trackFromFilename creates a Track by parsing "Artist - Title" from the
// filename, or using the bare filename as the title.
func trackFromFilename(path string) playlist.Track {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.SplitN(name, " - ", 2)
	if len(parts) == 2 {
		return playlist.Track{
			Path:   path,
			Artist: strings.TrimSpace(parts[0]),
			Title:  strings.TrimSpace(parts[1]),
		}
	}
	return playlist.Track{Path: path, Title: name}
}

// playlistExists checks whether a playlist with the given name exists.
func playlistExists(prov *local.Provider, name string) (bool, error) {
	lists, err := prov.Playlists()
	if err != nil {
		return false, fmt.Errorf("checking playlists: %w", err)
	}
	for _, pl := range lists {
		if pl.Name == name {
			return true, nil
		}
	}
	return false, nil
}
