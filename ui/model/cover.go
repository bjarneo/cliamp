package model

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/artcache"
	"github.com/bjarneo/cliamp/internal/kittyimg"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	coverLoadTimeout = 30 * time.Second
	coverMinRows     = 4
)

// coverSlot names which cover a load or render belongs to: the playing track
// beside the visualizer, or the highlighted track in the info overlay.
type coverSlot int

const (
	coverSlotPlaying coverSlot = iota
	coverSlotInfo
)

type coverState struct {
	mode string

	key       string
	local     string
	aspect    float64 // image width / height, 0 when unknown
	failed    bool
	loading   bool
	loadGen   uint64
	rendering bool
	renderGen uint64

	cols     int
	rows     int
	protocol string
	block    string
	imageID  int

	cellWidth  float64
	cellHeight float64
}

type coverLoadedMsg struct {
	slot   coverSlot
	key    string
	local  string
	aspect float64
	gen    uint64
	err    error
}

type coverRenderedMsg struct {
	slot     coverSlot
	key      string
	gen      uint64
	cols     int
	rows     int
	protocol string
	block    string
	imageID  int
	err      error
}

// SetCoverArtMode sets cover rendering to auto, on, or off. Auto draws covers
// only with Kitty graphics; on also uses ANSI half blocks elsewhere.
func (m *Model) SetCoverArtMode(mode string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "on", "off":
	default:
		mode = "auto"
	}
	m.cover.mode = mode
}

func (m Model) coverArtMode() string {
	if m.cover.mode == "" {
		return "auto"
	}
	return m.cover.mode
}

// coverEnabled reports whether covers are drawn at all. Auto draws them only
// in terminals with real image support; the ANSI half-block fallback is too
// blurry to read, so it needs an explicit "on".
func (m Model) coverEnabled() bool {
	switch m.coverArtMode() {
	case "on":
		return true
	case "auto":
		return kittyimg.SupportsGraphics()
	}
	return false
}

// artworkSource finds where track's cover comes from: a URL the cache can
// resolve, or a provider that fetches authenticated bytes. The key names the
// cover in the cache and in coverState.
func (m *Model) artworkSource(track playlist.Track) (key, source string, fetcher provider.ArtworkFetcher, ok bool) {
	source = m.baseArtworkURL(track)
	if source == "" {
		fetcher, _ = findCapable(m, func(candidate provider.ArtworkFetcher) bool {
			return candidate.CanFetchArtwork(track)
		})
	}
	if source == "" && fetcher == nil {
		return "", "", nil, false
	}
	key = track.Path + "\x00" + source
	if fetcher != nil {
		key = track.Path + "\x00provider"
	}
	return key, source, fetcher, true
}

// ensureCover schedules at most one artwork load or render. Artwork is loaded
// even while the TUI pane is hidden so media controls and desktop notifications
// can reuse the same local cache. Update calls it after applying layout and
// playback changes, so commands never block the UI.
func (m *Model) ensureCover() tea.Cmd {
	cols, rows, protocol, visible := m.coverGeometry()
	if !visible {
		m.cover.clearRender()
	}
	track, _ := m.currentPlaybackTrack()
	if m.player == nil || (!m.player.IsPlaying() && !m.buffering) || track.Path == "" {
		m.cover.reset()
		return nil
	}

	key, source, fetcher, ok := m.artworkSource(track)
	if !ok {
		m.cover.reset()
		return nil
	}
	if key != m.cover.key {
		m.cover.startLoad(key)
		return loadCoverCmd(coverSlotPlaying, key, source, trackArtwork(fetcher, track), m.cover.loadGen)
	}
	if !visible {
		return nil
	}
	return m.cover.renderCmd(coverSlotPlaying, cols, rows, protocol)
}

