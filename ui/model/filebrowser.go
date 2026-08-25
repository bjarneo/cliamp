package model

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/fuzzy"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
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
	tracks         []playlist.Track
	replace        bool
	toPlaylist     bool
	targetPlaylist string
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

func (m Model) fbHelpLine() string {
	if m.fileBrowser.searching {
		return m.commandHelp(commandModeFileBrowserSearch)
	}
	return m.commandHelp(commandModeFileBrowser)
}

// fbVisible returns the file-browser list height. The browser renders inline in
// the playlist region, so it shares the playlist's row budget.
func (m *Model) fbVisible() int {
	visible := m.effectivePlaylistVisible()
	if m.fileBrowser.err != "" {
		return max(1, visible-1)
	}
	return visible
}

// fbMaybeAdjustScroll keeps the cursor visible in the current file-browser window.
func (m *Model) fbMaybeAdjustScroll(visible int) {
	clampScroll(&m.fileBrowser.cursor, &m.fileBrowser.scroll, m.fbCount(), visible)
}

// openFileBrowser initialises and shows the file browser overlay.
func (m *Model) openFileBrowser() {
	if m.fileBrowser.dir == "" {
		dir := os.ExpandEnv(m.initialDir)
		if strings.HasPrefix(dir, "~") {
			if home, _ := os.UserHomeDir(); home != "" {
				dir = filepath.Join(home, dir[1:])
			}
		}
		if dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				m.fileBrowser.dir = dir
			}
		}
		if m.fileBrowser.dir == "" {
			m.fileBrowser.dir, _ = os.UserHomeDir()
			if m.fileBrowser.dir == "" {
				m.fileBrowser.dir = "/"
			}
		}
	}
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.err = ""
	m.fileBrowser.searching = false
	m.fileBrowser.search = ""
	m.fileBrowser.filtered = nil
	m.fileBrowser.targetPlaylist = ""
	m.fileBrowser.confirmReplace = false
	m.loadFBDir()
	m.fileBrowser.visible = true
}

func (m *Model) openFileBrowserForPlaylist(name string) {
	m.openFileBrowser()
	m.fileBrowser.targetPlaylist = name
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
				name:  full,
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

// fbUpdateFilter rebuilds the filtered view from the current search query.
// With no query, entries keep their natural directory order; otherwise matches
// are ranked by fuzzy relevance (best match first). The parent ("..") entry is
// never shown while filtering.
func (m *Model) fbUpdateFilter() {
	m.fileBrowser.filtered = nil
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	if m.fileBrowser.search == "" {
		for i, e := range m.fileBrowser.entries {
			if !e.isParent {
				m.fileBrowser.filtered = append(m.fileBrowser.filtered, i)
			}
		}
		return
	}
	type match struct{ idx, score int }
	matches := make([]match, 0, len(m.fileBrowser.entries))
	for i, e := range m.fileBrowser.entries {
		if e.isParent {
			continue
		}
		if score, ok := fuzzy.Match(m.fileBrowser.search, e.name); ok {
			matches = append(matches, match{i, score})
		}
	}
	sort.SliceStable(matches, func(a, b int) bool {
		return matches[a].score > matches[b].score
	})
	for _, mt := range matches {
		m.fileBrowser.filtered = append(m.fileBrowser.filtered, mt.idx)
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
			m.editText("file-browser-search", &m.fileBrowser.search, msg)
			m.fbUpdateFilter()
		} else {
			m.fileBrowser.searching = false
			m.fileBrowser.cursor = m.fileBrowser.savedCursor
			m.fileBrowser.scroll = m.fileBrowser.savedScroll
		}
		return nil
	}

	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("file-browser-search", &m.fileBrowser.search, " ")
		m.fbUpdateFilter()
		return nil
	}
	if m.editText("file-browser-search", &m.fileBrowser.search, msg) {
		m.fbUpdateFilter()
	}
	return nil
}

