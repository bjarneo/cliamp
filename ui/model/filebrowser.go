package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	nameLow  string // pre-computed strings.ToLower(name) for search filtering
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

func (m *Model) fbCount() int {
	if m.fileBrowser.searching || m.fileBrowser.search != "" {
		return len(m.fileBrowser.filtered)
	}
	return len(m.fileBrowser.entries)
}

func (m *Model) fbEntry(idx int) fbEntry {
	if m.fileBrowser.searching || m.fileBrowser.search != "" {
		return m.fileBrowser.entries[m.fileBrowser.filtered[idx]]
	}
	return m.fileBrowser.entries[idx]
}

func (m Model) fbHeaderLines() []string {
	header := []string{
		titleStyle.Render("O P E N  F I L E S"),
		dimStyle.Render("  " + m.fileBrowser.dir),
		"",
	}

	if m.fileBrowser.searching {
		cursor := "_"
		if (time.Now().UnixNano()/500000000)%2 != 0 {
			cursor = " "
		}
		header = append(header, playlistSelectedStyle.Render("  / "+m.fileBrowser.search+cursor), "")
	} else if m.fileBrowser.search != "" {
		header = append(header, dimStyle.Render("  / "+m.fileBrowser.search), "")
	}
	return header
}

func (m Model) fbHelpLine() string {
	if m.fileBrowser.searching {
		return helpKey("Enter", "Confirm ") + helpKey("Esc", "Cancel ") + helpKey("Type", "Filter")
	}
	help := helpKey("←↓↑→", "Navigate ") + helpKey("Enter", "Open ") + helpKey("/", "Filter ") +
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
	count := m.fbCount()
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
	m.fileBrowser.searching = false
	m.fileBrowser.search = ""
	m.fileBrowser.filtered = nil
	m.loadFBDir()
	m.fileBrowser.visible = true
}

// loadFBDir reads the current directory and populates fbEntries.
func (m *Model) loadFBDir() {
	m.fileBrowser.err = ""
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	m.fileBrowser.searching = false
	m.fileBrowser.search = ""
	m.fileBrowser.filtered = nil
	clear(m.fileBrowser.selected)

	// Reuse internal memory buffer of m.fileBrowser.entries.
	m.fileBrowser.entries = m.fileBrowser.entries[:0]
	if cap(m.fileBrowser.entries) > 512 {
		// Previous directory list was too large, do not retain memory, re-allocate buffer.
		m.fileBrowser.entries = nil
	}

	m.fileBrowser.entries = append(m.fileBrowser.entries, fbEntry{
		name:     "..",
		nameLow:  "..",
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
			full := name + dirType
			m.fileBrowser.entries = append(m.fileBrowser.entries, fbEntry{
				name:    full,
				nameLow: strings.ToLower(full),
				path:    filepath.Join(m.fileBrowser.dir, name),
				isDir:   true,
			})
		} else {
			if files == nil {
				files = make([]fbEntry, 0, 16) // Avoid reallocations
			}
			files = append(files, fbEntry{
				name:    name,
				nameLow: strings.ToLower(name),
				path:    filepath.Join(m.fileBrowser.dir, name),
				isAudio: player.SupportedExts[strings.ToLower(filepath.Ext(name))],
			})
		}
	}
	m.fileBrowser.entries = append(m.fileBrowser.entries, files...)
}

func (m *Model) fbUpdateFilter() {
	m.fileBrowser.filtered = nil
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	query := strings.ToLower(m.fileBrowser.search)
	for i, e := range m.fileBrowser.entries {
		if e.isParent {
			continue
		}
		if query == "" || strings.Contains(e.nameLow, query) {
			m.fileBrowser.filtered = append(m.fileBrowser.filtered, i)
		}
	}
}

func (m *Model) handleFileBrowserSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.fileBrowser.visible = false
		return m.quit()
	case "esc":
		m.fileBrowser.searching = false
		m.fileBrowser.search = ""
		m.fileBrowser.filtered = nil
		m.fileBrowser.cursor = m.fileBrowser.savedCursor
		m.fileBrowser.scroll = m.fileBrowser.savedScroll
		return nil
	case "enter":
		m.fileBrowser.searching = false
		if m.fileBrowser.search == "" {
			m.fileBrowser.cursor = m.fileBrowser.savedCursor
			m.fileBrowser.scroll = m.fileBrowser.savedScroll
		}
		return nil
	case "down":
		m.fileBrowser.searching = false
		if m.fbCount() > 0 {
			m.fileBrowser.cursor = 0
			m.fbMaybeAdjustScroll(m.fbVisible())
		}
		return nil
	case "backspace":
		if m.fileBrowser.search != "" {
			m.fileBrowser.search = removeLastRune(m.fileBrowser.search)
			m.fbUpdateFilter()
		} else {
			m.fileBrowser.searching = false
			m.fileBrowser.cursor = m.fileBrowser.savedCursor
			m.fileBrowser.scroll = m.fileBrowser.savedScroll
		}
		return nil
	case "space":
		m.fileBrowser.search += " "
		m.fbUpdateFilter()
		return nil
	}

	if len(msg.Text) > 0 {
		m.fileBrowser.search += msg.Text
		m.fbUpdateFilter()
	}
	return nil
}