// ensureInfoCover loads and renders the highlighted track's cover while the
// info overlay is open, and drops it when the overlay closes. A track that is
// already playing reuses the loaded cover instead of fetching it again.
func (m *Model) ensureInfoCover() tea.Cmd {
	c := &m.infoCover
	if !m.info.visible || !m.coverEnabled() || m.headless || m.quitting {
		c.reset()
		return nil
	}
	track := m.selectedMetadataTrack()
	if track.Path == "" {
		c.reset()
		return nil
	}
	// Playback reads the cover embedded in a local file; the playlist row
	// does not carry it.
	if playing, _ := m.currentPlaybackTrack(); playing.Path == track.Path && track.AlbumArtURL == "" {
		track.AlbumArtURL = playing.AlbumArtURL
	}
	key, source, fetcher, ok := m.artworkSource(track)
	embedded := !ok && localFileTrack(track)
	if embedded {
		key, ok = track.Path+"\x00embedded", true
	}
	if !ok {
		c.reset()
		return nil
	}
	if key != c.key {
		c.startLoad(key)
		if key == m.cover.key && m.cover.local != "" && m.cover.aspect > 0 {
			c.loading = false
			c.local = m.cover.local
			c.aspect = m.cover.aspect
		} else {
			if embedded {
				return loadEmbeddedCoverCmd(coverSlotInfo, key, track, c.loadGen)
			}
			return loadCoverCmd(coverSlotInfo, key, source, trackArtwork(fetcher, track), c.loadGen)
		}
	}
	cols, rows, protocol, visible := m.infoCoverGeometry()
	if !visible {
		c.clearRender()
		return nil
	}
	return c.renderCmd(coverSlotInfo, cols, rows, protocol)
}

// startLoad forgets the previous cover and marks key as loading.
func (c *coverState) startLoad(key string) {
	c.clearRender()
	c.key = key
	c.local = ""
	c.aspect = 0
	c.failed = false
	c.loading = true
	c.loadGen++
}

// renderCmd starts a render at the given size unless one is running or the
// current render already matches.
func (c *coverState) renderCmd(slot coverSlot, cols, rows int, protocol string) tea.Cmd {
	if c.loading || c.failed || c.local == "" || c.rendering || c.renderedAt(cols, rows, protocol) {
		return nil
	}
	c.clearRender()
	c.rendering = true
	c.renderGen++
	return renderCoverCmd(slot, c.key, c.local, cols, rows, protocol, 0, c.renderGen)
}

func (c coverState) renderedAt(cols, rows int, protocol string) bool {
	ready := c.block != ""
	if protocol == "kitty" {
		ready = c.imageID != 0
	}
	return ready && c.cols == cols && c.rows == rows && c.protocol == protocol
}

func (m Model) coverGeometry() (cols, rows int, protocol string, visible bool) {
	if m.fullVis || !m.showMetadata || !m.layout.twoColumn || !m.coverEnabled() || m.quitting || m.headless || m.usesSimplifiedLayout() ||
		m.usesContentFirstLayout() || m.visualizerDisabled() || m.layout.tier != layoutFull {
		return 0, 0, "", false
	}
	rows = m.layout.coverRows
	if rows < coverMinRows {
		return 0, 0, "", false
	}
	cellWidth, cellHeight := m.cellSize()
	cols = columnCoverCols(rows, m.layout.settingsWidth, cellWidth, cellHeight)
	if m.layout.panelWidth-cols-1 < playlistMinWidth {
		return 0, 0, "", false
	}
	return cols, rows, coverProtocol(), true
}

// cellSize returns the terminal cell size in pixels, read on each window
// resize; zero when the terminal does not report it.
func (m Model) cellSize() (width, height float64) {
	return m.cover.cellWidth, m.cover.cellHeight
}

func coverProtocol() string {
	if kittyimg.SupportsGraphics() {
		return "kitty"
	}
	return "blocks"
}

func squareCoverCols(rows int, cellWidth, cellHeight float64) int {
	return coverCols(rows, 1, cellWidth, cellHeight)
}

// coverCols is the width in cells of a rows-tall box with the image aspect
// (width / height). Without a known cell size it assumes 1:2 cells.
func coverCols(rows int, aspect, cellWidth, cellHeight float64) int {
	if cellWidth <= 0 || cellHeight <= 0 {
		cellWidth, cellHeight = 1, 2
	}
	return max(1, int(math.Round(float64(rows)*aspect*cellHeight/cellWidth)))
}

