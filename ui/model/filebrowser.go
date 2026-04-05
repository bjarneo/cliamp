package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
	"cliamp/ui"
)

// fbMaxVisible is a number of visible entries in file browser.
const fbMaxVisible = 12

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

func (m Model) fbHeaderLines() []string {
	return []string{
		titleStyle.Render("O P E N  F I L E S"),
		dimStyle.Render("  " + m.fileBrowser.dir),
		"",
	}
}

func (m Model) fbHelpLine() string {
	help := helpKey("↑↓", "Scroll ") + helpKey("Enter", "Open ") +
		helpKey("Spc", "Select ") + helpKey("a", "All ") +
		helpKey("←", "Back ") + helpKey("~.", "Home/Cwd ")
	if os.PathSeparator == '\\' {
		help += helpKey("AltCZ", "Drive ")
	}
	if len(m.fileBrowser.selected) > 0 {
		help += helpKey("R", "Replace ")
	}
	help += helpKey("Esc", "Close")
	return help
}

// fbVisible returns the current file-browser list height accounting for
// frame padding and all fixed (non-list) sections.
func (m *Model) fbVisible() int {
	probeSections := append([]string{}, m.fbHeaderLines()...)
	if m.fileBrowser.err != "" {
		probeSections = append(probeSections, errorStyle.Render("  "+m.fileBrowser.err))
	}

	// 1-line list placeholder.
	probeSections = append(probeSections, "x")

	// Footer area must mirror renderFileBrowser().
	if len(m.fileBrowser.selected) > 0 {
		probeSections = append(probeSections, "", statusStyle.Render("  1 selected"))
	} else {
		probeSections = append(probeSections, "")
		if m.fileBrowser.err == "" {
			probeSections = append(probeSections, "")
		}
	}
	probeSections = append(probeSections, "", m.fbHelpLine())

	probeFrame := ui.FrameStyle.Render(strings.Join(probeSections, "\n"))
	fixedHeight := lipgloss.Height(probeFrame) - 1

	limit := fbMaxVisible
	if m.heightExpanded {
		limit = m.height
	}
	return max(3, min(limit, m.height-fixedHeight))
}

// fbMaybeAdjustScroll keeps the cursor visible in the current file-browser window.
func (m *Model) fbMaybeAdjustScroll(visible int) {
	if visible <= 0 {
		return
	}
	count := len(m.fileBrowser.entries)
	if m.fileBrowser.cursor < 0 {
		m.fileBrowser.cursor = 0
	}
	if m.fileBrowser.cursor >= count && count > 0 {
		m.fileBrowser.cursor = count - 1
	}

	if m.fileBrowser.cursor < m.fileBrowser.scroll {
		m.fileBrowser.scroll = m.fileBrowser.cursor
	} else if m.fileBrowser.cursor >= m.fileBrowser.scroll+visible {
		m.fileBrowser.scroll = m.fileBrowser.cursor - visible + 1
	}

	if m.fileBrowser.scroll+visible > count {
		m.fileBrowser.scroll = max(0, count-visible)
	}
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
	m.fileBrowser.scroll = 0
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.err = ""
	m.loadFBDir()
	m.fileBrowser.visible = true
}

