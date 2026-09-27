package model

// track_menu.go implements the track context menu — opened by right-click
// on a track row or by `;` on the highlighted track — plus the actions its
// items invoke: song radio, go to album/artist, remove, and the credits
// viewer.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// trackRadioLimit is how many recommendations a song-radio open asks for.
// The seed itself is kept at the head so the queue starts where the user
// pointed, then plays like any replaced queue.
const trackRadioLimit = 30

// trackMenuItem is one menu row. The accelerator is a live key inside the
// menu and doubles as the hotkey documented in docs/keybindings.md.
type trackMenuItem struct {
	key   string
	label string
	run   func(*Model) tea.Cmd
}

// trackMenuItems builds the item list for the menu's target track. Items
// whose capability is provider-dependent (radio, album, artist) or
// view-dependent (remove) only appear when they can act.
func (m Model) trackMenuItems() []trackMenuItem {
	tm := &m.trackMenu
	t := tm.track
	items := make([]trackMenuItem, 0, 7)
	items = append(items, trackMenuItem{"w", "Add to playlist", func(m *Model) tea.Cmd {
		return m.openPlaylistPicker([]playlist.Track{t}, "Track: "+t.DisplayName())
	}})
	if rec, _ := m.recommenderForTrack(t); rec != nil {
		items = append(items, trackMenuItem{"r", "Go to song radio", func(m *Model) tea.Cmd {
			return m.startTrackRadio(t)
		}})
	}
	if tm.remove != menuRemoveQueue {
		items = append(items, trackMenuItem{"a", "Add to queue", func(m *Model) tea.Cmd {
			return m.menuQueueTrack()
		}})
	}
	if t.Album != "" {
		items = append(items, trackMenuItem{"l", "Go to album", func(m *Model) tea.Cmd {
			return m.goToTrackAlbum(t)
		}})
	}
	if t.Artist != "" {
		items = append(items, trackMenuItem{"t", "Go to artist", func(m *Model) tea.Cmd {
			return m.goToTrackArtist(t)
		}})
	}
	switch tm.remove {
	case menuRemovePlaylist, menuRemovePlMgr:
		items = append(items, trackMenuItem{"x", "Remove from this playlist", func(m *Model) tea.Cmd {
			return m.menuRemoveTrack()
		}})
	case menuRemoveQueue:
		items = append(items, trackMenuItem{"x", "Remove from queue", func(m *Model) tea.Cmd {
			return m.menuRemoveTrack()
		}})
	}
	items = append(items, trackMenuItem{"i", "View credits", func(m *Model) tea.Cmd {
		m.openCredits(t)
		return nil
	}})
	return items
}

// openTrackMenuAt opens the menu for the track under body row/col, moving
// the surface's cursor to the clicked row first so remove/add act there.
func (m *Model) openTrackMenuAt(row, col int) tea.Cmd {
	hit := m.hitTrack(row, col)
	if hit.cursor < 0 {
		return nil
	}
	m.selectTrackRow(row, col)
	m.openTrackMenu(hit.track, hit.remove, hit.removeIdx)
	return nil
}

// openTrackMenu opens the menu for a resolved track.
func (m *Model) openTrackMenu(t playlist.Track, remove menuRemoveKind, removeIdx int) {
	m.trackMenu = trackMenuState{
		visible:   true,
		track:     t,
		remove:    remove,
		removeIdx: removeIdx,
	}
}

// handleTrackHotkeys is the keyboard side of the menu: `;` opens it and the
// same actions exist as direct keys on the highlighted track. It returns
// handled=false when no track is highlighted (text inputs and non-track
// surfaces keep their keys).
func (m *Model) handleTrackHotkeys(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	t, remove, removeIdx, ok := m.hotkeyTrack()
	if !ok {
		return nil, false
	}
	switch msg.String() {
	case ";":
		m.openTrackMenu(t, remove, removeIdx)
		return nil, true
	case "W":
		return m.startTrackRadio(t), true
	case "ctrl+a":
		return m.goToTrackAlbum(t), true
	case "ctrl+t":
		return m.goToTrackArtist(t), true
	}
	return nil, false
}