// squareCoverRows is the height in rows of a square cover cols wide. Cells
// rarely divide a square evenly, so the cover fills its box and loses at most
// half a row from its top and bottom; it lines up with a column of that width.
func squareCoverRows(cols int, cellWidth, cellHeight float64) int {
	if cellWidth <= 0 || cellHeight <= 0 {
		cellWidth, cellHeight = 1, 2
	}
	return max(1, int(math.Round(float64(cols)*cellWidth/cellHeight)))
}

// columnCoverCols is the width of a rows-tall square cover in a column width
// cells wide: the whole column once the rows hold a square that wide, so the
// cover lines up with the column, and narrower below that.
func columnCoverCols(rows, width int, cellWidth, cellHeight float64) int {
	if rows >= squareCoverRows(width, cellWidth, cellHeight) {
		return width
	}
	return min(width, squareCoverCols(rows, cellWidth, cellHeight))
}

// Info overlay layout: a blank row under the header, then the cover after a
// left margin, a gap, and the field text with its usual two-space indent.
const (
	infoCoverTop    = 1
	infoCoverMargin = 2
	infoCoverGap    = 1
	infoMinText     = 30
)

// infoCoverGeometry sizes the info-overlay cover. Its height is the number of
// field lines, capped by the body; its width keeps the image aspect. Fields
// wrap in the width the cover leaves, which can add lines and so grow the
// cover; the loop stops once the height settles. Height only grows and is
// capped by the body, so it settles within a few passes.
func (m Model) infoCoverGeometry() (cols, rows int, protocol string, visible bool) {
	c := m.infoCover
	if !m.info.visible || c.local == "" || c.aspect <= 0 {
		return 0, 0, "", false
	}
	budget := m.effectivePlaylistVisible() - infoCoverTop
	width := m.infoWidth()
	cellWidth, cellHeight := m.cellSize()
	rows = min(budget, len(m.infoLinesAt(width)))
	for {
		cols = coverCols(rows, c.aspect, cellWidth, cellHeight)
		textWidth := width - infoCoverMargin - cols - infoCoverGap
		if rows < coverMinRows || cols > width/2 || textWidth < infoMinText {
			return 0, 0, "", false
		}
		next := min(budget, len(m.infoLinesAt(textWidth)))
		if next == rows {
			return cols, rows, coverProtocol(), true
		}
		rows = next
	}
}

// infoBudget is the number of field lines the info body shows: the blank
// row above a cover takes one.
func (m Model) infoBudget() int {
	if _, _, _, visible := m.infoCoverGeometry(); visible {
		return m.effectivePlaylistVisible() - infoCoverTop
	}
	return m.effectivePlaylistVisible()
}

// infoTextWidth is the width left for the field text beside the cover.
func (m Model) infoTextWidth() int {
	if cols, _, _, visible := m.infoCoverGeometry(); visible {
		return m.infoWidth() - infoCoverMargin - cols - infoCoverGap
	}
	return m.infoWidth()
}

// trackArtwork returns the authenticated download of track's cover, or nil
// when the cover comes from a URL instead.
func trackArtwork(fetcher provider.ArtworkFetcher, track playlist.Track) func(context.Context) ([]byte, error) {
	if fetcher == nil {
		return nil
	}
	return func(ctx context.Context) ([]byte, error) { return fetcher.FetchArtwork(ctx, track) }
}

var errNoEmbeddedArt = errors.New("no embedded cover")

// localFileTrack reports whether track is a file on this machine, whose tags
// can hold a cover.
func localFileTrack(track playlist.Track) bool {
	return track.Path != "" && !track.Stream && !playlist.IsURL(track.Path) && !strings.HasPrefix(track.Path, "ssh://")
}

// loadEmbeddedCoverCmd reads the cover embedded in the tags of a local file,
// as playback does. Reading the tags is file I/O, so it runs as a command.
// The tags are read each time and the image is cached under its content, so
// a retagged file shows its new cover.
func loadEmbeddedCoverCmd(slot coverSlot, key string, track playlist.Track, gen uint64) tea.Cmd {
	return func() tea.Msg {
		local := playlist.RefreshEmbeddedMetadata(track).AlbumArtURL
		var err error
		if local == "" {
			err = errNoEmbeddedArt
		}
		return coverLoaded(slot, key, local, gen, err)
	}
}

