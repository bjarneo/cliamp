package model

// immersive_art.go puts real cover art into the immersive frame's art boxes
// (Now Playing and the rows/grid canvas thumbnails). Covers are fetched and
// decoded once per URL, then encoded once per box size: as Sixel images for
// terminals that support them (drawn through the termimg layer on top of
// blank cells) or as truecolor half-block text everywhere else. Until a
// cover is ready the box keeps its text placeholder.

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders for cover art
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/ui/termimg"
)

// imageMode is the "images" config key.
type imageMode int

const (
	imagesAuto   imageMode = iota // Sixel when the terminal reports it, else blocks
	imagesSixel                   // always Sixel
	imagesBlocks                  // always half-block text
	imagesOff                     // text placeholders only
)

// artKind is how a cover is drawn right now.
type artKind int

const (
	artNone artKind = iota
	artSixel
	artBlocks
)

const (
	artMaxBytes     = 8 << 20 // covers larger than this are not decoded
	artFetchTimeout = 15 * time.Second
	artPollEvery    = 250 * time.Millisecond
	artMaxImages    = 128 // decoded covers kept before the cache is reset
	artMaxEncodings = 512 // encoded covers kept before the cache is reset
	artMaxInflight  = 6   // concurrent fetches
	artMaxSide      = 640 // decoded covers are downscaled to this; boxes are far smaller
)

// artStore caches decoded covers by URL and their encodings by box size.
// It is shared by pointer so View (on a Model copy) and Update agree.
type artStore struct {
	mu       sync.Mutex
	imgs     map[string]*artImage
	encs     map[artKey]*artEncoding
	inflight int
}

type artImage struct {
	img     image.Image
	loading bool
	failed  bool
}

type artKey struct {
	url   string
	w, h  int // cells
	cellW int // pixels per cell when encoded (Sixel sizes depend on it)
	cellH int
	kind  artKind
}

type artEncoding struct {
	sixel   []byte
	blocks  []string
	loading bool
}

func newArtStore() *artStore {
	return &artStore{imgs: map[string]*artImage{}, encs: map[artKey]*artEncoding{}}
}

// SetImageMode applies the "images" config key.
func (m *Model) SetImageMode(s string) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sixel":
		m.imgMode = imagesSixel
	case "blocks", "halfblocks", "text":
		m.imgMode = imagesBlocks
	case "off", "none", "false":
		m.imgMode = imagesOff
	default:
		m.imgMode = imagesAuto
	}
}

// SetImageLayer attaches the output layer images are drawn through.
func (m *Model) SetImageLayer(l *termimg.Layer) {
	m.imgLayer = l
	if m.art == nil {
		m.art = newArtStore()
	}
	if m.pixVis == nil && l != nil {
		m.pixVis = newPixVisWorker(l)
	}
	if m.frameMemo == nil {
		m.frameMemo = &frameMemo{}
	}
}

// artKindNow resolves the image mode against what the terminal reported.
func (m Model) artKindNow() artKind {
	if m.art == nil {
		return artNone
	}
	switch m.imgMode {
	case imagesOff:
		return artNone
	case imagesSixel:
		return artSixel
	case imagesBlocks:
		return artBlocks
	}
	if m.termSixel {
		return artSixel
	}
	return artBlocks
}

// cellPx is the terminal's cell size in pixels (CSI 16 t), with the common
// 10x20 default until it answers.
func (m Model) cellPx() (int, int) {
	if m.cellW > 0 && m.cellH > 0 {
		return m.cellW, m.cellH
	}
	return 10, 20
}

// termImageQueries asks the terminal for its device attributes (Sixel is
// attribute 4) and its cell size in pixels.
func termImageQueries() tea.Cmd {
	return tea.Batch(tea.Raw(ansi.RequestPrimaryDeviceAttributes), tea.Raw("\x1b[16t"))
}

// handleTermImageEvent records terminal replies. It reports whether msg was
// one of them.
func (m *Model) handleTermImageEvent(msg tea.Msg) bool {
	switch ev := msg.(type) {
	case uv.PrimaryDeviceAttributesEvent:
		for _, a := range ev {
			if a == 4 {
				m.termSixel = true
			}
		}
		applog.Debug("images: terminal attributes %v, sixel=%v, mode=%d", []int(ev), m.termSixel, m.imgMode)
		return true
	case uv.CellSizeEvent:
		if ev.Width > 0 && ev.Height > 0 && (ev.Width != m.cellW || ev.Height != m.cellH) {
			m.cellW, m.cellH = ev.Width, ev.Height
			m.imgLayer.Invalidate()
			applog.Debug("images: cell size %dx%d px", ev.Width, ev.Height)
		}
		return true
	}
	return false
}

// — requests —