// hotkeyTrack returns the highlighted track for the hotkey path, or
// ok=false on screens where a live text input owns the keyboard (the
// playlist filter, the nav-browser filter, the Home filter, the manager
// filter) or where nothing track-like is under the cursor.
func (m Model) hotkeyTrack() (playlist.Track, menuRemoveKind, int, bool) {
	if m.playlist == nil || m.search.active || m.navBrowser.searching || m.home.filtering || m.plManager.filtering {
		return playlist.Track{}, 0, 0, false
	}
	t, remove, idx := m.highlightedTrack()
	if t.Path == "" && t.Title == "" {
		return playlist.Track{}, 0, 0, false
	}
	return t, remove, idx, true
}

// handleTrackMenuKey runs the menu overlay: arrows move, accelerators and
// Enter run the item, Esc (or a second right-click outside) closes.
func (m *Model) handleTrackMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	items := m.trackMenuItems()
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", ";", "q":
		m.trackMenu.visible = false
		return nil
	case "up", "k", "ctrl+p":
		if m.trackMenu.cursor > 0 {
			m.trackMenu.cursor--
		} else if len(items) > 0 {
			m.trackMenu.cursor = len(items) - 1
		}
	case "down", "j", "ctrl+n":
		if m.trackMenu.cursor < len(items)-1 {
			m.trackMenu.cursor++
		} else {
			m.trackMenu.cursor = 0
		}
	case "enter":
		if m.trackMenu.cursor < len(items) {
			m.trackMenu.visible = false
			return items[m.trackMenu.cursor].run(m)
		}
	default:
		for _, item := range items {
			if item.key == msg.String() {
				m.trackMenu.visible = false
				return item.run(m)
			}
		}
	}
	return nil
}

// — menu actions —

// menuQueueTrack mirrors 'a' on a playlist row (toggle its queue slot) or
// 'q' elsewhere (append and queue next).
func (m *Model) menuQueueTrack() tea.Cmd {
	tm := &m.trackMenu
	if tm.remove == menuRemovePlaylist && tm.removeIdx < m.playlist.Len() {
		if !m.playlist.Dequeue(tm.removeIdx) {
			m.playlist.Queue(tm.removeIdx)
		}
		m.normalizeQueueOverlay()
		return m.rearmPreload()
	}
	return m.queueTrackNext(tm.track)
}

// menuRemoveTrack applies the surface's own removal path to the menu row:
// the playlist (remote then local), the play-next queue, or the playlist
// manager's open playlist.
func (m *Model) menuRemoveTrack() tea.Cmd {
	switch m.trackMenu.remove {
	case menuRemovePlaylist:
		m.plCursor = m.trackMenu.removeIdx
		if cmd, handled := m.removeSelectedRemote(); handled {
			return cmd
		}
		m.removeSelectedFromPlaylist()
	case menuRemoveQueue:
		m.playlistUndo = playlistUndo{active: true, snapshot: m.playlist.Snapshot()}
		m.playlist.RemoveQueueAt(m.trackMenu.removeIdx)
		m.normalizeQueueOverlay()
		m.status.Show("Removed queued track (Ctrl+Z to undo)", statusTTLDefault)
		return m.rearmPreload()
	case menuRemovePlMgr:
		// Removing through the menu is a single-track action, so the mark
		// set is cleared first: it must not extend the deletion to rows the
		// user happened to have marked.
		m.plManager.marked = map[int]bool{}
		m.plManager.cursor = m.trackMenu.removeIdx
		m.plMgrRemoveSelectedTracks()
	}
	return nil
}

// — song radio —

// recommenderForTrack resolves the Recommender for a track: its owning
// provider first, then any registered provider that can recommend at all
// (a local-file radio seeded from Spotify, say, degrades to whatever the
// provider can match rather than hiding the item).
func (m Model) recommenderForTrack(t playlist.Track) (provider.Recommender, string) {
	if prov := m.providerForTrack(t.Path); prov != nil {
		if rec, ok := prov.(provider.Recommender); ok {
			return rec, prov.Name()
		}
	}
	if rec, ok := m.provider.(provider.Recommender); ok {
		return rec, m.provider.Name()
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if rec, ok := pe.Provider.(provider.Recommender); ok {
			return rec, pe.Provider.Name()
		}
	}
	return nil, ""
}

// trackRadioMsg carries the seeded recommendation batch. The seed rides
// along: the menu's track field may have been overwritten by a newer menu
// before the fetch completes.
type trackRadioMsg struct {
	seed         playlist.Track
	tracks       []playlist.Track
	err          error
	providerName string
	gen          uint64
}

