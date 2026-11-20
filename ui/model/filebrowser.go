package model

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/fuzzy"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// fbTargetPurpose distinguishes playlist-targeted file browsers.
type fbTargetPurpose uint8

const (
	fbTargetNone fbTargetPurpose = iota
	fbTargetAddFiles
	fbTargetAddDirSource
)

// fbEntry is a single item in the file browser listing.
type fbEntry struct {
	name     string // label shown in the list, relative to the browsed directory in an index row
	base     string // file name without the parent path; empty means name
	path     string
	isDir    bool
	isAudio  bool
	isParent bool
}

// matchName returns the file name used for matching and display priority.
func (e fbEntry) matchName() string {
	if e.base != "" {
		return e.base
	}
	return e.name
}

// fbTracksResolvedMsg carries tracks resolved from file browser selections.
type fbTracksResolvedMsg struct {
	tracks         []playlist.Track
	replace        bool
	toPlaylist     bool
	targetPlaylist string
	queue          uint64 // the queue generation when a replace started
	err            error
}

// fbIndexMsg carries the recursive listing of a directory for the file
// browser filter. gen guards against an index that arrives after the browser
// moved to another directory.
type fbIndexMsg struct {
	gen     uint64
	dir     string
	entries []fbEntry
	err     error
}

// fbSource returns the rows the browser draws from: the recursive index while
// a query is active, and the current directory's listing otherwise.
func (m *Model) fbSource() []fbEntry {
	if m.fileBrowser.usesIndex() {
		return m.fileBrowser.index
	}
	return m.fileBrowser.entries
}

func (m *Model) fbCount() int {
	return m.fileBrowser.viewCount(len(m.fbSource()))
}

func (m *Model) fbEntry(idx int) fbEntry {
	rows := m.fbSource()
	if m.fileBrowser.isFiltered() {
		if idx < 0 || idx >= len(m.fileBrowser.filtered) {
			return fbEntry{}
		}
		raw := m.fileBrowser.filtered[idx]
		if raw < 0 || raw >= len(rows) {
			return fbEntry{}
		}
		return rows[raw]
	}
	if idx < 0 || idx >= len(rows) {
		return fbEntry{}
	}
	return rows[idx]
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
	m.fileBrowser.filterList = filterList{}
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.err = ""
	m.fileBrowser.targetPlaylist = ""
	m.fileBrowser.targetPurpose = fbTargetNone
	m.fileBrowser.confirmReplace = false
	m.fileBrowser.resumeLocal = m.showingLocalProvider()
	m.loadFBDir()
	m.fileBrowser.visible = true
}

func (m *Model) openFileBrowserForPlaylist(name string, purpose fbTargetPurpose) {
	m.openFileBrowser()
	m.fileBrowser.targetPlaylist = name
	m.fileBrowser.targetPurpose = purpose
	m.fileBrowser.resumeLocal = false
}

// restoreLocalFileBrowser reopens the Local file browser at the directory
// and cursor left behind when playback closed it.
func (m *Model) restoreLocalFileBrowser() {
	cursor, scroll := m.fileBrowser.cursor, m.fileBrowser.scroll
	m.noteProviderPaneVisit()
	m.focus = focusProvider
	m.openFileBrowser()
	m.fileBrowser.cursor = cursor
	m.fileBrowser.scroll = scroll
	m.fbMaybeAdjustScroll(m.fbVisible())
}

// loadFBDir reads the current directory and populates fbEntries.
func (m *Model) loadFBDir() {
	m.fileBrowser.err = ""
	m.fileBrowser.filterList = filterList{}
	// The recursive index is rooted at the directory that was being listed;
	// relative paths in it no longer apply once the browser moves.
	m.fileBrowser.index = nil
	m.fileBrowser.indexDir = ""
	m.fileBrowser.indexLoading = false
	nextRequest(&m.fileBrowser.indexGen)
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
			if e.Type()&os.ModeSymlink != 0 && !playlist.IsAudioFile(name) {
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
				isAudio: playlist.IsAudioFile(name),
			})
		}
	}
	m.fileBrowser.entries = append(m.fileBrowser.entries, files...)
}

