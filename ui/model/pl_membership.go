package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

const membershipCheck = "✓"

// playlistMembership is implemented by the local provider: which saved
// playlists hold a path, and removal of an explicit entry by path.
type playlistMembership interface {
	// PlaylistMembership maps playlist name to locked, where locked means only
	// a [[dir]] source supplies the track.
	PlaylistMembership(path string) (map[string]bool, error)
	RemoveTrackByPath(name, path string) error
}

// playlistMembershipCache remembers the lookup for the last path so the
// render path reads playlist files only when the highlighted track changes.
type playlistMembershipCache struct {
	path  string
	valid bool
	lists map[string]bool
}

func (c *playlistMembershipCache) invalidate() {
	if c != nil {
		c.valid = false
	}
}

// membershipFor returns the playlists holding track, or nil when the local
// provider cannot answer. The map is shared with the cache; do not modify it.
func (m Model) membershipFor(track playlist.Track) map[string]bool {
	c := m.plMembers
	if c != nil && c.valid && c.path == track.Path {
		return c.lists
	}
	pm, ok := m.localProvider.(playlistMembership)
	if !ok || track.Path == "" {
		return nil
	}
	lists, _ := pm.PlaylistMembership(track.Path) // nil on error: nothing to show
	if c != nil {
		c.path, c.valid, c.lists = track.Path, true, lists
	}
	return lists
}

// togglePickerMembership handles Enter on a playlist the single picked track
// already belongs to, and on Favorites, which the batch write path rejects.
// It reports whether it handled the row.
func (m *Model) togglePickerMembership(name string) (bool, tea.Cmd) {
	if len(m.plPicker.tracks) != 1 {
		return false, nil
	}
	track := m.plPicker.tracks[0]
	if name == favorites.PlaylistName {
		if m.favMgr == nil {
			return false, nil
		}
		added, err := m.favMgr.ToggleFavorite(track)
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
			return true, nil
		}
		m.refreshFavSet()
		m.plMembers.invalidate()
		if added {
			m.status.Showf(statusTTLDefault, "Added to %q", name)
		} else {
			m.status.Showf(statusTTLDefault, "Removed from %q", name)
		}
		return true, m.fetchProviderPlaylists()
	}
	locked, member := m.plPicker.member[name]
	if !member {
		return false, nil
	}
	if locked {
		m.status.Warningf(statusTTLDefault, "%q adds this track from a folder; edit its folders to remove it", name)
		return true, nil
	}
	pm, ok := m.localProvider.(playlistMembership)
	if !ok {
		return false, nil
	}
	if err := pm.RemoveTrackByPath(name, track.Path); err != nil {
		m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
		return true, nil
	}
	m.status.Showf(statusTTLDefault, "Removed from %q", name)
	m.refreshPlaylistManagerAfterWrite(name)
	return true, nil
}

// plPickerList renders the picker rows. With a single picked track, a check
// column left of the cursor marks the playlists that already hold it; the
// mark stays outside the row style so the row keeps its own color.
func (m Model) plPickerList(items []string, budget int) string {
	member := m.plPicker.member
	if member == nil {
		return windowList(items, m.plPicker.cursor, m.plPicker.scroll, budget)
	}
	if budget <= 0 {
		return ""
	}
	lines := make([]string, 0, budget)
	for i := m.plPicker.scroll; i < len(items) && len(lines) < budget; i++ {
		mark := "  "
		if i < len(m.plPicker.playlists) {
			if locked, ok := member[m.plPicker.playlists[i].Name]; ok {
				mark = membershipMark(locked) + " "
			}
		}
		lines = append(lines, mark+cursorLine(items[i], i == m.plPicker.cursor))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

// membershipMark is green for a removable membership and dim for one a
// directory source supplies.
func membershipMark(locked bool) string {
	if locked {
		return dimStyle.Render(membershipCheck)
	}
	return feedbackSuccessStyle.Render(membershipCheck)
}