// startTrackRadio dispatches a RecommendTracks call seeded from one track.
func (m *Model) startTrackRadio(t playlist.Track) tea.Cmd {
	rec, name := m.recommenderForTrack(t)
	if rec == nil {
		m.status.Show("No recommendation provider for this track", statusTTLDefault)
		return nil
	}
	m.status.Activityf(statusTTLDefault, "Finding tracks like %s…", t.DisplayName())
	gen := nextRequest(&m.requests.trackMenu)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tracks, err := rec.RecommendTracks(ctx, []playlist.Track{t}, trackRadioLimit)
		return trackRadioMsg{seed: t, tracks: tracks, err: err, providerName: name, gen: gen}
	}
}

// handleTrackRadio applies the radio batch: the seed first, then the
// recommendations (minus the seed if the provider echoed it back).
func (m *Model) handleTrackRadio(msg trackRadioMsg) tea.Cmd {
	if msg.gen != m.requests.trackMenu {
		return nil
	}
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Song radio failed: %s", msg.err)
		return nil
	}
	tracks := make([]playlist.Track, 0, len(msg.tracks)+1)
	seed := msg.seed
	tracks = append(tracks, seed)
	for _, t := range msg.tracks {
		if t.Path == "" || t.Path == seed.Path {
			continue
		}
		tracks = append(tracks, t)
	}
	if len(tracks) == 1 {
		m.status.Show("No recommendations found", statusTTLDefault)
		return nil
	}
	m.retireTracksPaging()
	m.replacePlayerPlaylist(tracks)
	m.status.Successf(statusTTLDefault, "Song radio: %d tracks", len(tracks))
	m.notifyAll()
	return m.playCurrentTrack()
}

// — go to artist / album —

// trackArtistTarget resolves a track's artist on a registered provider,
// using the TrackArtistResolver candidates in the same order as 'N'.
func (m Model) trackArtistTarget(track playlist.Track) (selectedTrackArtistTarget, bool) {
	candidates := make([]playlist.Provider, 0, len(m.providers)+1)
	if m.provider != nil {
		candidates = append(candidates, m.provider)
	}
	for _, entry := range m.providers {
		if entry.Provider != nil {
			candidates = append(candidates, entry.Provider)
		}
	}
	for _, candidate := range candidates {
		resolver, ok := candidate.(provider.TrackArtistResolver)
		if !ok {
			continue
		}
		artist, ok := resolver.ArtistForTrack(track)
		if !ok {
			continue
		}
		browser, ok := candidate.(provider.ArtistBrowser)
		if ok {
			return selectedTrackArtistTarget{prov: candidate, browser: browser, artist: artist}, true
		}
	}
	return selectedTrackArtistTarget{}, false
}

// goToTrackArtist opens the track's artist on its provider: the profile
// screen when the provider can load one, the album list otherwise, and a
// provider-side search when nothing resolves the track directly.
func (m *Model) goToTrackArtist(t playlist.Track) tea.Cmd {
	if target, ok := m.trackArtistTarget(t); ok {
		if _, ok := target.prov.(provider.ArtistDetailLoader); ok {
			return m.openArtistScreen(target.prov.Name(), target.artist)
		}
		m.openNavBrowserWith(target.prov)
		m.navBrowser.mode = navBrowseModeByArtistAlbum
		m.navBrowser.screen = navBrowseScreenAlbums
		m.navBrowser.selArtist = target.artist
		m.navBrowser.directTrackJump = true
		if entry, found := providerBrowseEntryForMode(target.prov, provider.BrowseArtistAlbums); found {
			m.navBrowser.openInPlaylist = entry.OpenInPlaylist
		}
		m.navBrowser.loading = true
		return fetchNavArtistAlbumsCmd(target.browser, target.artist.ID, m.nextNavRequest())
	}
	if s, name := m.multiSearcherFor(t); s != nil && t.Artist != "" {
		m.status.Activityf(statusTTLDefault, "Finding artist %s…", t.Artist)
		gen := nextRequest(&m.requests.trackMenu)
		return searchArtistCmd(s, name, t.Artist, gen)
	}
	m.status.Show("No artist info available", statusTTLDefault)
	return nil
}

// menuArtistMsg carries a resolved artist from the MultiSearcher fallback.
type menuArtistMsg struct {
	artist       provider.ArtistInfo
	providerName string
	err          error
	gen          uint64
}