// loadFBDir reads the current directory and populates fbEntries.
func (m *Model) loadFBDir() {
	m.fileBrowser.err = ""
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	clear(m.fileBrowser.selected)

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
func (m *Model) handleFileBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	var cd string
	switch msg.String() {
	case "ctrl+c":
		m.fileBrowser.visible = false
		return m.quit()

	case "esc", "o", "q":
		m.fileBrowser.visible = false

	case "ctrl+x":
		m.toggleExpandPlaylist()
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "up", "k":
		if m.fileBrowser.cursor > 0 {
			m.fileBrowser.cursor--
		} else if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = len(m.fileBrowser.entries) - 1
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "down", "j":
		if m.fileBrowser.cursor < len(m.fileBrowser.entries)-1 {
			m.fileBrowser.cursor++
		} else if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = 0
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "pgup", "ctrl+u":
		if m.fileBrowser.cursor > 0 {
			visible := m.fbVisible()
			m.fileBrowser.cursor -= min(m.fileBrowser.cursor, visible)
			m.fbMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		if m.fileBrowser.cursor < len(m.fileBrowser.entries)-1 {
			visible := m.fbVisible()
			m.fileBrowser.cursor = min(len(m.fileBrowser.entries)-1, m.fileBrowser.cursor+visible)
			m.fbMaybeAdjustScroll(visible)
		}

	case "enter", "l", "right":
		if len(m.fileBrowser.selected) > 0 {
			return m.fbConfirm(false)
		}
		if m.fileBrowser.cursor < len(m.fileBrowser.entries) {
			e := m.fileBrowser.entries[m.fileBrowser.cursor]
			if e.isDir {
				cd = m.fileBrowser.dir
				m.fileBrowser.dir = e.path
				m.loadFBDir()
				if e.name == ".." {
					// cd .. and reveal previous directory name in list
					for i := range m.fileBrowser.entries {
						if m.fileBrowser.entries[i].path == cd {
							m.fileBrowser.cursor = i
							break
						}
					}
					m.fbMaybeAdjustScroll(m.fbVisible())
				}
			} else if e.isAudio {
				m.fileBrowser.selected[e.path] = true
				return m.fbConfirm(false)
			}
		}

	case "backspace", "h", "left":
		cd = m.fileBrowser.dir
		m.fileBrowser.dir = filepath.Dir(m.fileBrowser.dir)
		m.loadFBDir()
		// Reveal previous directory name in list
		for i := range m.fileBrowser.entries {
			if m.fileBrowser.entries[i].path == cd {
				m.fileBrowser.cursor = i
				break
			}
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "~":
		if cd, _ = os.UserHomeDir(); cd != "" && m.fileBrowser.dir != cd {
			m.fileBrowser.dir = cd
			m.loadFBDir()
		}

	case ".":
		if cd, _ = os.Getwd(); cd != "" && m.fileBrowser.dir != cd {
			m.fileBrowser.dir = cd
			m.loadFBDir()
		}

	case "space":
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
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "a":
		// Toggle select all audio files in current view.
		var selectAll bool
		for _, e := range m.fileBrowser.entries {
			// If we found at least one unselected file then all files should be selected:
			// set selectAll flag and skip checking selection of remaining files.
			if e.isAudio && (selectAll || !m.fileBrowser.selected[e.path]) {
				selectAll, m.fileBrowser.selected[e.path] = true, true
			}
		}
		if !selectAll {
			// All files selected (no unselected files found): clear selection for all
			clear(m.fileBrowser.selected)
		}

	case "g", "home":
		m.fileBrowser.cursor = 0
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "G", "end":
		if len(m.fileBrowser.entries) > 0 {
			m.fileBrowser.cursor = len(m.fileBrowser.entries) - 1
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "R":
		if len(m.fileBrowser.selected) > 0 {
			return m.fbConfirm(true)
		}
	}

	// Change drive letter on Windows by pressing alt+[c..z]
	if os.PathSeparator == '\\' {
		if cd = msg.String(); len(cd) == 5 && strings.HasPrefix(cd, "alt+") && 'c' <= cd[4] && cd[4] <= 'z' {
			cd = strings.ToUpper(cd[4:]) + ":\\"
			m.fileBrowser.dir = cd
			m.loadFBDir()
		}
	}

	return nil
}

// fbConfirm collects selected paths, closes the overlay, and returns an async
// command that resolves the paths into tracks.
func (m *Model) fbConfirm(replace bool) tea.Cmd {
	paths := make([]string, 0, len(m.fileBrowser.selected))
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
	maxVisible := m.fbVisible()
	lines := append(make([]string, 0, 3+maxVisible+4), m.fbHeaderLines()...)

	if m.fileBrowser.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.fileBrowser.err))
	}

	rendered := 0

	if len(m.fileBrowser.entries) == 0 {
		lines = append(lines, dimStyle.Render("  (empty)"))
		rendered = 1
	} else {
		scroll := m.fileBrowser.scroll
		if scroll < 0 {
			scroll = 0
		}
		if scroll > len(m.fileBrowser.entries)-1 {
			scroll = max(0, len(m.fileBrowser.entries)-1)
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
			maxW := max(1, ui.PanelWidth-2)
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
	for i := 0; i < maxVisible-rendered; i++ {
		lines = append(lines, "")
	}

	// Selection count.
	if len(m.fileBrowser.selected) > 0 {
		lines = append(lines, "", statusStyle.Render(fmt.Sprintf("  %d selected", len(m.fileBrowser.selected))))
	} else {
		lines = append(lines, "")
		// Keep footer alignment consistent when no error/status line is present.
		if m.fileBrowser.err == "" {
			lines = append(lines, "")
		}
	}

	lines = append(lines, "", m.fbHelpLine())

	return m.centerOverlay(strings.Join(lines, "\n"))
}
