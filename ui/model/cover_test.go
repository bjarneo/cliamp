package model

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func coverTestModel(t *testing.T, width, height int) Model {
	t.Helper()
	t.Setenv("TERM_PROGRAM", "")
	engine := &playbackFakeEngine{playing: true}
	m := New(engine, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
	m.width, m.height = width, height
	m.showMetadata = true
	// Most tests run in a plain test terminal; "on" keeps the block fallback
	// so they can check layout and rendering.
	m.SetCoverArtMode("on")
	m.recomputeLayout()
	return m
}

func TestCoverGeometryModes(t *testing.T) {
	tests := []struct {
		name  string
		term  string
		mode  string
		w, h  int
		want  bool
		proto string
	}{
		{name: "auto with kitty", term: "xterm-kitty", mode: "auto", w: 100, h: 24, want: true, proto: "kitty"},
		{name: "auto without graphics", term: "xterm-256color", mode: "auto", w: 100, h: 24},
		{name: "forced block fallback", term: "xterm-256color", mode: "on", w: 100, h: 24, want: true, proto: "blocks"},
		{name: "disabled", term: "xterm-kitty", mode: "off", w: 100, h: 24},
		{name: "compact layout", term: "xterm-kitty", mode: "on", w: 70, h: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			m := coverTestModel(t, tt.w, tt.h)
			m.SetCoverArtMode(tt.mode)
			cols, rows, protocol, visible := m.coverGeometry()
			if visible != tt.want || protocol != tt.proto {
				t.Fatalf("coverGeometry = (%d,%d,%q,%v), want protocol %q visible %v", cols, rows, protocol, visible, tt.proto, tt.want)
			}
			if visible && (cols != rows*2 || rows != m.layout.visualizerRows) {
				t.Fatalf("cover dimensions = %dx%d, visualizer rows = %d", cols, rows, m.layout.visualizerRows)
			}
		})
	}
}

func TestSetCoverArtModeNormalizesInvalidValue(t *testing.T) {
	m := Model{}
	m.SetCoverArtMode(" ON ")
	if got := m.coverArtMode(); got != "on" {
		t.Fatalf("mode = %q, want on", got)
	}
	m.SetCoverArtMode("unexpected")
	if got := m.coverArtMode(); got != "auto" {
		t.Fatalf("mode = %q, want auto", got)
	}
}

func TestCoverFollowsMetadataVisibility(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 120, 40)
	m.cover.local = "file:///cover.png"
	m.recomputeLayout()

	cols, rows, protocol, visible := m.coverGeometry()
	if !visible || protocol != "blocks" {
		t.Fatalf("coverGeometry = (%d,%d,%q,%v)", cols, rows, protocol, visible)
	}
	// A square as wide as the settings column, with the visualizer as tall.
	if cols != m.layout.settingsWidth || rows != squareCoverRows(cols, 0, 0) || rows != m.layout.visualizerRows {
		t.Fatalf("cover = %dx%d, settings %d cols, visualizer rows = %d", cols, rows, m.layout.settingsWidth, m.layout.visualizerRows)
	}

	m.SetShowMetadata(false)
	if cols, rows, protocol, visible := m.coverGeometry(); visible || cols != 0 || rows != 0 || protocol != "" {
		t.Fatalf("hidden metadata coverGeometry = (%d,%d,%q,%v)", cols, rows, protocol, visible)
	}

	m.SetShowMetadata(true)
	m.SetHideSettingsPane(true)
	if _, _, _, visible := m.coverGeometry(); visible {
		t.Fatal("cover is visible while the metadata sidebar is hidden")
	}
}

func TestLargeCoverKeepsMetadataVisible(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 80, 24)
	m.cover.local = "file:///cover.png"
	m.recomputeLayout()

	if _, _, _, visible := m.coverGeometry(); !visible {
		t.Fatal("cover is hidden in the minimum full layout")
	}
	if rows := m.metadataPaneRows(m.effectivePlaylistVisible()); rows < metadataPaneMinRows {
		t.Fatalf("metadata rows = %d, want at least %d", rows, metadataPaneMinRows)
	}
}