func searchArtistCmd(s provider.MultiSearcher, providerName, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := s.SearchAll(ctx, query, 10)
		if err != nil {
			return menuArtistMsg{providerName: providerName, err: err, gen: gen}
		}
		for _, a := range res.Artists {
			if strings.EqualFold(a.Name, query) {
				return menuArtistMsg{artist: a, providerName: providerName, gen: gen}
			}
		}
		if len(res.Artists) > 0 {
			return menuArtistMsg{artist: res.Artists[0], providerName: providerName, gen: gen}
		}
		return menuArtistMsg{providerName: providerName, err: fmt.Errorf("no artist match for %q", query), gen: gen}
	}
}

// goToTrackAlbum opens the track's album: straight into its track list when
// the track knows its album ID, otherwise the artist's album list, and
// otherwise a provider-side search for the album title.
func (m *Model) goToTrackAlbum(t playlist.Track) tea.Cmd {
	if id := t.AlbumID(); id != "" {
		if prov := m.providerForTrack(t.Path); prov != nil {
			if l, ok := prov.(provider.AlbumTrackLoader); ok {
				m.openNavBrowserWith(prov)
				m.navBrowser.mode = navBrowseModeByArtistAlbum
				m.navBrowser.screen = navBrowseScreenTracks
				m.navBrowser.selAlbum = provider.AlbumInfo{ID: id, Name: t.Album, Artist: t.Artist}
				m.navBrowser.directTrackJump = true
				m.navBrowser.loading = true
				return fetchNavAlbumTracksCmd(l, id, m.nextNavRequest())
			}
		}
	}
	if target, ok := m.trackArtistTarget(t); ok {
		m.openNavBrowserWith(target.prov)
		m.navBrowser.mode = navBrowseModeByArtistAlbum
		m.navBrowser.screen = navBrowseScreenAlbums
		m.navBrowser.selArtist = target.artist
		m.navBrowser.directTrackJump = true
		m.navBrowser.loading = true
		return fetchNavArtistAlbumsCmd(target.browser, target.artist.ID, m.nextNavRequest())
	}
	if s, name := m.multiSearcherFor(t); s != nil && t.Album != "" {
		m.status.Activityf(statusTTLDefault, "Finding album %s…", t.Album)
		gen := nextRequest(&m.requests.trackMenu)
		return searchAlbumCmd(s, name, t.Album, t.Artist, gen)
	}
	m.status.Show("No album info available", statusTTLDefault)
	return nil
}

// menuAlbumMsg carries a resolved album from the MultiSearcher fallback.
type menuAlbumMsg struct {
	album        provider.AlbumInfo
	providerName string
	err          error
	gen          uint64
}

func searchAlbumCmd(s provider.MultiSearcher, providerName, album, artist string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := s.SearchAll(ctx, album, 10)
		if err != nil {
			return menuAlbumMsg{providerName: providerName, err: err, gen: gen}
		}
		for _, a := range res.Albums {
			if strings.EqualFold(a.Name, album) && (artist == "" || strings.EqualFold(a.Artist, artist)) {
				return menuAlbumMsg{album: a, providerName: providerName, gen: gen}
			}
		}
		if len(res.Albums) > 0 {
			return menuAlbumMsg{album: res.Albums[0], providerName: providerName, gen: gen}
		}
		return menuAlbumMsg{providerName: providerName, err: fmt.Errorf("no album match for %q", album), gen: gen}
	}
}

// openResolvedAlbum drills into an album the MultiSearcher path resolved.
func (m *Model) openResolvedAlbum(msg menuAlbumMsg) tea.Cmd {
	prov := m.providerNamed(msg.providerName)
	if prov == nil {
		m.status.Show("Album provider is unavailable", statusTTLDefault)
		return nil
	}
	l, ok := prov.(provider.AlbumTrackLoader)
	if !ok {
		m.status.Show("This provider cannot open albums", statusTTLDefault)
		return nil
	}
	m.openNavBrowserWith(prov)
	m.navBrowser.mode = navBrowseModeByArtistAlbum
	m.navBrowser.screen = navBrowseScreenTracks
	m.navBrowser.selAlbum = msg.album
	m.navBrowser.directTrackJump = true
	m.navBrowser.loading = true
	return fetchNavAlbumTracksCmd(l, msg.album.ID, m.nextNavRequest())
}