// fbUpdateFilter rebuilds the filtered view from the current search query.
// With no query, rows keep their natural directory order. Otherwise the
// recursive index answers, so a name in a subfolder matches too, and results
// come grouped the way a file manager shows them: folders first, then files,
// each group ranked by fuzzy relevance.
func (m *Model) fbUpdateFilter() {
	m.fileBrowser.filtered = nil
	m.fileBrowser.cursor = 0
	m.fileBrowser.scroll = 0
	rows := m.fbSource()
	if m.fileBrowser.filter == "" {
		for i, e := range rows {
			if !e.isParent {
				m.fileBrowser.filtered = append(m.fileBrowser.filtered, i)
			}
		}
		return
	}
	type match struct {
		idx   int
		score int
	}
	matches := make([]match, 0, len(rows))
	substring := m.fileBrowser.usesIndex()
	for i, e := range rows {
		if e.isParent {
			continue
		}
		if score, ok := fbMatchScore(m.fileBrowser.filter, e, substring); ok {
			matches = append(matches, match{i, score})
		}
	}
	// Folders first, so the directories that hold a match stay reachable
	// while the query is active. Stable, so equal scores keep path order.
	sort.SliceStable(matches, func(a, b int) bool {
		aDir, bDir := rows[matches[a].idx].isDir, rows[matches[b].idx].isDir
		if aDir != bDir {
			return aDir
		}
		return matches[a].score > matches[b].score
	})
	for _, mt := range matches {
		m.fileBrowser.filtered = append(m.fileBrowser.filtered, mt.idx)
	}
}

// startFBIndex builds the recursive index of the browsed directory in the
// background. The filter searches it, so a query reaches subfolders instead of
// only the listing on screen. A directory already indexed is reused.
func (m *Model) startFBIndex() tea.Cmd {
	dir := m.fileBrowser.dir
	if dir == "" {
		return nil
	}
	if m.fileBrowser.indexDir == dir && len(m.fileBrowser.index) > 0 {
		return nil
	}
	gen := nextRequest(&m.fileBrowser.indexGen)
	m.fileBrowser.indexLoading = true
	return func() tea.Msg {
		entries, err := indexDirRecursive(dir)
		return fbIndexMsg{gen: gen, dir: dir, entries: entries, err: err}
	}
}

// fbIndexMaxEntries bounds the recursive index, so a query in a home directory
// full of source trees cannot spend the UI loop's time on hundreds of
// thousands of rows. It is far above a music library.
const fbIndexMaxEntries = 200_000

// fbMatchScore matches query against the entry's own name, as a file manager's
// search does: a file inside a matching folder is only listed when its own name
// matches too, so a query on a folder name does not return everything below it.
//
// The directory listing keeps the fuzzy matcher it has always used. The
// recursive index uses a substring test instead, because a subsequence matcher
// across a whole library turns a short query into hundreds of weak hits:
// "intro" alone reaches every "Tranquility" album below it.
func fbMatchScore(query string, e fbEntry, substring bool) (int, bool) {
	if e.isParent {
		return 0, false
	}
	name := e.matchName()
	if !substring {
		return fuzzy.Match(query, name)
	}
	return fbSubstringScore(query, name)
}

// fbSubstringScore reports whether query is a case-insensitive substring of
// name, scored so a prefix ranks first, then a word boundary, then the rest,
// with an earlier match ahead of a later one.
func fbSubstringScore(query, name string) (int, bool) {
	q := strings.ToLower(query)
	n := strings.ToLower(name)
	at := strings.Index(n, q)
	if at < 0 {
		return 0, false
	}
	score := len(q) * 4
	switch {
	case at == 0:
		score += 40
	case at > 0 && (n[at-1] == ' ' || n[at-1] == '-' || n[at-1] == '_' || n[at-1] == '.' || n[at-1] == '(' || n[at-1] == '['):
		score += 20
	}
	// Earlier matches win ties, and a shorter name wins over a longer one.
	score -= min(at, 10)
	score -= min(len(n)/16, 5)
	return score, true
}

// indexDirRecursive lists every entry below root, with names relative to root
// so the filter can show where a match lives. Dot files and dot directories are
// skipped, as in the directory listing, and so are unreadable subtrees.
func indexDirRecursive(root string) ([]fbEntry, error) {
	var entries []fbEntry
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// A permission error on one subtree must not lose the rest.
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if p != root && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		isDir := d.IsDir()
		display := rel
		if isDir {
			display += "/"
		}
		entries = append(entries, fbEntry{
			name:    display,
			base:    name,
			path:    p,
			isDir:   isDir,
			isAudio: !isDir && playlist.IsAudioFile(name),
		})
		if len(entries) >= fbIndexMaxEntries {
			return fs.SkipAll
		}
		return nil
	})
	return entries, err
}