// handleFileBrowserKey processes key presses while the file browser is open.
func (m *Model) handleFileBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.fileBrowser.searching {
		return m.handleFileBrowserSearchKey(msg)
	}
	if m.fileBrowser.confirmReplace {
		switch msg.String() {
		case "enter":
			m.fileBrowser.confirmReplace = false
			return m.fbConfirm(true)
		case "esc", "R":
			m.fileBrowser.confirmReplace = false
		}
		return nil
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
		if m.fileBrowser.targetPlaylist != "" {
			// Esc acts as "done": commit anything still selected, then
			// refresh the provider pane so track/dir counts update.
			return m.fbCommitAndRefresh()
		}

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

	case "right", "l":
		return m.fbDescend()

	case "enter":
		if len(m.fileBrowser.selected) > 0 {
			if m.fileBrowser.targetPlaylist != "" {
				// Adding to a playlist: refresh the list so track/dir
				// counts update right away.
				return m.fbCommitAndRefresh()
			}
			return m.fbConfirm(false)
		}
		return m.fbDescend()

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
		if len(m.fileBrowser.selected) > 0 && m.fileBrowser.targetPlaylist == "" {
			if m.playlist != nil && m.playlist.Len() > 0 {
				m.fileBrowser.confirmReplace = true
				return nil
			}
			return m.fbConfirm(true)
		}

	case "w":
		if len(m.fileBrowser.selected) > 0 && m.fileBrowser.targetPlaylist == "" {
			return m.fbConfirmToPlaylist()
		}
		if m.fileBrowser.cursor < m.fbCount() && m.fileBrowser.targetPlaylist == "" {
			e := m.fbEntry(m.fileBrowser.cursor)
			if e.isAudio || e.isDir {
				m.fileBrowser.selected[e.path] = true
				return m.fbConfirmToPlaylist()
			}
		}

	case "D":
		// Add the selected directory (or the current directory) as a [[dir]]
		// source on the target playlist instead of expanding it into tracks.
		if m.fileBrowser.targetPlaylist != "" {
			m.fbAddDirSource()
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

// fbCommitAndRefresh confirms the pending selection and re-pulls the provider
// playlist list so dirs/tracks/duration counts reflect the write. Used when
// the browser is targeted at a playlist (Enter confirm or Esc-as-done).
func (m *Model) fbCommitAndRefresh() tea.Cmd {
	cmd := m.fbConfirm(false)
	if cmd == nil {
		// Directory-only commit: the write landed synchronously, so pull
		// playlist counts now. A resolver command refreshes from its
		// fbTracksResolvedMsg handler instead — batching a fetch here
		// would read Playlists() before the tracks are written.
		return m.fetchProviderPlaylists()
	}
	return cmd
}

// fbDescend opens the directory under the cursor (or plays a highlighted
// audio file immediately). Shared by l/→ and Enter. Returns the async
// track-resolution command when the descend confirmed a selection.
func (m *Model) fbDescend() tea.Cmd {
	if m.fileBrowser.cursor >= m.fbCount() {
		return nil
	}
	e := m.fbEntry(m.fileBrowser.cursor)
	if e.isDir {
		cd := m.fileBrowser.dir
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
	return nil
}

// fbConfirm collects selected paths, closes the overlay, and returns an async
// command that resolves the paths into tracks. Paths are emitted in the
// directory listing's natural (alphabetical) order so albums play in track
// order rather than the random map iteration order.
//
// When the browser targets a playlist, selected directories become [[dir]]
// sources instead of being expanded into explicit tracks; only audio files
// are resolved as tracks.
func (m *Model) fbConfirm(replace bool) tea.Cmd {
	paths := make([]string, 0, len(m.fileBrowser.selected))
	for _, e := range m.fileBrowser.entries {
		if m.fileBrowser.selected[e.path] {
			paths = append(paths, e.path)
		}
	}
	m.fileBrowser.visible = false

	target := m.fileBrowser.targetPlaylist
	if target != "" && plMgrVirtualPlaylistName(target) == "" {
		paths = m.fbSplitDirSources(target, paths)
		if len(paths) == 0 {
			return nil
		}
	}

	return func() tea.Msg {
		r, err := resolve.Args(paths)
		if err != nil {
			return err
		}
		return fbTracksResolvedMsg{tracks: r.Tracks, replace: replace, targetPlaylist: target}
	}
}

// fbSplitDirSources adds every directory in paths to target as a [[dir]]
// source and returns the remaining audio-file paths. Paths that are neither an
// entry in the current listing nor directories (e.g. loose files already
// gone from disk) stay untouched for resolution to report.
func (m *Model) fbSplitDirSources(target string, paths []string) []string {
	isDir := make(map[string]bool, len(m.fileBrowser.entries))
	for _, e := range m.fileBrowser.entries {
		isDir[e.path] = e.isDir && !e.isParent
	}
	var dirs, files []string
	for _, p := range paths {
		if isDir[p] {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	if len(dirs) > 0 {
		m.plMgrAddDirSources(target, dirs)
	}
	return files
}

func (m *Model) fbConfirmToPlaylist() tea.Cmd {
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
		return fbTracksResolvedMsg{tracks: r.Tracks, toPlaylist: true}
	}
}