// multiSearcherFor resolves a MultiSearcher for a track: its owner first,
// then the active provider, then anyone registered. Spotify is the only
// implementer today, which is exactly the provider needing the fallback.
func (m Model) multiSearcherFor(t playlist.Track) (provider.MultiSearcher, string) {
	if prov := m.providerForTrack(t.Path); prov != nil {
		if s, ok := prov.(provider.MultiSearcher); ok {
			return s, prov.Name()
		}
	}
	if s, ok := m.provider.(provider.MultiSearcher); ok {
		return s, m.provider.Name()
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if s, ok := pe.Provider.(provider.MultiSearcher); ok {
			return s, pe.Provider.Name()
		}
	}
	return nil, ""
}

// — credits —

// creditMetaSuffixes are the ProviderMeta suffixes a credits view exists
// for: people and rights. Providers populate what they can; tracks expose
// nothing here when the provider has no credits data (Spotify has no
// credits endpoint), in which case the overlay says so.
var creditMetaSuffixes = []string{
	"composer", "producer", "writer", "lyricist", "mixer", "engineer",
	"performer", "album_artist", "label", "publisher", "copyright", "isrc",
}

// creditLabel returns the display label for a ProviderMeta key when it
// names a credit ("qobuz.composer" → "Composer", "isrc" → "ISRC").
func creditLabel(key string) (string, bool) {
	k := strings.ToLower(key)
	for _, suffix := range creditMetaSuffixes {
		if k == suffix || strings.HasSuffix(k, "."+suffix) {
			if suffix == "isrc" {
				return "ISRC", true
			}
			label := strings.ReplaceAll(suffix, "_", " ")
			return strings.ToUpper(label[:1]) + label[1:], true
		}
	}
	return "", false
}

// creditsFields lists the credit fields a track exposes, then the standard
// metadata, so the overlay is never empty-handed but always honest about
// what is and isn't credits data.
func (m Model) creditsFields(t playlist.Track) []metadataField {
	var fields []metadataField
	for key, v := range t.ProviderMeta {
		label, ok := creditLabel(key)
		if !ok {
			continue
		}
		if v = metadataText(v); v != "" {
			fields = append(fields, metadataField{label, v})
		}
	}
	if len(fields) == 0 {
		fields = append(fields, metadataField{"Credits", "none exposed for this track"})
	}
	return append(fields, m.metadataFieldsFor(t)...)
}

// openCredits shows the credits overlay for a track.
func (m *Model) openCredits(t playlist.Track) {
	m.credits = creditsState{visible: true, track: t, fields: m.creditsFields(t)}
}

func (m *Model) handleCreditsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "i", "q":
		m.credits.visible = false
	case "up", "k", "ctrl+p":
		if m.credits.scroll > 0 {
			m.credits.scroll--
		}
	case "down", "j", "ctrl+n":
		m.credits.scroll++
	case "ctrl+u":
		m.credits.scroll -= max(1, m.effectivePlaylistVisible())
	case "ctrl+d":
		m.credits.scroll += max(1, m.effectivePlaylistVisible())
	}
	m.credits.scroll = max(0, min(m.credits.scroll, max(0, len(m.credits.fields)-m.effectivePlaylistVisible())))
	return nil
}

// — rendering —

func (m Model) trackMenuHeaderLine() string {
	name := m.trackMenu.track.DisplayName()
	if name == "" {
		name = "Track"
	}
	return sepHeaderN("Track — "+name, m.trackMenu.cursor+1, len(m.trackMenuItems()))
}

func (m Model) trackMenuHelpLine() string {
	return m.commandHelp(commandModeTrackMenu)
}

func (m Model) renderTrackMenuBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	items := m.trackMenuItems()
	lines := make([]string, 0, budget)
	for i, item := range items {
		if len(lines) >= budget {
			break
		}
		label := "  " + item.label + "  [" + item.key + "]"
		lines = append(lines, cursorLine(label, i == m.trackMenu.cursor))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

func (m Model) creditsHeaderLine() string {
	name := m.credits.track.DisplayName()
	if name == "" {
		name = "Track"
	}
	return sepHeader("Credits — " + name)
}

func (m Model) creditsHelpLine() string {
	return m.commandHelp(commandModeCredits)
}

func (m Model) renderCreditsBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	lines := make([]string, 0, len(m.credits.fields))
	for _, f := range m.credits.fields {
		lines = append(lines, dimStyle.Render("  "+f.label+": ")+trackStyle.Render(f.value))
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  No credits or metadata available."))
	}
	start := min(m.credits.scroll, max(0, len(lines)-budget))
	end := min(start+budget, len(lines))
	return bodyLines(lines[start:end], budget)
}
