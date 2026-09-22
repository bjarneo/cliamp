package ui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"time"
)

// Drawing the pomodoro clock as a real image rather than block characters.
//
// A terminal cell is the smallest thing text can colour, and on a typical
// setup that is a rectangle of roughly 190 screen pixels. No arrangement of
// block characters escapes that: curves step in whole cells, and shading the
// edges only trades a staircase for visible grey blocks.
//
// The kitty graphics protocol draws at real screen resolution instead. Raw
// graphics escapes do not survive Bubbletea's cell renderer, but *Unicode
// placeholders* do: the image is transmitted out of band, and the view then
// prints ordinary characters that the terminal composites the image behind.
// Bubbletea only ever sees text.
//
// Each glyph is transmitted once, before the program starts, so nothing is
// ever written to the terminal while Bubbletea owns it. Changing the time
// afterwards only changes which placeholders are printed.

const (
	// clockGlyphBase is the first image id. Ids must not collide with other
	// programs sharing the terminal, so this sits well away from small numbers.
	clockGlyphBase = 7310

	// Transmitted glyph height. The terminal scales this down to the cell box,
	// which stays sharp because it is scaling pixels, not characters. The
	// width is derived from the font's digit advance in initClockFont.
	clockGlyphH = 720
)

// clockGlyphOrder is the set of glyphs transmitted, in image-id order. The
// colon is absent deliberately: a flip clock separates the pairs with a gap,
// so ':' is laid out as empty space rather than drawn.
var clockGlyphOrder = []rune{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-'}

// rowColumnDiacritics encodes a placeholder's row and column, from the kitty
// graphics specification. Only as many as a panel can need are listed.
var rowColumnDiacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611,
	0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
}

// clockGlyphAdvance is a glyph's width relative to a digit's. The colon is a
// gap, wide enough to read as a separator without a mark.
var clockGlyphAdvance = map[rune]float64{':': 0.30}

// Three widths of the same face. Four digits abreast make the clock
// width-limited on a typical panel, so a narrower cut is the only way to make
// the digits meaningfully taller — the trade is proportions against height.
//
//go:embed fonts/Barlow-Bold.ttf
var clockFontBold []byte

//go:embed fonts/BarlowSemiCondensed-Bold.ttf
var clockFontSemiCondensed []byte

//go:embed fonts/BarlowCondensed-Bold.ttf
var clockFontCondensed []byte

var clockFontChoice = "semicondensed"

// SetClockFont picks the face width: "bold", "semicondensed" or "condensed".
// Anything else is ignored.
func SetClockFont(name string) {
	switch name {
	case "bold", "semicondensed", "condensed":
		clockFontChoice = name
	}
}

// resetClockFont clears the parsed face so a different width takes effect.
// Only the font choice changes at runtime, and only in tests.
func resetClockFont() {
	clockFaceMu.Lock()
	defer clockFaceMu.Unlock()
	clockFontOnce = sync.Once{}
	clockFont = nil
	clockFaces = map[int]font.Face{}
}

func clockFontTTF() []byte {
	switch clockFontChoice {
	case "bold":
		return clockFontBold
	case "condensed":
		return clockFontCondensed
	default:
		return clockFontSemiCondensed
	}
}

// digitFill is how much of a glyph image's height a digit stands at. Anything
// less is padding baked into the image, which the terminal then scales up along
// with the digit — so it shows as blank rows above and below the clock.
const digitFill = 0.97

// clockGapFraction is the space between glyphs, as a fraction of a digit's
// width. Four digits abreast make the clock width-limited on a typical panel,
// so every column spent on gaps is height the digits do not get.
const clockGapFraction = 0.05

// clockMaxStretch is how much taller than its natural proportions the clock is
// drawn. At 1.35 the digits stand at roughly 85-90% of a typical panel; 1.0
// keeps the typeface exactly and leaves the rest blank.
//
// Once four digits fill the width, keeping the font's proportions leaves the
// rest of the panel blank however tall it is. Condensing the glyphs is the only
// way to use that height.
var clockMaxStretch = 1.35

