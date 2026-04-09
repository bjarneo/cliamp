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
	"cliamp/internal/sshurl"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
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

// PlaylistCreate creates a new playlist from the given file and directory paths.
// If sshHost is non-empty, remote paths are walked via SSH.
func PlaylistCreate(name string, paths []string, sshHost string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if prov.Exists(name) {
		return fmt.Errorf("playlist %q already exists (use `add` to append)", name)
	}

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

	if err := prov.AddTracks(name, tracks); err != nil {
		return fmt.Errorf("writing playlist: %w", err)
	}

	fmt.Printf("Created playlist %q with %d tracks.\n", name, len(audioPaths))
	return nil
}

// PlaylistAdd appends tracks from the given paths to an existing playlist.
func PlaylistAdd(name string, paths []string) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if !prov.Exists(name) {
		return fmt.Errorf("playlist %q not found", name)
	}

	audioPaths, err := collectLocalAudio(paths)
	if err != nil {
		return err
	}
	if len(audioPaths) == 0 {
		return fmt.Errorf("no audio files found in %s", strings.Join(paths, ", "))
	}

	tracks := make([]playlist.Track, len(audioPaths))
	for i, ap := range audioPaths {
		tracks[i] = playlist.TrackFromPath(ap)
	}

	if err := prov.AddTracks(name, tracks); err != nil {
		return fmt.Errorf("adding tracks: %w", err)
	}

	fmt.Printf("Added %d tracks to %q.\n", len(audioPaths), name)
	return nil
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

// PlaylistFavorite toggles the favorite flag on a track by index.
func PlaylistFavorite(name string, index int) error {
	prov, err := newProvider()
	if err != nil {
		return err
	}

	if err := prov.SetFavorite(name, index-1); err != nil {
		return fmt.Errorf("toggling favorite: %w", err)
	}

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

// PlaylistEnrich probes duration and derives album metadata for SSH tracks.
func PlaylistEnrich(name string) error {
	prov, err := newProvider()
	if err != nil {
		return err
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

		parsed, err := sshurl.Parse(t.Path)
		if err != nil {
			continue
		}
		host := parsed.Host
		remotePath := parsed.Path

		changed := false

		if t.DurationSecs == 0 {
			dur := probeRemoteDuration(host, remotePath)
			if dur > 0 {
				tracks[i].DurationSecs = dur
				changed = true
				fmt.Fprintf(os.Stderr, "  %s: %ds\n", t.DisplayName(), dur)
			}
		}

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
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	var dur float64
	fmt.Sscanf(s, "%f", &dur)
	return int(dur)
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
		all = append(all, files...)
	}
	return all, nil
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