// fbSelectedInOrder returns the selected paths in listing order, followed by
// any selection that the current view no longer holds, so an index result and
// the directory listing agree on playback order.
func (m *Model) fbSelectedInOrder() []string {
	paths := make([]string, 0, len(m.fileBrowser.selected))
	seen := make(map[string]struct{}, len(m.fileBrowser.selected))
	for _, e := range m.fbSource() {
		if m.fileBrowser.selected[e.path] {
			paths = append(paths, e.path)
			seen[e.path] = struct{}{}
		}
	}
	if len(seen) == len(m.fileBrowser.selected) {
		return paths
	}
	rest := make([]string, 0, len(m.fileBrowser.selected)-len(seen))
	for p := range m.fileBrowser.selected {
		if _, ok := seen[p]; !ok {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	return append(paths, rest...)
}

func (m *Model) handleFileBrowserSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.filterKey(&m.fileBrowser.filterList, "file-browser-search", msg, len(m.fbSource()), m.fbUpdateFilter) {
		m.fbMaybeAdjustScroll(m.fbVisible())
	}
	return nil
}

// handleFileBrowserKey processes key presses while the file browser is open.
func (m *Model) handleFileBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.fileBrowser.filtering {
		return m.handleFileBrowserSearchKey(msg)
	}
	if m.fileBrowser.confirmReplace {
		switch msg.String() {
		case "enter":
			m.fileBrowser.confirmReplace = false
			return m.fbConfirm(true)
		case "esc", "R":
			m.fileBrowser.confirmReplace = false
		default:
			m.status.Show("Enter confirms replace, Esc cancels.", statusTTLShort)
		}
		return nil
	}

	key := msg.String()
	if (key == "up" || key == "k") && m.fileBrowser.filter != "" && m.fileBrowser.cursor == 0 {
		m.fileBrowser.filtering = true
		return nil
	}
	if visible := m.fbVisible(); stepListCursor(key, &m.fileBrowser.cursor, m.fbCount(), visible) {
		m.fbMaybeAdjustScroll(visible)
		return nil
	}
	var cd string
	switch key {
	case "p":
		if m.localProvider == nil {
			break
		}
		m.fileBrowser.visible = false
		m.fileBrowser.resumeLocal = false
		m.fileBrowser.clearFilter()
		m.openPlaylistManager()

	case "esc", "o", "q":
		m.fileBrowser.visible = false
		m.fileBrowser.resumeLocal = false
		m.fileBrowser.clearFilter()
		if m.fileBrowser.targetPlaylist != "" {
			// Esc acts as "done": commit anything still selected, then
			// refresh the provider pane so track/dir counts update.
			return m.fbCommitAndRefresh()
		}

	case "ctrl+x":
		m.toggleExpandedView()
		m.fbMaybeAdjustScroll(m.fbVisible())

	case "/", "ctrl+f":
		m.fileBrowser.beginFilter()
		m.fbUpdateFilter()
		return m.startFBIndex()

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

	case "R":
		if m.fileBrowser.targetPlaylist != "" {
			return nil
		}
		if len(m.fileBrowser.selected) == 0 {
			m.status.Warning("Select files first (Space).", statusTTLDefault)
			return nil
		}
		if m.playlist != nil && m.playlist.Len() > 0 {
			m.fileBrowser.confirmReplace = true
			return nil
		}
		return m.fbConfirm(true)

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
	paths := m.fbSelectedInOrder()
	m.fileBrowser.visible = false
	if m.fileBrowser.targetPlaylist != "" {
		m.fileBrowser.resumeLocal = false
	}

	target := m.fileBrowser.targetPlaylist
	if target != "" && plMgrVirtualPlaylistName(target) == "" {
		paths = m.fbSplitDirSources(target, paths)
		if len(paths) == 0 {
			return nil
		}
	}

	var queue uint64
	if replace {
		queue = nextRequest(&m.requests.queue)
	}
	return func() tea.Msg {
		r, err := resolve.Args(paths)
		if err != nil {
			return fbTracksResolvedMsg{err: err, replace: replace, queue: queue}
		}
		return fbTracksResolvedMsg{tracks: r.Tracks, replace: replace, targetPlaylist: target, queue: queue}
	}
}

// fbSplitDirSources adds every directory in paths to target as a [[dir]]
// source and returns the remaining audio-file paths. Paths that are neither an
// entry in the current listing nor directories (e.g. loose files already
// gone from disk) stay untouched for resolution to report.
func (m *Model) fbSplitDirSources(target string, paths []string) []string {
	isDir := make(map[string]bool, len(m.fileBrowser.selected))
	for _, e := range m.fbSource() {
		isDir[e.path] = e.isDir && !e.isParent
	}
	// A selection can outlive the view that made it (an index result, then a
	// step back to the listing), so fall back to the filesystem for the rest.
	for _, p := range paths {
		if _, ok := isDir[p]; ok {
			continue
		}
		if info, err := os.Stat(p); err == nil {
			isDir[p] = info.IsDir()
		}
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
	paths := m.fbSelectedInOrder()
	m.fileBrowser.visible = false
	m.fileBrowser.resumeLocal = false

	return func() tea.Msg {
		r, err := resolve.Args(paths)
		if err != nil {
			return fbTracksResolvedMsg{err: err}
		}
		return fbTracksResolvedMsg{tracks: r.Tracks, toPlaylist: true}
	}
}