func TestSquareCoverColsUsesTerminalCellAspect(t *testing.T) {
	tests := []struct {
		name              string
		rows              int
		cellWidth, height float64
		want              int
	}{
		{name: "unknown geometry", rows: 14, want: 28},
		{name: "one to two cells", rows: 14, cellWidth: 10, height: 20, want: 28},
		{name: "taller cells", rows: 14, cellWidth: 14, height: 30, want: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := squareCoverCols(tt.rows, tt.cellWidth, tt.height); got != tt.want {
				t.Fatalf("squareCoverCols() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStaleCoverLoadIsIgnored(t *testing.T) {
	m := Model{cover: coverState{key: "new", loadGen: 2, loading: true}}
	m.handleCoverLoaded(coverLoadedMsg{key: "old", gen: 1, local: "file:///old.png"})
	if m.cover.local != "" || !m.cover.loading {
		t.Fatalf("stale load changed state: %+v", m.cover)
	}
	m.handleCoverLoaded(coverLoadedMsg{key: "new", gen: 2, local: "file:///new.png"})
	if m.cover.local != "file:///new.png" || m.cover.loading {
		t.Fatalf("current load was not applied: %+v", m.cover)
	}
}

func TestCoverRenderReadiness(t *testing.T) {
	tests := []struct {
		name     string
		state    coverState
		protocol string
		want     bool
	}{
		{name: "kitty placement", state: coverState{cols: 28, rows: 14, protocol: "kitty", imageID: 1001}, protocol: "kitty", want: true},
		{name: "kitty missing image", state: coverState{cols: 28, rows: 14, protocol: "kitty"}, protocol: "kitty"},
		{name: "block image", state: coverState{cols: 28, rows: 14, protocol: "blocks", block: "▀"}, protocol: "blocks", want: true},
		{name: "wrong dimensions", state: coverState{cols: 14, rows: 7, protocol: "kitty", imageID: 1001}, protocol: "kitty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.renderedAt(28, 14, tt.protocol); got != tt.want {
				t.Fatalf("renderedAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoverLoadsAndRendersWithoutBlockingUpdate(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 100, 24)
	m.SetCoverArtMode("on")

	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatalf("encode cover: %v", err)
	}
	path := filepath.Join(t.TempDir(), "cover art.png")
	if err := os.WriteFile(path, raw.Bytes(), 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	source := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	m.playlist.Replace([]playlist.Track{{Path: "song.mp3", AlbumArtURL: source}})

	load := m.ensureCover()
	if load == nil || !m.cover.loading {
		t.Fatal("ensureCover did not schedule a load")
	}
	loadMsg := load()
	loaded, ok := loadMsg.(coverLoadedMsg)
	if !ok {
		t.Fatalf("load command returned %T", loadMsg)
	}
	m.handleCoverLoaded(loaded)
	m.recomputeLayout()
	if got := m.artworkURL(playlist.Track{Path: "song.mp3", AlbumArtURL: source}); got != source {
		t.Fatalf("artworkURL = %q, want %q", got, source)
	}

	render := m.ensureCover()
	if render == nil || !m.cover.rendering {
		t.Fatal("ensureCover did not schedule a render")
	}
	renderMsg := render()
	rendered, ok := renderMsg.(coverRenderedMsg)
	if !ok {
		t.Fatalf("render command returned %T", renderMsg)
	}
	m.handleCoverRendered(rendered)
	if m.cover.protocol != "blocks" || !strings.Contains(m.renderCover(), "▀") {
		t.Fatalf("rendered cover protocol=%q block=%q", m.cover.protocol, m.renderCover())
	}
}

func TestArtworkLoadsWhileCoverPaneIsHidden(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 100, 24)
	m.SetShowMetadata(false)
	source := (&url.URL{Scheme: "file", Path: "/tmp/notification-cover.png"}).String()
	track := playlist.Track{Path: "song.mp3", AlbumArtURL: source}
	m.playlist.Replace([]playlist.Track{track})

	load := m.ensureCover()
	if load == nil || !m.cover.loading {
		t.Fatal("hidden cover pane did not schedule artwork loading")
	}
	loaded, ok := load().(coverLoadedMsg)
	if !ok {
		t.Fatal("artwork load returned the wrong message type")
	}
	m.handleCoverLoaded(loaded)
	if got := m.artworkURL(track); got != source {
		t.Fatalf("artworkURL = %q, want %q", got, source)
	}
	if cmd := m.ensureCover(); cmd != nil || m.renderCover() != "" {
		t.Fatal("hidden cover pane rendered artwork")
	}
}

func TestArtworkURLHidesRemoteSourceUntilCached(t *testing.T) {
	m := coverTestModel(t, 100, 24)
	track := playlist.Track{Path: "song.mp3", AlbumArtURL: "https://example.com/cover.jpg?token=secret"}
	if got := m.artworkURL(track); got != "" {
		t.Fatalf("uncached remote artwork URL leaked as %q", got)
	}
	m.cover.key = track.Path + "\x00" + track.AlbumArtURL
	m.cover.local = "file:///tmp/cached-cover.jpg"
	if got := m.artworkURL(track); got != m.cover.local {
		t.Fatalf("cached artworkURL = %q, want %q", got, m.cover.local)
	}
}

func writeCoverPNG(t *testing.T, name string, width, height int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		for y := range height {
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatalf("encode cover: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw.Bytes(), 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func TestCoverColsKeepsAspect(t *testing.T) {
	tests := []struct {
		name                 string
		rows                 int
		aspect, cellW, cellH float64
		want                 int
	}{
		{name: "square in 1:2 cells", rows: 6, aspect: 1, cellW: 10, cellH: 20, want: 12},
		{name: "wide image", rows: 6, aspect: 2, cellW: 10, cellH: 20, want: 24},
		{name: "tall image", rows: 6, aspect: 0.5, cellW: 10, cellH: 20, want: 6},
		{name: "unknown cell size", rows: 5, aspect: 1, want: 10},
		{name: "never zero", rows: 1, aspect: 0.01, cellW: 10, cellH: 20, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coverCols(tt.rows, tt.aspect, tt.cellW, tt.cellH); got != tt.want {
				t.Fatalf("coverCols = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInfoCoverShowsHighlightedTrack(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 120, 30)
	playing := playlist.Track{Path: "playing.mp3", Title: "Playing", AlbumArtURL: writeCoverPNG(t, "playing.png", 4, 4)}
	selected := playlist.Track{
		Path: "/music/selected.mp3", Title: "Selected", Artist: "Artist", Album: "Album",
		Genre: "Genre", Year: 2026, TrackNumber: 3,
		AlbumArtURL: writeCoverPNG(t, "selected.png", 8, 4),
	}
	m.playlist.Replace([]playlist.Track{playing, selected})
	m.plCursor = 1
	m.info.visible = true

	load := m.ensureInfoCover()
	if load == nil || !m.infoCover.loading {
		t.Fatal("ensureInfoCover did not load the highlighted track's cover")
	}
	loaded, ok := load().(coverLoadedMsg)
	if !ok || loaded.slot != coverSlotInfo {
		t.Fatalf("load returned %#v, want an info-slot coverLoadedMsg", loaded)
	}
	m.handleCoverLoaded(loaded)
	if m.infoCover.aspect != 2 {
		t.Fatalf("info cover aspect = %v, want 2 for an 8x4 image", m.infoCover.aspect)
	}

	cols, rows, _, visible := m.infoCoverGeometry()
	if !visible {
		t.Fatal("info cover geometry is hidden in a wide terminal")
	}
	if fields := len(m.infoLines()); rows != min(fields, m.effectivePlaylistVisible()-infoCoverTop) {
		t.Fatalf("cover rows = %d, want the %d field lines", rows, fields)
	}
	cellW, cellH := m.cellSize()
	if want := coverCols(rows, 2, cellW, cellH); cols != want {
		t.Fatalf("cover cols = %d, want %d for a 2:1 image", cols, want)
	}

	render := m.ensureInfoCover()
	if render == nil || !m.infoCover.rendering {
		t.Fatal("ensureInfoCover did not render the loaded cover")
	}
	m.handleCoverRendered(render().(coverRenderedMsg))
	body := m.renderInfoBody()
	if !strings.Contains(body, "▀") || !strings.Contains(body, "Selected") {
		t.Fatalf("info body lacks the cover or the fields:\n%s", body)
	}
	if first, _, _ := strings.Cut(body, "\n"); strings.TrimSpace(first) != "" {
		t.Fatalf("info body starts with %q, want a blank row above the cover", first)
	}
	if m.renderCover() != "" {
		t.Fatal("info cover leaked into the playing-track pane")
	}

	m.info.visible = false
	if cmd := m.ensureInfoCover(); cmd != nil || m.infoCover.key != "" || m.infoCover.render() != "" {
		t.Fatal("closing the info overlay kept its cover")
	}
}

func TestInfoCoverHiddenWhenTooNarrow(t *testing.T) {
	m := coverTestModel(t, 40, 30)
	m.playlist.Replace([]playlist.Track{{Path: "a.mp3", Title: "A", Artist: "B", Album: "C", Genre: "D", Year: 1, TrackNumber: 1}})
	m.info.visible = true
	m.infoCover = coverState{key: "k", local: "file:///cover.png", aspect: 1}
	if _, _, _, visible := m.infoCoverGeometry(); visible {
		t.Fatal("info cover shown in a body too narrow for the fields")
	}
	if got, want := m.infoTextWidth(), m.infoWidth(); got != want {
		t.Fatalf("text width = %d, want full body %d without a cover", got, want)
	}
}

// The full-screen visualizer shows no cover, even with Metadata on.
func TestFullVisShowsNoCover(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	m := coverTestModel(t, 120, 40)
	m.SetCoverArtMode("on")
	m.SetShowMetadata(true)
	m.cover.local = "file:///cover.png"
	m.recomputeLayout()
	if _, _, _, visible := m.coverGeometry(); !visible {
		t.Fatal("main view shows no cover")
	}
	m.fullVis = true
	m.recomputeLayout()
	if _, _, _, visible := m.coverGeometry(); visible {
		t.Fatal("full-screen visualizer shows a cover")
	}
}

func TestAutoCoverWithoutGraphicsKeepsLayout(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	m := coverTestModel(t, 120, 40)
	m.SetCoverArtMode("auto")
	m.cover.local = "file:///cover.png"
	m.recomputeLayout()
	if _, _, _, visible := m.coverGeometry(); visible {
		t.Fatal("auto drew a cover in a terminal without graphics")
	}
	if m.layout.coverRows != m.layout.baseVisualizerRows || m.layout.visualizerRows != m.layout.baseVisualizerRows {
		t.Fatalf("visualizer grew for a hidden cover: cover=%d vis=%d base=%d", m.layout.coverRows, m.layout.visualizerRows, m.layout.baseVisualizerRows)
	}

	m.playlist.Replace([]playlist.Track{{Path: "a.mp3", Title: "A", AlbumArtURL: "file:///cover.png"}})
	m.info.visible = true
	if cmd := m.ensureInfoCover(); cmd != nil || m.infoCover.key != "" {
		t.Fatal("auto loaded an info-overlay cover in a terminal without graphics")
	}
}

// A cover fills its column once the rows hold a square that wide, whatever
// the cell shape, and is narrower below that.
func TestColumnCoverCols(t *testing.T) {
	for _, cell := range [][2]float64{{1, 2}, {9, 20}, {8, 17}, {10, 21}} {
		for width := settingsMinWidth; width <= settingsMaxWidth; width++ {
			rows := squareCoverRows(width, cell[0], cell[1])
			if got := columnCoverCols(rows, width, cell[0], cell[1]); got != width {
				t.Fatalf("cell %v, width %d: %d rows give %d cols, want the column", cell, width, rows, got)
			}
			if got := columnCoverCols(rows-2, width, cell[0], cell[1]); got >= width {
				t.Fatalf("cell %v, width %d: %d rows give %d cols, want narrower", cell, width, rows-2, got)
			}
		}
	}
}

// writeTaggedMP3 writes a file at path that holds only an ID3v2.3 tag with a
// title and the PNG at cover as its front cover.
func writeTaggedMP3(t *testing.T, path, cover string) {
	t.Helper()
	u, err := url.Parse(cover)
	if err != nil {
		t.Fatal(err)
	}
	png, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	frame := func(id string, body []byte) []byte {
		n := len(body)
		return append([]byte{id[0], id[1], id[2], id[3], byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n), 0, 0}, body...)
	}
	// Text frames and APIC start with the text encoding; APIC then names
	// the MIME type, the picture type (3, front cover) and a description.
	body := frame("TIT2", []byte("\x00Tagged"))
	body = append(body, frame("APIC", append([]byte("\x00image/png\x00\x03\x00"), png...))...)
	n := len(body)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	if err := os.WriteFile(path, append(header, body...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The info overlay shows the cover embedded in a local file that is not
// playing: the playlist row does not carry it, so the overlay reads the tags.
// It reads them each time it opens, so a retagged file shows its new cover.
func TestInfoCoverReadsEmbeddedArt(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("HOME", t.TempDir())
	m := coverTestModel(t, 120, 30)
	path := filepath.Join(t.TempDir(), "tagged.mp3")
	m.playlist.Replace([]playlist.Track{{Path: "other.mp3", Title: "Other"}, {Path: path, Title: "Tagged"}})
	m.plCursor = 1

	for _, tt := range []struct {
		width, height int
		aspect        float64
	}{{4, 4, 1}, {8, 4, 2}} {
		writeTaggedMP3(t, path, writeCoverPNG(t, "embedded.png", tt.width, tt.height))
		m.info.visible = false
		m.ensureInfoCover()
		m.info.visible = true
		load := m.ensureInfoCover()
		if load == nil || !m.infoCover.loading {
			t.Fatal("info overlay did not load the embedded cover")
		}
		loaded, ok := load().(coverLoadedMsg)
		if !ok || loaded.err != nil || loaded.local == "" {
			t.Fatalf("load = %#v, want the embedded cover", loaded)
		}
		m.handleCoverLoaded(loaded)
		if m.infoCover.aspect != tt.aspect {
			t.Fatalf("info cover aspect = %v, want %v for a %dx%d image", m.infoCover.aspect, tt.aspect, tt.width, tt.height)
		}
	}
}