// handleFileBrowserKey processes key presses while the file browser is open.
func (m *Model) handleFileBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.fileBrowser.searching {
		return m.handleFileBrowserSearchKey(msg)
	}

	var cd string
	switch msg.String() {
	case "ctrl+c":
		m.fileBrowser.visible = false
		return m.quit()

	case "esc", "o", "q":
		m.fileBrowser.visible = false
		m.fileBrowser.searching = false
		m.fileBrowser.search = ""
		m.fileBrowser.filtered = nil

	case "ctrl+x":
		m.toggleExpandedView()
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "/":
		m.fileBrowser.savedCursor = m.fileBrowser.cursor
		m.fileBrowser.savedScroll = m.fileBrowser.scroll
		m.fileBrowser.searching = true
		m.fileBrowser.search = ""
		m.fbUpdateFilter()
		return nil

	case "up", "k":
		if m.fileBrowser.search != "" && m.fileBrowser.cursor == 0 {
			m.fileBrowser.searching = true
			return nil
		}
		count := m.fbCount()
		if m.fileBrowser.cursor > 0 {
			m.fileBrowser.cursor--
		} else if count > 0 {
			m.fileBrowser.cursor = count - 1
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "down", "j":
		count := m.fbCount()
		if m.fileBrowser.cursor < count-1 {
			m.fileBrowser.cursor++
		} else if count > 0 {
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
		count := m.fbCount()
		if m.fileBrowser.cursor < count-1 {
			visible := m.fbVisible()
			m.fileBrowser.cursor = min(count-1, m.fileBrowser.cursor+visible)
			m.fbMaybeAdjustScroll(visible)
		}

	case "enter", "right", "l":
		if len(m.fileBrowser.selected) > 0 {
			return m.fbConfirm(false)
		}
		if m.fileBrowser.cursor < m.fbCount() {
			e := m.fbEntry(m.fileBrowser.cursor)
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

	case "backspace", "left", "h":
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
		count := m.fbCount()
		if m.fileBrowser.cursor < count {
			e := m.fbEntry(m.fileBrowser.cursor)
			if !e.isParent && (e.isAudio || e.isDir) {
				if m.fileBrowser.selected[e.path] {
					delete(m.fileBrowser.selected, e.path)
				} else {
					m.fileBrowser.selected[e.path] = true
				}
			}
			if m.fileBrowser.cursor < count-1 {
				m.fileBrowser.cursor++
			}
		}
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "a":
		// Toggle select all audio files in current view.
		var selectAll bool
		count := m.fbCount()
		for i := 0; i < count; i++ {
			e := m.fbEntry(i)
			// If we found at least one unselected file then all files should be selected:
			// set selectAll flag and skip checking selection of remaining files.
			if e.isAudio && (selectAll || !m.fileBrowser.selected[e.path]) {
				selectAll, m.fileBrowser.selected[e.path] = true, true
			}
		}
		if !selectAll {
			// All files in current view selected: clear selection for them.
			for i := 0; i < count; i++ {
				e := m.fbEntry(i)
				if e.isAudio {
					delete(m.fileBrowser.selected, e.path)
				}
			}
		}

	case "home", "g":
		m.fileBrowser.cursor = 0
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "end", "G":
		count := m.fbCount()
		if count > 0 {
			m.fileBrowser.cursor = count - 1
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
// command that resolves the paths into tracks. Paths are emitted in the
// directory listing's natural (alphabetical) order so albums play in track
// order rather than the random map iteration order.
func (m *Model) fbConfirm(replace bool) tea.Cmd {
	paths := make([]string, 0, len(m.fileBrowser.selected))
	for _, e := range m.fileBrowser.entries {
		if m.fileBrowser.selected[e.path] {
			paths = append(paths, e.path)
		}
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

	count := m.fbCount()

	if count == 0 {
		if m.fileBrowser.search != "" {
			lines = append(lines, dimStyle.Render("  No matches"))
		} else {
			lines = append(lines, dimStyle.Render("  (empty)"))
		}
		rendered = 1
	} else {
		scroll := max(m.fileBrowser.scroll, 0)
		if scroll > count-1 {
			scroll = max(0, count-1)
		}

		for i := scroll; i < count && i < scroll+maxVisible; i++ {
			e := m.fbEntry(i)

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
			label = truncate(label, max(1, ui.PanelWidth-2))

			if m.fileBrowser.searching {
				lines = append(lines, dimStyle.Render("  "+label))
			} else if i == m.fileBrowser.cursor {
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
