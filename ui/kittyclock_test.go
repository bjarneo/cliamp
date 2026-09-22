package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/theme"
)

func TestExpandImageClockPassesThroughUnmarked(t *testing.T) {
	in := "just a normal visualizer frame\nsecond line"
	if got := ExpandImageClock(in, 10, 80); got != in {
		t.Errorf("unmarked output was altered:\n%q", got)
	}
}

// Without graphics support the plugin's own block rendering must come through
// untouched, marker removed.
func TestExpandImageClockFallsBackWithoutGraphics(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "0")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")

	fallback := "line1\nline2\nline3"
	got := ExpandImageClock("\x00 05:23 \x00"+fallback, 3, 40)
	if got != fallback {
		t.Errorf("fallback = %q, want the block rendering unchanged", got)
	}
	if strings.Contains(got, "\x00") {
		t.Error("marker leaked into the output")
	}
}

func TestExpandImageClockEmitsPlaceholders(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	placements := capturePlacements(t)

	out := ExpandImageClock("\x0005:23\x00fallback", 12, 120)
	if strings.Contains(out, "fallback") {
		t.Fatal("fell back to block rendering despite graphics support")
	}
	if !strings.ContainsRune(out, 0x10EEEE) {
		t.Error("no Unicode placeholders in the output")
	}
	// Placements go to the terminal, not into the frame, because Bubbletea
	// strips graphics escapes from a view.
	if strings.Contains(out, "\x1b_Ga=p") {
		t.Error("placement escape was put in the frame, where Bubbletea eats it")
	}
	if !strings.Contains(placements.String(), "\x1b_Ga=p,U=1") {
		t.Error("no virtual placement was written to the terminal")
	}
	// The rendered block must still fit the panel it was given.
	if n := strings.Count(out, "\n") + 1; n != 12 {
		t.Errorf("clock occupies %d lines, want 12", n)
	}
}

// A panel too small for a legible clock must not emit a broken placement.
func TestExpandImageClockFallsBackWhenTiny(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	if got := ExpandImageClock("\x0005:23\x00tiny", 1, 4); got != "tiny" {
		t.Errorf("got %q, want the fallback on a tiny panel", got)
	}
}

// resetTransmit lets a test observe the transmission, which another test may
// already have consumed.
func resetTransmit(t *testing.T) {
	t.Helper()
	// A sync.Once cannot be copied, so these are re-zeroed rather than saved
	// and restored. Re-transmitting in a later test is harmless.
	reset := func() {
		prepareOnce = sync.Once{}
		masksReady = make(chan []*image.RGBA, 1)
		cachedMasks = nil
		sentTint = color.RGBA{}
		tintBuilding = color.RGBA{}
		select {
		case <-tintEncoded:
		default:
		}
		SetClockTint(color.RGBA{255, 255, 255, 255})
	}
	reset()
	t.Cleanup(reset)
}

func TestTransmitClockGlyphsProducesImages(t *testing.T) {
	resetTransmit(t)
	var buf bytes.Buffer
	TransmitClockGlyphs(&buf)
	out := buf.String()

	if !strings.Contains(out, "\x1b_Ga=t,f=100") {
		t.Fatal("no image transmission escape produced")
	}
	for _, ch := range []string{"i=7310", "i=7319"} {
		if !strings.Contains(out, ch) {
			t.Errorf("missing glyph %s", ch)
		}
	}
	// Transmission happens once; a second call must add nothing.
	before := buf.Len()
	TransmitClockGlyphs(&buf)
	if buf.Len() != before {
		t.Error("glyphs were transmitted twice")
	}
}