// loadCoverCmd resolves source through the cache, or, without a source,
// downloads the cover with fetch and caches it under key.
func loadCoverCmd(slot coverSlot, key, source string, fetch func(context.Context) ([]byte, error), gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), coverLoadTimeout)
		defer cancel()
		var (
			local string
			err   error
		)
		if source != "" {
			local, err = artcache.Resolve(ctx, source)
		} else {
			if cached, ok := artcache.Lookup(key); ok {
				local = cached
			} else {
				var raw []byte
				raw, err = fetch(ctx)
				if err == nil {
					local, err = artcache.StoreForKey(key, raw)
				}
			}
		}
		return coverLoaded(slot, key, local, gen, err)
	}
}

func coverLoaded(slot coverSlot, key, local string, gen uint64, err error) coverLoadedMsg {
	msg := coverLoadedMsg{slot: slot, key: key, local: local, gen: gen, err: err}
	if err == nil {
		// The aspect only sizes the info-overlay cover; a failure keeps
		// the cover usable for the square pane and notifications.
		msg.aspect, _ = kittyimg.Aspect(local)
	}
	return msg
}

func renderCoverCmd(slot coverSlot, key, source string, cols, rows int, protocol string, reuseID int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		msg := coverRenderedMsg{slot: slot, key: key, gen: gen, cols: cols, rows: rows, protocol: protocol}
		// The playing-track cover fills a box as wide as the settings column;
		// the info cover keeps its box to the image aspect.
		fill := slot != coverSlotInfo
		if protocol == "kitty" {
			show := kittyimg.Show
			if fill {
				show = kittyimg.ShowFill
			}
			placement, err := show(source, cols, rows, reuseID)
			msg.err = err
			msg.imageID = placement.ID
			return msg
		}
		blocks := kittyimg.Blocks
		if fill {
			blocks = kittyimg.BlocksFill
		}
		msg.block, msg.err = blocks(source, cols, rows)
		return msg
	}
}

func (m *Model) coverFor(slot coverSlot) *coverState {
	switch slot {
	case coverSlotInfo:
		return &m.infoCover
	}
	return &m.cover
}

func (m *Model) handleCoverLoaded(msg coverLoadedMsg) {
	c := m.coverFor(msg.slot)
	if msg.gen != c.loadGen || msg.key != c.key {
		return
	}
	c.loading = false
	if msg.err != nil {
		c.failed = true
		applog.Debug("cover art load failed: %v", msg.err)
		return
	}
	c.local = msg.local
	c.aspect = msg.aspect
}

func (m *Model) handleCoverRendered(msg coverRenderedMsg) {
	c := m.coverFor(msg.slot)
	if msg.gen != c.renderGen || msg.key != c.key {
		if msg.imageID != 0 {
			kittyimg.Clear(msg.imageID)
		}
		return
	}
	c.rendering = false
	if msg.err != nil {
		c.failed = true
		applog.Debug("cover art render failed: %v", msg.err)
		return
	}
	c.cols = msg.cols
	c.rows = msg.rows
	c.protocol = msg.protocol
	c.block = msg.block
	if msg.imageID != 0 {
		c.imageID = msg.imageID
	}
}

func (c *coverState) clearRender() {
	if c.imageID == 0 && c.block == "" && c.protocol == "" &&
		c.cols == 0 && c.rows == 0 && !c.rendering {
		return
	}
	if c.imageID != 0 && c.protocol == "kitty" {
		kittyimg.Clear(c.imageID)
	}
	c.imageID = 0
	c.block = ""
	c.protocol = ""
	c.cols = 0
	c.rows = 0
	c.rendering = false
	// Reject an in-flight render whose dimensions no longer apply.
	c.renderGen++
}

func (c *coverState) reset() {
	if c.key == "" && c.local == "" && !c.failed && !c.loading &&
		c.block == "" && c.imageID == 0 {
		return
	}
	c.clearRender()
	c.key = ""
	c.local = ""
	c.aspect = 0
	c.failed = false
	c.loading = false
	c.loadGen++
}

func (m Model) renderCover() string { return m.cover.render() }

func (c coverState) render() string {
	if c.protocol == "kitty" && c.imageID != 0 {
		if placement, ok := kittyimg.PlacementByID(c.imageID); ok {
			return placement.Block()
		}
		return ""
	}
	return c.block
}