type artPollMsg struct{}

type artFetchedMsg struct {
	url string
	img image.Image
	err error
}

type artEncodedMsg struct {
	key    artKey
	sixel  []byte
	blocks []string
}

func artPollCmd() tea.Cmd {
	return tea.Tick(artPollEvery, func(time.Time) tea.Msg { return artPollMsg{} })
}

// artSlot is one art box in the current frame: the cover URL and the box in
// frame content coordinates.
type artSlot struct {
	url  string
	rect immRect
}

// immArtSlots lists the art boxes the current frame draws.
func (m Model) immArtSlots() []artSlot {
	if !m.immersiveShown() || m.fullVis {
		return nil
	}
	g := m.immGeom()
	var slots []artSlot
	if t, _ := m.currentPlaybackTrack(); t.AlbumArtURL != "" && g.npArt.W > 0 && g.npArt.H > 0 {
		slots = append(slots, artSlot{url: t.AlbumArtURL, rect: g.npArt})
	}
	if m.immersive.mode == immCanvasList || m.immersive.view == immViewSettings {
		return slots
	}
	items := m.canvasItems()
	for _, it := range m.immCanvasItemsGeom(g.canvasIW, g.canvasIH) {
		if it.idx < len(items) && items[it.idx].art != "" && it.art.W > 0 && it.art.H > 0 {
			slots = append(slots, artSlot{url: items[it.idx].art, rect: it.art})
		}
	}
	return slots
}

// immArtRequests starts fetches and encodes for art boxes on screen that
// have no cover yet. It is driven by artPollMsg while immersive is active.
func (m *Model) immArtRequests() tea.Cmd {
	kind := m.artKindNow()
	if kind == artNone {
		return nil
	}
	cw, ch := m.cellPx()
	a := m.art
	a.mu.Lock()
	defer a.mu.Unlock()
	var cmds []tea.Cmd
	for _, s := range m.immArtSlots() {
		im := a.imgs[s.url]
		switch {
		case im == nil:
			if a.inflight >= artMaxInflight {
				continue
			}
			if len(a.imgs) >= artMaxImages {
				a.imgs, a.encs = map[string]*artImage{}, map[artKey]*artEncoding{}
			}
			a.imgs[s.url] = &artImage{loading: true}
			a.inflight++
			cmds = append(cmds, fetchArtCmd(s.url))
		case im.img != nil:
			key := artKey{url: s.url, w: s.rect.W, h: s.rect.H, cellW: cw, cellH: ch, kind: kind}
			if a.encs[key] == nil {
				if len(a.encs) >= artMaxEncodings {
					a.encs = map[artKey]*artEncoding{}
				}
				a.encs[key] = &artEncoding{loading: true}
				cmds = append(cmds, encodeArtCmd(key, im.img, cw, ch))
			}
		}
	}
	return tea.Batch(cmds...)
}

// handleArtMsg applies fetch/encode results and the poll tick.
func (m *Model) handleArtMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case artPollMsg:
		if !m.immersive.active || m.art == nil {
			m.artPolling = false
			return nil, true
		}
		return tea.Batch(m.immArtRequests(), artPollCmd()), true
	case artFetchedMsg:
		if m.art == nil {
			return nil, true
		}
		m.art.mu.Lock()
		m.art.inflight = max(0, m.art.inflight-1)
		if im := m.art.imgs[msg.url]; im != nil {
			im.loading = false
			im.img = msg.img
			im.failed = msg.err != nil
		}
		if msg.err != nil {
			applog.Debug("images: cover %s: %v", msg.url, msg.err)
		}
		m.art.mu.Unlock()
		return m.immArtRequests(), true
	case artEncodedMsg:
		if m.art == nil {
			return nil, true
		}
		m.art.mu.Lock()
		if e := m.art.encs[msg.key]; e != nil {
			e.loading = false
			e.sixel, e.blocks = msg.sixel, msg.blocks
		}
		m.art.mu.Unlock()
		return nil, true
	}
	return nil, false
}

// startArtPolling begins the art loop once per immersive session.
func (m *Model) startArtPolling() tea.Cmd {
	if m.artPolling || m.art == nil || m.imgMode == imagesOff {
		return nil
	}
	m.artPolling = true
	return tea.Batch(artPollCmd(), termImageQueries())
}

func fetchArtCmd(u string) tea.Cmd {
	return func() tea.Msg {
		img, err := loadArt(u)
		return artFetchedMsg{url: u, img: img, err: err}
	}
}