func TestClockGraphicsDetection(t *testing.T) {
	for _, tc := range []struct {
		name, term, prog, kitty, override string
		want                              bool
	}{
		{name: "ghostty", term: "xterm-ghostty", want: true},
		{name: "kitty by term", term: "xterm-kitty", want: true},
		{name: "kitty by window id", term: "xterm", kitty: "3", want: true},
		{name: "term program", prog: "ghostty", want: true},
		{name: "plain xterm", term: "xterm-256color", want: false},
		{name: "override off", term: "xterm-ghostty", override: "0", want: false},
		{name: "override on", term: "xterm-256color", override: "1", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Unsetenv("CLIAMP_CLOCK_GRAPHICS")
			t.Setenv("TERM", tc.term)
			t.Setenv("TERM_PROGRAM", tc.prog)
			t.Setenv("KITTY_WINDOW_ID", tc.kitty)
			if tc.override != "" {
				t.Setenv("CLIAMP_CLOCK_GRAPHICS", tc.override)
			}
			if got := ClockGraphicsAvailable(); got != tc.want {
				t.Errorf("ClockGraphicsAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The glyph images must actually contain anti-aliased ink, not be blank.
func TestGlyphRasterHasSmoothEdges(t *testing.T) {
	img := drawClockGlyph('0', 120, 180)
	var ink, partial int
	for y := range 180 {
		for x := range 120 {
			_, _, _, a := img.At(x, y).RGBA()
			a8 := a >> 8
			if a8 > 0 {
				ink++
				if a8 < 250 {
					partial++
				}
			}
		}
	}
	if ink == 0 {
		t.Fatal("glyph rendered blank")
	}
	if partial == 0 {
		t.Error("no partially transparent pixels: edges are not anti-aliased")
	}
	t.Logf("ink=%d partial=%d", ink, partial)
}

// capturePlacements redirects placement escapes and clears the cache, so each
// test sees the placements its own render produces.
func capturePlacements(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer

	placementMu.Lock()
	prevOut, prevMade := placementOut, placementSize
	placementOut, placementSize = &buf, map[int]placement{}
	placementMu.Unlock()

	t.Cleanup(func() {
		placementMu.Lock()
		placementOut, placementSize = prevOut, prevMade
		placementMu.Unlock()
	})
	return &buf
}

// A placement is created once per image and size, not on every frame.
func TestPlacementsAreNotResentEveryFrame(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	placements := capturePlacements(t)

	ExpandImageClock("\x0005:23\x00fallback", 12, 120)
	first := placements.Len()
	if first == 0 {
		t.Fatal("no placements written on the first render")
	}

	for range 5 {
		ExpandImageClock("\x0005:23\x00fallback", 12, 120)
	}
	if placements.Len() != first {
		t.Errorf("placements grew from %d to %d bytes: they are being resent", first, placements.Len())
	}

	// A different panel size genuinely needs new placements.
	ExpandImageClock("\x0005:23\x00fallback", 8, 90)
	if placements.Len() == first {
		t.Error("resizing did not create a new placement")
	}
}

// countInkComponents flood-fills the rasterized glyph and returns how many
// separate blobs of ink it contains.
func countInkComponents(ch rune, w, h int) int {
	img := drawClockGlyph(ch, w, h)
	on := func(x, y int) bool {
		if x < 0 || y < 0 || x >= w || y >= h {
			return false
		}
		return img.Pix[img.PixOffset(x, y)+3] > 128
	}
	seen := make([]bool, w*h)
	comps := 0
	for y := range h {
		for x := range w {
			if !on(x, y) || seen[y*w+x] {
				continue
			}
			comps++
			stack := [][2]int{{x, y}}
			seen[y*w+x] = true
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if on(nx, ny) && !seen[ny*w+nx] {
						seen[ny*w+nx] = true
						stack = append(stack, [2]int{nx, ny})
					}
				}
			}
		}
	}
	return comps
}

// A digit is one continuous shape. More than one blob means a stroke does not
// reach its neighbour, which shows as a broken joint.
func TestGlyphsAreConnected(t *testing.T) {
	want := map[rune]int{
		'0': 1, '1': 1, '2': 1, '3': 1, '4': 1,
		'5': 1, '6': 1, '7': 1, '8': 1, '9': 1,
		'-': 1,
	}
	for ch, expect := range want {
		if got := countInkComponents(ch, 120, 180); got != expect {
			t.Errorf("glyph %q renders as %d blobs, want %d: a joint is broken", ch, got, expect)
		}
	}
}

// The glyph images and the cell box they are placed into must share a digit's
// aspect. If they drift apart the terminal stretches the image to fit and the
// digits come out visibly distorted.
func TestGlyphImageMatchesLayoutAspect(t *testing.T) {
	got := float64(clockGlyphW) / float64(clockGlyphH)
	if diff := got - clockDigitAspect; diff > 0.01 || diff < -0.01 {
		t.Errorf("glyph image aspect %.3f but layout uses %.3f: the image will be stretched",
			got, clockDigitAspect)
	}
}

// Every digit must be placed as soon as the clock is drawn, not when it first
// appears: a placement issued mid-stream can land after the frame that already
// references it, and that digit shows blank for a tick.
func TestAllGlyphsArePlacedUpFront(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	placements := capturePlacements(t)

	ExpandImageClock("\x0012:34\x00fallback", 14, 100)

	out := placements.String()
	for i := range clockGlyphOrder {
		id := clockGlyphBase + i
		if !strings.Contains(out, fmt.Sprintf("i=%d,p=1", id)) {
			t.Errorf("glyph id %d (%q) was not placed", id, clockGlyphOrder[i])
		}
	}
}

// The rendered clock passes through fitVisualizerFrame before display. If a
// placeholder cell is measured as anything but one column, the line is clipped
// and digits vanish.
func TestClockSurvivesFrameFitting(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	capturePlacements(t)

	const rows, cols = 14, 90
	out := ExpandImageClock("\x0012:34\x00fallback", rows, cols)

	before := strings.Count(out, string(rune(0x10EEEE)))
	after := strings.Count(fitVisualizerFrame(out, cols, rows), string(rune(0x10EEEE)))
	if before == 0 {
		t.Fatal("no placeholders produced")
	}
	if after != before {
		t.Errorf("fitting dropped %d of %d placeholder cells: digits will be clipped", before-after, before)
	}
}

// A narrower cut is the only way to make the digits meaningfully taller: four
// digits abreast make the clock width-limited, so the face's advance decides
// how much of the panel height it can use.
func TestClockFontWidthsDifferInAspect(t *testing.T) {
	prev := clockFontChoice
	t.Cleanup(func() {
		clockFontChoice = prev
		resetClockFont()
	})

	aspects := map[string]float64{}
	for _, name := range []string{"bold", "semicondensed", "condensed"} {
		clockFontChoice = name
		resetClockFont()
		initClockFont()
		if clockFont == nil {
			t.Fatalf("%s: font failed to parse", name)
		}
		aspects[name] = clockDigitAspect
	}

	if !(aspects["condensed"] < aspects["semicondensed"] && aspects["semicondensed"] < aspects["bold"]) {
		t.Errorf("widths are not ordered: bold %.3f, semicondensed %.3f, condensed %.3f",
			aspects["bold"], aspects["semicondensed"], aspects["condensed"])
	}
}

func TestSetClockFontIgnoresUnknown(t *testing.T) {
	prev := clockFontChoice
	t.Cleanup(func() { clockFontChoice = prev })

	SetClockFont("condensed")
	SetClockFont("comic-sans")
	if clockFontChoice != "condensed" {
		t.Errorf("clockFontChoice = %q, want an unknown name to be ignored", clockFontChoice)
	}
	SetClockFont("")
	if clockFontChoice != "condensed" {
		t.Errorf("clockFontChoice = %q, want empty to be ignored", clockFontChoice)
	}
}

// The clock should use the height it is given. Keeping the font's proportions
// exactly leaves a width-limited clock stranded in the middle of a tall panel,
// which is the blank space this trades away.
func TestClockUsesMostOfThePanelHeight(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	capturePlacements(t)
	SetClockCellAspect(3.0)

	for _, rows := range []int{18, 24, 28} {
		out := ExpandImageClock("\x0000:00\x00fb", rows, 164)
		box := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.ContainsRune(line, 0x10EEEE) {
				box++
			}
		}
		ink := float64(box) * digitFill
		if used := ink / float64(rows); used < 0.75 {
			t.Errorf("panel %d rows: digits stand %.0f rows, only %.0f%% of the height",
				rows, ink, used*100)
		}
	}
}

func TestSetClockStretchRejectsNonsense(t *testing.T) {
	prev := clockMaxStretch
	t.Cleanup(func() { clockMaxStretch = prev })

	SetClockStretch(1.6)
	for _, bad := range []float64{0, -1, 0.5, 9} {
		SetClockStretch(bad)
		if clockMaxStretch != 1.6 {
			t.Fatalf("SetClockStretch(%v) took effect; want it ignored", bad)
		}
	}
}

// Images outlive the process that sent them. A previous build's glyphs sit in
// the terminal under the same ids, so they must be deleted before the new ones
// are sent or the old digits keep being drawn.
func TestTransmitDeletesStaleImagesFirst(t *testing.T) {
	resetTransmit(t)
	var buf bytes.Buffer
	TransmitClockGlyphs(&buf)
	out := buf.String()

	for i := range clockGlyphOrder {
		del := fmt.Sprintf("\x1b_Ga=d,d=I,i=%d", clockGlyphBase+i)
		if !strings.Contains(out, del) {
			t.Fatalf("no delete issued for image id %d", clockGlyphBase+i)
		}
		send := fmt.Sprintf("i=%d,q=2,m=", clockGlyphBase+i)
		if strings.Index(out, del) > strings.Index(out, send) {
			t.Errorf("image %d is deleted after it is sent", clockGlyphBase+i)
		}
	}
}

// The clock must fill the panel's height without running past its width.
// Stretching the placement box alone does neither: the terminal keeps the
// image's aspect when filling a placement, so a taller box scales the whole
// glyph up and it spills off the right-hand edge.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestClockFitsWidthWhileFillingHeight(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	capturePlacements(t)
	SetClockCellAspect(3.0)

	const cols = 164
	for _, rows := range []int{18, 24, 26} {
		out := ExpandImageClock("\x0000:04\x00fb", rows, cols)

		box, widest := 0, 0
		for _, line := range strings.Split(out, "\n") {
			plain := ansiRe.ReplaceAllString(line, "")
			cells := strings.Count(plain, string(rune(0x10EEEE)))
			if cells == 0 {
				continue
			}
			box++
			if w := cells + strings.Count(plain, " "); w > widest {
				widest = w
			}
		}

		if widest > cols {
			t.Errorf("panel %d rows: line is %d cells wide, overflows %d", rows, widest, cols)
		}
		if used := float64(box) * digitFill / float64(rows); used < 0.85 {
			t.Errorf("panel %d rows: digits use only %.0f%% of the height", rows, used*100)
		}
	}
}

// The UI drops to a 1.5s idle cadence when nothing is playing. A clock counts
// down regardless, so it must keep the UI awake or it visibly skips seconds —
// which is exactly what happens during a pomodoro break, when playback pauses.
func TestClockMarksItselfSelfAnimating(t *testing.T) {
	v := NewVisualizer(44100)
	if v.SelfAnimating() {
		t.Error("a fresh visualizer should not claim to self-animate")
	}

	v.RegisterLuaVisualizers([]string{"clock"}, func(string, [DefaultSpectrumBands]float64, int, int, uint64) string {
		return "\x0005:23\x00fallback rows"
	})
	v.Rows, v.Cols = 12, 100
	d := &luaModeDriver{index: 0}
	d.Render(v)
	if !v.SelfAnimating() {
		t.Error("a rendered clock did not mark itself self-animating")
	}

	// An ordinary visualizer must not keep the UI awake.
	v.RegisterLuaVisualizers([]string{"bars"}, func(string, [DefaultSpectrumBands]float64, int, int, uint64) string {
		return "just bars"
	})
	d.Render(v)
	if v.SelfAnimating() {
		t.Error("an ordinary visualizer claimed to self-animate")
	}
}

// A theme has to reach the digits themselves: the placeholder's foreground
// colour is spent carrying the image id, so the terminal cannot tint them.
func TestClockFollowsThemeColor(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Theme{}) })

	ApplyThemeColors(theme.Theme{
		Name: "test", BG: "#101010", Accent: "#ff8800",
		BrightFG: "#ffffff", FG: "#bbbbbb",
		Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000",
	})
	if got, want := currentClockTint(), (color.RGBA{0xff, 0x88, 0x00, 0xff}); got != want {
		t.Errorf("clock tint = %v, want the theme accent %v", got, want)
	}

	// The block-character fallback is ordinary text, so it takes the colour
	// as a style rather than as pixels.
	styled := ExpandImageClock("\x0005:23\x00fallback", 0, 0)
	if !strings.Contains(styled, "fallback") {
		t.Fatalf("fallback content lost: %q", styled)
	}
	if styled == "fallback" {
		t.Error("themed fallback was not coloured")
	}

	// The default theme leaves both paths alone: its palette is the terminal's
	// own ANSI colours, which say nothing about what reads well at this size.
	ApplyThemeColors(theme.Theme{})
	if got, want := currentClockTint(), (color.RGBA{255, 255, 255, 255}); got != want {
		t.Errorf("default-theme tint = %v, want white %v", got, want)
	}
	if got := ExpandImageClock("\x0005:23\x00fallback", 0, 0); got != "fallback" {
		t.Errorf("default theme coloured the fallback: %q", got)
	}
}

