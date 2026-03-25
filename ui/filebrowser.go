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
	if m.fileBrowser.dir == "" {
		m.fileBrowser.dir, _ = os.UserHomeDir()
		if m.fileBrowser.dir == "" {
			m.fileBrowser.dir = "/"
		}
	}
	m.fileBrowser.cursor = 0
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.err = ""
	m.loadFBDir()
	m.fileBrowser.visible = true
}

// loadFBDir reads the current directory and populates fbEntries.
func (m *Model) loadFBDir() {
	m.fileBrowser.err = ""
	m.fileBrowser.cursor = 0

	// Reuse internal memory buffer of m.fileBrowser.entries.
	m.fileBrowser.entries = m.fileBrowser.entries[:0]
	if cap(m.fileBrowser.entries) > 512 {
		// Previous directory list was too large, do not retain memory, re-allocate buffer.
		m.fileBrowser.entries = nil
	}

	// Always provide a parent entry for navigating up.
	m.fileBrowser.entries = append(m.fileBrowser.entries, fbEntry{
		name:     "..",
		path:     filepath.Dir(m.fileBrowser.dir),
		isDir:    true,
		isParent: true,
	})

	// Get entries sorted by name, dirs and files mixed
	entries, err := os.ReadDir(m.fileBrowser.dir)
	if err != nil {
		m.fileBrowser.err = err.Error()
		return
	}

	// Add directories to m.fileBrowser.entries (reuse internal memory),
	// add files to files, then append all files to m.fileBrowser.entries, skip dotfiles.
	var files []fbEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Detect directories and directory-like entries.
		dirType := "" // Name suffix for directories and some non-regular file types.
		if e.IsDir() {
			dirType = "/"
		} else if !e.Type().IsRegular() {
			if e.Type()&os.ModeSymlink != 0 && !player.SupportedExts[strings.ToLower(filepath.Ext(name))] {
				// Treat symlink as a directory unless it points to media file.
				// os.DirEntry has no option to test the type of object symlink points to.
				dirType = "@"
			} else if os.PathSeparator == '\\' && e.Type()&os.ModeIrregular != 0 {
				// Try to support directory junctions on Windows (mklink /J).
				// Go do not support such files, it treats them as os.ModeIrregular (?---------).
				dirType = "?"
			}
		}
		// Add entry to m.fileBrowser.entries or to files slice
		if dirType != "" {
			m.fileBrowser.entries = append(m.fileBrowser.entries, fbEntry{
				name:  name + dirType,
				path:  filepath.Join(m.fileBrowser.dir, name),
				isDir: true,
			})
		} else {
			if files == nil {
				files = make([]fbEntry, 0, 16) // Avoid reallocations
			}
			files = append(files, fbEntry{
				name:    name,
				path:    filepath.Join(m.fileBrowser.dir, name),
				isAudio: player.SupportedExts[strings.ToLower(filepath.Ext(name))],
			})
		}
	}
	m.fileBrowser.entries = append(m.fileBrowser.entries, files...)
}

// handleFileBrowserKey processes key presses while the file browser is open.
func (m *Model) handleFileBrowserKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.fileBrowser.visible = false
		return m.quit()

	case "esc", "o", "q":
		m.fileBrowser.visible = false

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

	case "pgup", "ctrl+u":
		if m.fileBrowser.cursor > 0 {
			m.fileBrowser.cursor -= min(m.fileBrowser.cursor, 12)
		}

	case "pgdown", "ctrl+d":
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

	case "~":
		cd, _ := os.UserHomeDir()
		if cd != "" && m.fileBrowser.dir != cd {
			m.fileBrowser.dir = cd
			m.loadFBDir()
		}

	case ".":
		cd, _ := os.Getwd()
		if cd != "" && m.fileBrowser.dir != cd {
			m.fileBrowser.dir = cd
			m.loadFBDir()
		}

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
			if m.fileBrowser.cursor < len(m.fileBrowser.entries)-1 {
				m.fileBrowser.cursor++
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

	help := helpKey("↑↓", "Scroll ") + helpKey("Enter", "Open ") + 
		helpKey("Spc", "Select ") + helpKey("a", "All ") +
		helpKey("←", "Back ") + helpKey("~.", "Home/Cwd ")
	if len(m.fileBrowser.selected) > 0 {
		help += helpKey("R", "Replace ")
	}
	help += helpKey("Esc", "Close")
	lines = append(lines, "", help)

	return m.centerOverlay(strings.Join(lines, "\n"))
}