// SetClockStretch overrides how far the clock may stretch beyond the font's
// proportions. 1.0 keeps them exactly; values outside 1.0–2.5 are ignored.
func SetClockStretch(v float64) {
	if v >= 1.0 && v <= 2.5 {
		clockMaxStretch = v
		resetClockFont()
	}
}

var (
	clockFontOnce sync.Once
	clockFont     *opentype.Font
	// Digit metrics at a reference size, used to derive both the box aspect
	// and the face size for any box height.
	probeDigitH  float64
	probeAdvance float64
	clockFaceMu  sync.Mutex
	clockFaces   = map[int]font.Face{}

	// clockDigitAspect is a digit's advance divided by its box height. Taken
	// from the font's own metrics rather than guessed, so the images and the
	// cell box they are placed into agree and the terminal does not stretch
	// them to fit.
	clockDigitAspect = 0.62
	clockGlyphW      = 446
)

const clockProbeSize = 100.0

func initClockFont() {
	clockFontOnce.Do(func() {
		f, err := opentype.Parse(clockFontTTF())
		if err != nil {
			return
		}
		probe, err := opentype.NewFace(f, &opentype.FaceOptions{Size: clockProbeSize, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return
		}
		defer probe.Close()

		bounds, adv, ok := probe.GlyphBounds('0')
		if !ok {
			return
		}
		probeDigitH = float64(bounds.Max.Y-bounds.Min.Y) / 64
		probeAdvance = float64(adv) / 64
		if probeDigitH <= 0 || probeAdvance <= 0 {
			return
		}

		clockFont = f
		// Size cancels out: this ratio holds for any box height. Dividing by
		// the stretch narrows the glyph box, which is how the digits are made
		// taller relative to their width — the terminal keeps the image's
		// aspect when filling a placement, so a taller box alone would just
		// scale the whole glyph up and overflow sideways.
		clockDigitAspect = probeAdvance * digitFill / probeDigitH / clockMaxStretch
		clockGlyphW = int(float64(clockGlyphH)*clockDigitAspect + 0.5)
	})
}

// faceForHeight returns a face sized so a digit fills digitFill of a box h tall.
// Faces are cached per height and reused; a font.Face keeps rasterizer state and
// must not be shared across goroutines, so callers are serialized.
func faceForHeight(h int) font.Face {
	initClockFont()
	if clockFont == nil || h <= 0 {
		return nil
	}

	clockFaceMu.Lock()
	defer clockFaceMu.Unlock()
	if f, ok := clockFaces[h]; ok {
		return f
	}
	size := clockProbeSize * (float64(h) * digitFill) / probeDigitH
	face, err := opentype.NewFace(clockFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	clockFaces[h] = face
	return face
}

// drawClockGlyph renders one character white-on-transparent, centred in the box.
func drawClockGlyph(ch rune, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	face := faceForHeight(h)
	if face == nil {
		return img
	}

	clockFaceMu.Lock()
	defer clockFaceMu.Unlock()

	bounds, adv, ok := face.GlyphBounds(ch)
	if !ok {
		return img
	}
	natural := max(1, int(float64(adv)/64+0.5))

	// Centre the glyph's own ink in the box so digits and the dash sit
	// consistently, rather than trusting a shared baseline.
	inkTop := float64(bounds.Min.Y) / 64
	inkBottom := float64(bounds.Max.Y) / 64
	baseline := (float64(h)-(inkBottom-inkTop))/2 - inkTop

	// Drawn at its natural width first, then squeezed into the target width.
	// Type cannot be condensed by drawing it into a narrower box — it would
	// simply overflow — so it is rendered and then resampled.
	tmp := image.NewRGBA(image.Rect(0, 0, natural, h))
	d := &font.Drawer{
		Dst:  tmp,
		Src:  image.NewUniform(color.RGBA{255, 255, 255, 255}),
		Face: face,
		Dot:  fixed.Point26_6{Y: fixed.Int26_6(baseline * 64)},
	}
	d.DrawString(string(ch))

	draw.ApproxBiLinear.Scale(img, img.Bounds(), tmp, tmp.Bounds(), draw.Over, nil)
	return img
}

// ClockGraphicsAvailable reports whether this terminal can draw the image
// clock. Only terminals known to implement the protocol's Unicode placeholders
// are accepted; guessing wrong would print a screen of stray characters.
func ClockGraphicsAvailable() bool {
	if v, ok := os.LookupEnv("CLIAMP_CLOCK_GRAPHICS"); ok {
		return v == "1" || strings.EqualFold(v, "true")
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	prog := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	for _, name := range []string{"kitty", "ghostty"} {
		if strings.Contains(term, name) || strings.Contains(prog, name) {
			return true
		}
	}
	return false
}

// The clock's colour has to live in the pixels. A placeholder cell spends its
// foreground colour carrying the image id, so the terminal cannot tint the
// digits for us — a theme change means sending different images.
//
// The outlines are therefore rasterized once into white masks, and only the
// tinting and PNG encoding are repeated per colour. That is the cheap half:
// scaling a mask by a colour is a pass over the pixels, where re-rasterizing
// would walk the font outlines again.
var (
	clockTintMu sync.Mutex
	clockTint   = color.RGBA{255, 255, 255, 255}
)

// SetClockTint records the colour the clock digits should be drawn in. The
// next frame re-sends the glyphs if the terminal is holding another colour.
func SetClockTint(c color.Color) {
	if c == nil {
		c = color.RGBA{255, 255, 255, 255}
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return
	}
	clockTintMu.Lock()
	defer clockTintMu.Unlock()
	clockTint = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
}

func currentClockTint() color.RGBA {
	clockTintMu.Lock()
	defer clockTintMu.Unlock()
	return clockTint
}

var (
	prepareOnce sync.Once
	masksReady  = make(chan []*image.RGBA, 1)
)

// PrepareClockGlyphs starts rasterizing the glyph masks in the background.
//
// Rasterizing all of them takes a few hundred milliseconds, which is worth
// hiding: called early in startup it overlaps with provider and audio setup,
// so TransmitClockGlyphs later usually finds the work already done.
func PrepareClockGlyphs() {
	prepareOnce.Do(func() {
		go func() { masksReady <- buildClockMasks() }()
	})
}

// buildClockMasks rasterizes each glyph white-on-transparent. The result is
// premultiplied, so every channel already equals the coverage — which is what
// makes tinting a single multiply.
func buildClockMasks() []*image.RGBA {
	initClockFont()
	masks := make([]*image.RGBA, len(clockGlyphOrder))
	for i, ch := range clockGlyphOrder {
		gw := int(float64(clockGlyphW) * glyphAdvance(ch))
		if gw < 8 {
			gw = 8
		}
		masks[i] = drawClockGlyph(ch, gw, clockGlyphH)
	}
	return masks
}

// tintGlyph scales a white mask to the given colour, keeping it premultiplied.
func tintGlyph(mask *image.RGBA, c color.RGBA) *image.RGBA {
	if c.R == 255 && c.G == 255 && c.B == 255 {
		return mask
	}
	out := image.NewRGBA(mask.Bounds())
	copy(out.Pix, mask.Pix)
	for i := 0; i < len(out.Pix); i += 4 {
		a := uint32(out.Pix[i+3])
		out.Pix[i] = uint8(a * uint32(c.R) / 255)
		out.Pix[i+1] = uint8(a * uint32(c.G) / 255)
		out.Pix[i+2] = uint8(a * uint32(c.B) / 255)
	}
	return out
}

func encodeClockGlyphs(masks []*image.RGBA, tint color.RGBA) []byte {
	// Rendered one at a time: a font.Face keeps internal rasterizer buffers
	// and is not safe to share across goroutines. Real type is fast enough
	var out bytes.Buffer

	// Delete anything already stored under these ids first. Images outlive the
	// process that sent them, so a previous build's glyphs sit in the terminal
	// under the same ids — and a placement pointing at stale image data shows
	// the old digits however the box is sized. Without this, changing the
	// glyphs appears to do nothing until the terminal is restarted.
	for i := range clockGlyphOrder {
		fmt.Fprintf(&out, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", clockGlyphBase+i)
	}

	for i := range clockGlyphOrder {
		if i >= len(masks) || masks[i] == nil {
			continue
		}
		// Fast compression: this is on the startup path and the data goes to a
		// local terminal, so encode time matters more than size.
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		var enc bytes.Buffer
		if err := encoder.Encode(&enc, tintGlyph(masks[i], tint)); err != nil {
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(enc.Bytes())

		id := clockGlyphBase + i
		const chunk = 4000
		for p := 0; p < len(b64); p += chunk {
			end := min(p+chunk, len(b64))
			more := 0
			if end < len(b64) {
				more = 1
			}
			if p == 0 {
				fmt.Fprintf(&out, "\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", id, more, b64[p:end])
			} else {
				fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, b64[p:end])
			}
		}
	}
	return out.Bytes()
}

type encodedGlyphs struct {
	tint color.RGBA
	data []byte
}

var (
	// sentTint is the colour the terminal is currently holding. Its zero value
	// has alpha 0, which no real tint has, so it also means "nothing sent yet".
	sentTint     color.RGBA
	cachedMasks  []*image.RGBA
	tintBuilding color.RGBA
	tintEncoded  = make(chan encodedGlyphs, 1)
)

// TransmitClockGlyphs writes the glyph images to the terminal, re-sending them
// when the theme has changed the clock's colour.
//
// This cannot be done before the program starts: entering the alternate screen
// discards stored images, so anything sent beforehand is gone by the time the
// clock is drawn. It is therefore sent from the render path, alongside the
// placements — which is safe because a view is built on the same goroutine
// that writes the frame, so this lands before the frame rather than inside it.
//
// Only the first send blocks. A later re-tint is encoded on another goroutine
// and picked up by a subsequent frame, so stepping through the theme picker
// costs the clock a frame in the old colour rather than stalling the UI on a
// PNG encode per keystroke. The write itself always happens here, never off
// the render goroutine.
func TransmitClockGlyphs(w io.Writer) {
	// Collect a finished encode, if one is waiting.
	select {
	case done := <-tintEncoded:
		tintBuilding = color.RGBA{}
		_, _ = w.Write(done.data)
		forgetPlacements(clockGlyphIDs()...)
		sentTint = done.tint
	default:
	}

	want := currentClockTint()
	if sentTint == want || tintBuilding == want {
		return
	}

	if cachedMasks == nil {
		PrepareClockGlyphs()
		cachedMasks = <-masksReady
	}
	if sentTint == (color.RGBA{}) {
		// Nothing on screen yet: an async first send would leave the clock
		// blank for a frame, which on a one-second clock reads as a fault.
		_, _ = w.Write(encodeClockGlyphs(cachedMasks, want))
		forgetPlacements(clockGlyphIDs()...)
		sentTint = want
		return
	}

	tintBuilding = want
	masks := cachedMasks
	go func() { tintEncoded <- encodedGlyphs{want, encodeClockGlyphs(masks, want)} }()
}

// clockGlyphIDs lists the image ids the glyphs occupy.
func clockGlyphIDs() []int {
	ids := make([]int, len(clockGlyphOrder))
	for i := range clockGlyphOrder {
		ids[i] = clockGlyphBase + i
	}
	return ids
}

func glyphAdvance(ch rune) float64 {
	if a, ok := clockGlyphAdvance[ch]; ok {
		return a
	}
	return 1
}

func clockGlyphID(ch rune) (int, bool) {
	for i, g := range clockGlyphOrder {
		if g == ch {
			return clockGlyphBase + i, true
		}
	}
	return 0, false
}

var (
	placementMu sync.Mutex
	// What each image is currently placed as, so a resize replaces the
	// placement rather than adding a second one.
	placementSize = map[int]placement{}
	// placementOut is the terminal; overridden in tests.
	placementOut io.Writer = os.Stdout
)

// placement is one image's current placement and when it was last issued.
type placement struct {
	cols, rows int
	at         time.Time
}

// placementRefresh re-issues a placement that has stood for this long. The
// terminal can drop placements without telling us — a repaint on pause did
// exactly that, and the picture stayed gone because we believed it was still
// placed. Re-issuing is a few dozen bytes, so it is cheaper than being wrong.
var placementRefresh = 2 * time.Second

// forgetPlacements drops the record of what is placed for the given image ids.
// A send starts by deleting the old image, and deleting an image deletes its
// placements too, so afterwards those ids are no longer placed. Without this a
// theme change re-sent the clock glyphs and ensurePlacement, believing them
// still placed, never placed them again: the clock went blank until restart.
//
// Only the ids that were actually re-sent are forgotten. Clearing the lot
// would make one image's refresh blank every other image on screen — the cover
// in the settings pane losing its placement whenever the visualizer's copy was
// re-sent, and vice versa.
func forgetPlacements(ids ...int) {
	placementMu.Lock()
	defer placementMu.Unlock()
	for _, id := range ids {
		delete(placementSize, id)
	}
}

// SetGraphicsOutput redirects the graphics escapes, which otherwise go
// straight to the terminal. Tests point it at a buffer so a test run does not
// paint images over the terminal that started it.
func SetGraphicsOutput(w io.Writer) func() {
	placementMu.Lock()
	previous := placementOut
	placementOut = w
	placementMu.Unlock()
	return func() {
		placementMu.Lock()
		placementOut = previous
		placementMu.Unlock()
	}
}

// ensurePlacement creates the virtual placement mapping an image onto a cell
// box of the given size.
//
// This cannot go in the rendered frame: Bubbletea's cell renderer strips
// graphics escapes, which is why placeholders alone show nothing. It is
// therefore written straight to the terminal — but only the first time a given
// image and size are needed, so it happens on the first draw and on a resize,
// never per frame. The escape is a few dozen bytes written in one call.
func ensurePlacement(id, cols, rows int) {
	placementMu.Lock()
	defer placementMu.Unlock()

	if got, ok := placementSize[id]; ok && got.cols == cols && got.rows == rows &&
		time.Since(got.at) < placementRefresh {
		return
	}
	placementSize[id] = placement{cols: cols, rows: rows, at: time.Now()}

	// An explicit placement id matters: without one the terminal accumulates a
	// separate placement per size, and a placeholder cannot say which it means,
	// so digits keep whatever size they were first drawn at. Re-issuing id 1
	// replaces it instead.
	fmt.Fprintf(placementOut, "\x1b_Ga=p,U=1,i=%d,p=1,c=%d,r=%d,q=2\x1b\\", id, cols, rows)
}

// clockRow renders one text row of the clock as placeholder cells.
func clockRow(text string, row, glyphCols, gap int) string {
	var sb strings.Builder
	for i, ch := range text {
		cols := glyphCols
		if adv := glyphAdvance(ch); adv < 1 {
			cols = max(1, int(float64(glyphCols)*adv))
		}
		if i > 0 {
			sb.WriteString(strings.Repeat(" ", gap))
		}
		id, ok := clockGlyphID(ch)
		if !ok {
			// No image for this glyph — the colon — so it is simply space.
			sb.WriteString(strings.Repeat(" ", cols))
			continue
		}
		// The image id travels in the foreground colour.
		fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm", (id>>16)&0xFF, (id>>8)&0xFF, id&0xFF)
		for c := range cols {
			if row >= len(rowColumnDiacritics) || c >= len(rowColumnDiacritics) {
				break
			}
			sb.WriteRune(0x10EEEE)
			sb.WriteRune(rowColumnDiacritics[row])
			sb.WriteRune(rowColumnDiacritics[c])
		}
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

// ClockMarker wraps the time a plugin wants drawn, e.g. "\x00 05:23 \x00".
// Plugins get it from cliamp.clock() rather than writing it out; see
// luaplugin.ClockMarker, which must stay the same string.
const ClockMarker = "\x00"

// ExpandImageClock replaces a plugin's clock marker with placeholder cells that
// the terminal fills with real images. The marker carries the time to show, and
// everything after it is the plugin's own block-character rendering, used as-is
// when this terminal cannot draw images.
func ExpandImageClock(out string, rows, cols int) string {
	if !strings.HasPrefix(out, ClockMarker) {
		return out
	}
	rest := out[len(ClockMarker):]
	end := strings.Index(rest, ClockMarker)
	if end < 0 {
		return out
	}
	text := rest[:end]
	fallback := rest[end+len(ClockMarker):]

	if !ClockGraphicsAvailable() || rows <= 0 || cols <= 0 {
		return styleClockFallback(fallback)
	}
	return renderImageClock(text, rows, cols, fallback)
}

func renderImageClock(text string, rows, cols int, fallback string) string {
	if text == "" {
		return styleClockFallback(fallback)
	}

	// Size the glyphs to the panel, keeping a digit's proportions. A digit is
	// about 0.62 as wide as it is tall; cells are far taller than they are
	// wide, so the cell aspect decides how many columns that comes to.
	units := 0.0
	for _, ch := range text {
		units += glyphAdvance(ch)
	}
	gaps := float64(max(0, len([]rune(text))-1))

	glyphRows := rows
	glyphCols := 0
	for glyphRows > 0 {
		initClockFont()
		width := clockDigitAspect * float64(glyphRows) * clockAspect()
		gap := max(1.0, width*clockGapFraction)
		if width*units+gap*gaps <= float64(cols) {
			glyphCols = int(width)
			break
		}
		glyphRows--
	}
	if glyphCols < 2 || glyphRows < 2 {
		return styleClockFallback(fallback)
	}
	gap := max(1, int(float64(glyphCols)*clockGapFraction))

	// The images themselves must be (re)sent from here rather than at startup:
	// entering the alternate screen discards them.
	TransmitClockGlyphs(placementOut)

	// Place every glyph, not just the ones on screen now. A placement written
	// when a digit first appears can reach the terminal after the frame that
	// already contains its placeholders, leaving that digit blank for a tick —
	// which on a one-second clock looks like a skipped number.
	for _, ch := range clockGlyphOrder {
		if id, ok := clockGlyphID(ch); ok {
			ensurePlacement(id, glyphCols, glyphRows)
		}
	}

	// Measure the laid-out width so the block can be centred.
	total := 0
	for i, ch := range text {
		c := glyphCols
		if adv := glyphAdvance(ch); adv < 1 {
			c = max(1, int(float64(glyphCols)*adv))
		}
		total += c
		if i > 0 {
			total += gap
		}
	}
	pad := max(0, (cols-total)/2)

	lines := make([]string, 0, rows)
	top := max(0, (rows-glyphRows)/2)
	for range top {
		lines = append(lines, "")
	}
	for r := range glyphRows {
		lines = append(lines, strings.Repeat(" ", pad)+clockRow(text, r, glyphCols, gap))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}

	return strings.Join(lines[:rows], "\n")
}

// clockCellAspect is the terminal's cell height divided by its width, used to
// keep the digits from being stretched. Zero means measure it.
var (
	clockCellAspect   = 0.0
	clockAspectOnce   sync.Once
	clockAspectCached = 2.0
)

// clockAspect is the cell aspect the clock sizes itself by: an explicit
// setting if there is one, otherwise the terminal's own, which the window
// size ioctl reports. Falling back to a constant is a last resort — a guessed
// aspect is what makes big digits come out stretched — so it only applies
// where the terminal does not report its size in pixels, as over plain ssh.
func clockAspect() float64 {
	if clockCellAspect > 0 {
		return clockCellAspect
	}
	clockAspectOnce.Do(func() {
		if a := terminalCellAspect(); a > 0 {
			clockAspectCached = a
		}
	})
	return clockAspectCached
}

// SetClockCellAspect overrides the measured cell aspect. Out-of-range values
// are ignored rather than producing a badly distorted clock.
func SetClockCellAspect(a float64) {
	if a >= 1.0 && a <= 5.0 {
		clockCellAspect = a
	}
}

// clockTextStyle colours the block-character clock, which is ordinary text and
// so can simply be styled. Zero value on the default theme: the digits then
// keep the terminal's own foreground, matching the white the image path draws.
var clockTextStyle lipgloss.Style

// applyClockTheme points both clock renderers at the theme's clock colour.
func applyClockTheme(c color.Color) {
	if c == nil {
		clockTextStyle = lipgloss.Style{}
		SetClockTint(color.RGBA{255, 255, 255, 255})
		return
	}
	clockTextStyle = lipgloss.NewStyle().Foreground(c)
	SetClockTint(c)
}

// styleClockFallback colours the plugin's own block rendering. The visualizer's
// line fitting decodes escape sequences, so the added colour does not disturb
// its width accounting.
func styleClockFallback(s string) string {
	if s == "" || ColorClock == nil {
		return s
	}
	return clockTextStyle.Render(s)
}