// Re-theming must actually reach the terminal, or the clock keeps the colour
// it was first drawn in until a restart.
func TestClockGlyphsAreResentOnThemeChange(t *testing.T) {
	resetTransmit(t)
	t.Cleanup(func() { ApplyThemeColors(theme.Theme{}) })

	var buf bytes.Buffer
	TransmitClockGlyphs(&buf)
	if buf.Len() == 0 {
		t.Fatal("no initial transmission")
	}

	ApplyThemeColors(theme.Theme{
		Name: "test", BG: "#101010", Accent: "#ff8800",
		BrightFG: "#ffffff", FG: "#bbbbbb",
		Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000",
	})

	// The re-tint is encoded off the render goroutine, so the frame that asks
	// for it only starts the work; a later frame writes the result.
	buf.Reset()
	deadline := time.Now().Add(10 * time.Second)
	for buf.Len() == 0 && time.Now().Before(deadline) {
		TransmitClockGlyphs(&buf)
		time.Sleep(10 * time.Millisecond)
	}
	if buf.Len() == 0 {
		t.Fatal("theme change never re-sent the glyphs")
	}
	if !strings.Contains(buf.String(), "\x1b_Ga=t,f=100") {
		t.Error("re-send carried no image data")
	}
}

// A theme change re-sends the glyphs, and every send deletes the old images,
// placements included. The digits must be placed again afterwards, or the
// clock goes blank after the first theme switch.
func TestThemeChangeReplacesPlacements(t *testing.T) {
	t.Setenv("CLIAMP_CLOCK_GRAPHICS", "1")
	resetTransmit(t)
	out := capturePlacements(t)

	ExpandImageClock("\x0005:23\x00fallback", 12, 120)
	if !strings.Contains(out.String(), "a=p") {
		t.Fatal("no placements on the first render")
	}

	SetClockTint(color.RGBA{0x7a, 0xa2, 0xf7, 255}) // a theme's accent
	out.Reset()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "a=d") && time.Now().Before(deadline) {
		ExpandImageClock("\x0005:24\x00fallback", 12, 120) // collects the async re-tint
		time.Sleep(10 * time.Millisecond)
	}
	sent := out.String()
	del := strings.LastIndex(sent, "a=d")
	if del < 0 {
		t.Fatal("the re-tinted glyphs were never sent")
	}
	if !strings.Contains(sent[del:], "a=p") {
		t.Error("glyphs were re-sent (deleting their placements) but not placed again")
	}
}
