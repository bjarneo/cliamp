package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
)

// fbEntry is a single item in the file browser listing.
type fbEntry struct {
	name     string
	path     string
	isDir    bool
	isAudio  bool
	isParent bool
}

// fbTracksResolvedMsg carries tracks resolved from file browser selections.
type fbTracksResolvedMsg struct {
	tracks  []playlist.Track
	replace bool
}

// openFileBrowser initialises and shows the file browser overlay.
func (m *Model) openFileBrowser() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	m.fileBrowser.dir = home
	m.fileBrowser.cursor = 0
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.err = ""
	m.loadFBDir()
	m.fileBrowser.visible = true
}

// loadFBDir reads the current directory and populates fbEntries.
func (m *Model) loadFBDir() {
	m.fileBrowser.err = ""
	m.fileBrowser.entries = nil

	// Always provide a parent entry for navigating up.
	m.fileBrowser.entries = append(m.fileBrowser.entries, fbEntry{
		name:     "..",
		path:     filepath.Dir(m.fileBrowser.dir),
		isDir:    true,
		isParent: true,
	})

	entries, err := os.ReadDir(m.fileBrowser.dir)
	if err != nil {
		m.fileBrowser.err = err.Error()
		m.fileBrowser.cursor = 0
		return
	}

	// Separate dirs and files, skip dotfiles.
	var dirs, files []fbEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(m.fileBrowser.dir, name)
		if e.IsDir() {
			dirs = append(dirs, fbEntry{
				name:  name + "/",
				path:  full,
				isDir: true,
			})
		} else {
			ext := strings.ToLower(filepath.Ext(name))
			files = append(files, fbEntry{
				name:    name,
				path:    full,
				isAudio: player.SupportedExts[ext],
			})
		}
	}

	m.fileBrowser.entries = append(m.fileBrowser.entries, dirs...)
	m.fileBrowser.entries = append(m.fileBrowser.entries, files...)
	m.fileBrowser.cursor = 0
}

// handleFileBrowserKey processes key presses while the file browser is open.
func (m *Model) handleFileBrowserKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.fileBrowser.visible = false
		return m.quit()

	case "esc", "o":
		m.fileBrowser.visible = false
		return nil

	case "up", "k":
		if m.fileBrowser.cursor > 0 {
			m.fileBrowser.cursor--
		} else if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = len(m.fileBrowser.entries)-1
		}

	case "down", "j":
		if m.fileBrowser.cursor < len(m.fileBrowser.entries)-1 {
			m.fileBrowser.cursor++
		} else if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = 0
		}

	case "pgup":
		if m.fileBrowser.cursor > 0 {
			m.fileBrowser.cursor -= min(m.fileBrowser.cursor, 12)
		}

	case "pgdown":
		if m.fileBrowser.cursor < len(m.fileBrowser.entries)-1 {
			m.fileBrowser.cursor = min(len(m.fileBrowser.entries)-1, m.fileBrowser.cursor + 12)
		}

	case "enter", "l", "right":
		if len(m.fileBrowser.selected) > 0 {
			return m.fbConfirm(false)
		}
		if m.fileBrowser.cursor < len(m.fileBrowser.entries) {
			e := m.fileBrowser.entries[m.fileBrowser.cursor]
			if e.isDir {
				m.fileBrowser.dir = e.path
				m.loadFBDir()
			} else if e.isAudio {
				m.fileBrowser.selected[e.path] = true
				return m.fbConfirm(false)
			}
		}

	case "backspace", "h", "left":
		m.fileBrowser.dir = filepath.Dir(m.fileBrowser.dir)
		m.loadFBDir()

	case " ":
		if m.fileBrowser.cursor < len(m.fileBrowser.entries) {
			e := m.fileBrowser.entries[m.fileBrowser.cursor]
			if !e.isParent && (e.isAudio || e.isDir) {
				if m.fileBrowser.selected[e.path] {
					delete(m.fileBrowser.selected, e.path)
				} else {
					m.fileBrowser.selected[e.path] = true
				}
			}
		}

	case "a":
		// Toggle select all audio files in current view.
		allSelected := true
		for _, e := range m.fileBrowser.entries {
			if e.isAudio && !m.fileBrowser.selected[e.path] {
				allSelected = false
				break
			}
		}
		for _, e := range m.fileBrowser.entries {
			if e.isAudio {
				if allSelected {
					delete(m.fileBrowser.selected, e.path)
				} else {
					m.fileBrowser.selected[e.path] = true
				}
			}
		}

	case "g", "home":
		m.fileBrowser.cursor = 0

	case "G", "end":
		if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = len(m.fileBrowser.entries) - 1
		}

	case "R":
		if len(m.fileBrowser.selected) > 0 {
			return m.fbConfirm(true)
		}
	}

	return nil
}

// fbConfirm collects selected paths, closes the overlay, and returns an async
// command that resolves the paths into tracks.
func (m *Model) fbConfirm(replace bool) tea.Cmd {
	var paths []string
	for p := range m.fileBrowser.selected {
		paths = append(paths, p)
	}
	m.fileBrowser.visible = false

	return func() tea.Msg {
		r, err := resolve.Args(paths)
		if err != nil {
			return err
		}
		return fbTracksResolvedMsg{tracks: r.Tracks, replace: replace}
	}
}

// renderFileBrowser renders the file browser overlay.
func (m Model) renderFileBrowser() string {
	lines := []string{
		titleStyle.Render("O P E N  F I L E S"),
		dimStyle.Render("  " + m.fileBrowser.dir),
		"",
	}

	if m.fileBrowser.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.fileBrowser.err))
	}

	maxVisible := 12
	rendered := 0

	if len(m.fileBrowser.entries) == 0 {
		lines = append(lines, dimStyle.Render("  (empty)"))
		rendered = 1
	} else {
		scroll := 0
		if m.fileBrowser.cursor >= maxVisible {
			scroll = m.fileBrowser.cursor - maxVisible + 1
		}

		for i := scroll; i < len(m.fileBrowser.entries) && i < scroll+maxVisible; i++ {
			e := m.fileBrowser.entries[i]

			// Selection check mark.
			check := "  "
			if m.fileBrowser.selected[e.path] {
				check = "✓ "
			}

			// Type indicator suffix.
			suffix := ""
			if e.isAudio {
				suffix = " ♫"
			}

			label := check + e.name + suffix

			// Truncate long names.
			maxW := panelWidth - 4
			labelRunes := []rune(label)
			if len(labelRunes) > maxW {
				label = string(labelRunes[:maxW-1]) + "…"
			}

			if i == m.fileBrowser.cursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else if e.isDir {
				lines = append(lines, trackStyle.Render("  "+label))
			} else if e.isAudio {
				lines = append(lines, playlistItemStyle.Render("  "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	}

	// Pad to fixed height.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

	// Selection count.
	if len(m.fileBrowser.selected) > 0 {
		lines = append(lines, "", statusStyle.Render(fmt.Sprintf("  %d selected", len(m.fileBrowser.selected))))
	} else {
		lines = append(lines, "")
	}

	help := helpKey("↑↓", "Navigate ") + helpKey("Enter", "Open ") + helpKey("Spc", "Select ") + helpKey("a", "All ") + helpKey("←", "Back ")
	if len(m.fileBrowser.selected) > 0 {
		help += helpKey("R", "Replace ")
	}
	help += helpKey("Esc", "Close")
	lines = append(lines, "", help)

	return m.centerOverlay(strings.Join(lines, "\n"))
}
