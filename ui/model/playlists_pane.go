package model

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

// renderPlaylistsPane lists the saved playlists holding the highlighted track.
// It takes the Metadata pane's place and row budget below Settings.
func (m Model) renderPlaylistsPane(rows int) []string {
	if rows <= 0 {
		return nil
	}
	w := m.layout.settingsWidth
	lines := []string{fillSeparator(sepHeader("Playlists [Ctrl+L]"), w)}
	track := m.selectedMetadataTrack()
	member := m.membershipFor(track)
	switch {
	case track.Path == "":
		return append(lines, dimStyle.Render(truncate("No track selected", w)))
	case member == nil:
		return append(lines, dimStyle.Render(truncate("Local playlists unavailable", w)))
	case len(member) == 0:
		return append(lines, dimStyle.Render(truncate("Not in any playlist", w)))
	}

	// Favorites leads, as it does in the playlist browser.
	names := slices.Sorted(maps.Keys(member))
	if i := slices.Index(names, favorites.PlaylistName); i > 0 {
		names = slices.Insert(slices.Delete(names, i, i+1), 0, favorites.PlaylistName)
	}

	count := rows - 1
	if len(names) > count {
		count-- // keep a route to the complete, editable list
	}
	for _, name := range names[:min(len(names), max(0, count))] {
		lines = append(lines, membershipMark(member[name])+" "+trackStyle.Render(truncate(name, max(1, w-2))))
	}
	if hidden := len(names) - max(0, count); hidden > 0 {
		lines = append(lines, dimStyle.Render(truncate(fmt.Sprintf("w: edit (+%d more)", hidden), w)))
	}
	return lines
}

// togglePlaylistsPane mirrors toggleMetadata: the two panes share one slot, so
// showing one hides the other. Without room beside the playlist, it opens the
// playlist picker for the highlighted track instead.
func (m *Model) togglePlaylistsPane() {
	if m.showMetadata {
		m.SetShowMetadata(false)
		m.saveConfigKey("show_metadata", "false")
	}
	m.SetShowPlaylists(!m.showPlaylists)
	m.saveConfigKey("show_playlists", strconv.FormatBool(m.showPlaylists))
	if m.showPlaylists && (!m.layout.twoColumn || m.metadataPaneRows(m.effectivePlaylistVisible()) == 0) {
		if track := m.selectedMetadataTrack(); track.Path != "" {
			m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
		}
	}
}

// hidePlaylistsPane gives the shared slot back to Metadata.
func (m *Model) hidePlaylistsPane() {
	if m.showPlaylists {
		m.SetShowPlaylists(false)
		m.saveConfigKey("show_playlists", "false")
	}
}