// loadArt fetches and decodes one cover from an http(s) or file:// URL.
func loadArt(u string) (image.Image, error) {
	var r io.ReadCloser
	switch {
	case strings.HasPrefix(u, "file://"):
		p, err := url.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("cover url: %w", err)
		}
		path := p.Path
		if len(path) > 2 && path[0] == '/' && path[2] == ':' {
			path = path[1:] // file:///C:/... on Windows
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open cover: %w", err)
		}
		r = f
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		ctx, cancel := context.WithTimeout(context.Background(), artFetchTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("cover request: %w", err)
		}
		req.Header.Set("User-Agent", httpclient.UserAgent)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch cover: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("fetch cover: %s", resp.Status)
		}
		r = resp.Body
	default:
		return nil, errors.New("cover: unsupported url")
	}
	defer r.Close()
	img, _, err := image.Decode(io.LimitReader(r, artMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("decode cover: %w", err)
	}
	// Keep the cache small: podcast art is often 3000px square.
	if b := img.Bounds(); max(b.Dx(), b.Dy()) > artMaxSide {
		w, h := b.Dx(), b.Dy()
		if w >= h {
			h, w = max(1, h*artMaxSide/w), artMaxSide
		} else {
			w, h = max(1, w*artMaxSide/h), artMaxSide
		}
		return termimg.Fill(img, w, h), nil
	}
	return img, nil
}

func encodeArtCmd(key artKey, img image.Image, cellW, cellH int) tea.Cmd {
	return func() tea.Msg {
		switch key.kind {
		case artSixel:
			px := termimg.Fill(img, key.w*cellW, key.h*cellH)
			return artEncodedMsg{key: key, sixel: termimg.EncodeSixel(px)}
		default:
			px := termimg.Fill(img, key.w, key.h*2)
			return artEncodedMsg{key: key, blocks: termimg.HalfBlocks(px)}
		}
	}
}

// — drawing —

// immArtCells returns what the text frame shows in an art box: blanks
// under a ready Sixel cover, the half-block cover, or ok=false to keep the
// placeholder.
func (m Model) immArtCells(u string, w, h int) ([]string, bool) {
	kind := m.artKindNow()
	if kind == artNone || u == "" {
		return nil, false
	}
	m.art.mu.Lock()
	cw, ch := m.cellPx()
	e := m.art.encs[artKey{url: u, w: w, h: h, cellW: cw, cellH: ch, kind: kind}]
	m.art.mu.Unlock()
	switch {
	case e == nil || e.loading:
		return nil, false
	case kind == artSixel && len(e.sixel) > 0:
		lines := make([]string, h)
		for i := range lines {
			lines[i] = strings.Repeat(" ", w)
		}
		return lines, true
	case kind == artBlocks && len(e.blocks) > 0:
		return e.blocks, true
	}
	return nil, false
}

// clearImages removes every image (screens without art, quitting, the
// too-small notice) and resets the row memo.
func (m Model) clearImages() {
	m.imgLayer.Set(nil)
	m.imgLayer.ArmLive(0, 0, 0, 0)
	m.touchChangedRows("")
}

// frameMemo keeps the last view's lines so View can tell the image layer
// which screen rows changed (see termimg.Layer.Touch).
type frameMemo struct{ lines []string }

// touchChangedRows marks rows whose text differs from the previous view;
// "" (a non-immersive view) resets the memo so the next immersive view
// touches every row.
func (m Model) touchChangedRows(rendered string) {
	if m.frameMemo == nil || m.imgLayer == nil {
		return
	}
	if rendered == "" {
		m.frameMemo.lines = nil
		return
	}
	lines := strings.Split(rendered, "\n")
	var rows []int
	for i, l := range lines {
		if i >= len(m.frameMemo.lines) || m.frameMemo.lines[i] != l {
			rows = append(rows, i)
		}
	}
	m.frameMemo.lines = lines
	m.imgLayer.Touch(rows...)
}

// immArtPlacements lists the Sixel covers to draw over this frame's blank
// art boxes, in screen cells.
func (m Model) immArtPlacements() []termimg.Placement {
	if m.artKindNow() != artSixel {
		return nil
	}
	slots := m.immArtSlots()
	if len(slots) == 0 {
		return nil
	}
	ox, oy := m.layout.paddingH, m.layout.paddingV
	cw, ch := m.cellPx()
	ps := make([]termimg.Placement, 0, len(slots))
	m.art.mu.Lock()
	defer m.art.mu.Unlock()
	for _, s := range slots {
		e := m.art.encs[artKey{url: s.url, w: s.rect.W, h: s.rect.H, cellW: cw, cellH: ch, kind: artSixel}]
		if e == nil || len(e.sixel) == 0 {
			continue
		}
		ps = append(ps, termimg.Placement{
			Key: s.url, X: ox + s.rect.X, Y: oy + s.rect.Y, W: s.rect.W, H: s.rect.H, Data: e.sixel,
		})
	}
	return ps
}
