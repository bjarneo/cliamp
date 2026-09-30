package termimg

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func TestLayerRenderDiffs(t *testing.T) {
	a := Placement{Key: "a", X: 1, Y: 2, W: 4, H: 2, Data: []byte("<A>")}
	b := Placement{Key: "b", X: 10, Y: 2, W: 4, H: 2, Data: []byte("<B>")}
	tests := []struct {
		name      string
		want      []Placement
		full      bool
		pre, post string // substrings expected; "" means empty
	}{
		{"first draw", []Placement{a}, false, "", "\x1b[3;2H<A>"},
		{"unchanged draws nothing", []Placement{a}, false, "", ""},
		{"added image draws only it", []Placement{a, b}, false, "", "\x1b[3;11H<B>"},
		{"removed image is erased", []Placement{b}, false, "\x1b[3;2H\x1b[4X\x1b[4;2H\x1b[4X", ""},
		{"full repaint redraws all", []Placement{b}, true, "", "<B>"},
		{"clear erases the rest", nil, false, "\x1b[3;11H\x1b[4X", ""},
	}
	l := NewLayer()
	for _, tt := range tests {
		l.Set(tt.want)
		pre, post := l.render(tt.full)
		if (tt.pre == "") != (len(pre) == 0) || !bytes.Contains(pre, []byte(tt.pre)) {
			t.Fatalf("%s: pre = %q, want %q", tt.name, pre, tt.pre)
		}
		if (tt.post == "") != (len(post) == 0) || !bytes.Contains(post, []byte(tt.post)) {
			t.Fatalf("%s: post = %q, want %q", tt.name, post, tt.post)
		}
	}
}

func TestWriterSplicesAroundFrameText(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := NewLayer()
	w := NewWriter(f, l)
	old := Placement{Key: "old", X: 0, Y: 0, W: 2, H: 1, Data: []byte("<OLD>")}
	l.Set([]Placement{old})
	frame := "\x1b[?2026hTEXT\x1b[?2026l"
	if _, err := w.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	l.Set([]Placement{{Key: "new", X: 5, Y: 0, W: 2, H: 1, Data: []byte("<NEW>")}})
	if _, err := w.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(f.Name())
	second := string(out[strings.LastIndex(string(out), "\x1b[?2026h"):])
	erase, text, draw := strings.Index(second, "\x1b[2X"), strings.Index(second, "TEXT"), strings.Index(second, "<NEW>")
	if erase < 0 || text < 0 || draw < 0 || !(erase < text && text < draw) {
		t.Fatalf("frame order wrong (erase %d, text %d, draw %d): %q", erase, text, draw, second)
	}
	if !strings.HasSuffix(second, "\x1b[?2026l") {
		t.Fatalf("images must stay inside the synchronized update: %q", second)
	}
}

func TestWriterPassesUnchangedWritesThrough(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := NewLayer()
	l.Set([]Placement{{Key: "a", W: 1, H: 1, Data: []byte("<A>")}})
	w := NewWriter(f, l)
	_, _ = w.Write([]byte("x")) // first write draws the image
	before, _ := os.ReadFile(f.Name())
	_, _ = w.Write([]byte("\x1b[?25l")) // nothing changed since
	after, _ := os.ReadFile(f.Name())
	if !strings.Contains(string(before), "<A>") || string(after[len(before):]) != "\x1b[?25l" {
		t.Fatalf("first write %q, second write %q", before, after[len(before):])
	}
}

// With no image wanted, armed, or drawn, frames go out byte for byte and
// the layer's frame bookkeeping never runs.
func TestWriterIdleLayerPassesFramesThrough(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := NewLayer()
	l.Invalidate() // a resize while idle must not make the layer draw
	w := NewWriter(f, l)
	frame := "\x1b[?2026h\x1b[2JTEXT\x1b[?2026l"
	if _, err := w.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("more"); err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(f.Name()); string(out) != frame+"more" {
		t.Fatalf("idle output altered: %q", out)
	}
	if l.active() {
		t.Fatal("idle layer reports active")
	}
}

// The layer stays engaged after the last image is removed until the frame
// that erases it, then drops back to pass-through.
func TestLayerDisengagesAfterErasingLastImage(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := NewLayer()
	w := NewWriter(f, l)
	l.Set([]Placement{{Key: "a", X: 0, Y: 0, W: 3, H: 1, Data: []byte("<A>")}})
	_, _ = w.Write([]byte("x"))
	l.Set(nil)
	if !l.active() {
		t.Fatal("layer disengaged before erasing the drawn image")
	}
	_, _ = w.Write([]byte("y"))
	if l.active() {
		t.Fatal("layer still engaged after erasing the last image")
	}
	if out, _ := os.ReadFile(f.Name()); !strings.Contains(string(out), "\x1b[3X") {
		t.Fatalf("removed image not erased: %q", out)
	}
	l.ArmLive(0, 0, 4, 2)
	if !l.active() {
		t.Fatal("armed live rectangle must engage the layer")
	}
	l.ArmLive(0, 0, 0, 0)
	if l.active() {
		t.Fatal("disarming with nothing drawn must disengage the layer")
	}
}

// Leaving a screen with images: Bubbletea's diff often ends with an
// erase-below, which must not stop the layer from erasing what went away.
func TestLayerErasesRemovedImagesEvenWhenFrameClears(t *testing.T) {
	l := NewLayer()
	l.Set([]Placement{{Key: "a", X: 2, Y: 3, W: 5, H: 1, Data: []byte("<A>")}})
	l.render(false)
	l.Set(nil)
	if pre, _ := l.render(true); !bytes.Contains(pre, []byte("\x1b[4;3H\x1b[5X")) {
		t.Fatalf("removed image not erased: %q", pre)
	}
}

func TestFillCoverCropsToAspect(t *testing.T) {
	// Left half red, right half blue, 200x100; a square fill keeps the
	// center, so both colors survive side by side.
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 100 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	dst := Fill(src, 10, 10)
	if dst.Rect.Dx() != 10 || dst.Rect.Dy() != 10 {
		t.Fatalf("size = %v", dst.Rect)
	}
	if l, r := dst.RGBAAt(0, 5), dst.RGBAAt(9, 5); l.R < 200 || r.B < 200 {
		t.Fatalf("crop lost a side: left %v right %v", l, r)
	}
}

func TestHalfBlocksLineShape(t *testing.T) {
	lines := HalfBlocks(solid(3, 4, color.RGBA{10, 20, 30, 255}))
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, l := range lines {
		if strings.Count(l, "▀") != 3 || !strings.Contains(l, "38;2;10;20;30") {
			t.Fatalf("bad line %q", l)
		}
	}
}

func TestEncodeSixelFraming(t *testing.T) {
	out := EncodeSixel(solid(12, 12, color.RGBA{200, 50, 50, 255}))
	s := string(out)
	if !strings.HasPrefix(s, "\x1bP0;1;0q\"1;1;12;12") || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("bad sixel framing: %q", s)
	}
	if strings.Count(s, "-") != 2 { // two six-pixel bands
		t.Fatalf("bands = %d, want 2: %q", strings.Count(s, "-"), s)
	}
}

func TestLayerLiveFrames(t *testing.T) {
	l := NewLayer()
	frame := func(key string) Placement {
		return Placement{Key: key, X: 0, Y: 0, W: 10, H: 2, Data: []byte("<" + key + ">")}
	}
	l.SetLive(frame("early"))
	if _, post := l.render(false); len(post) != 0 {
		t.Fatalf("live frame accepted before its rect was armed: %q", post)
	}
	l.ArmLive(0, 0, 10, 2)
	l.SetLive(frame("v1"))
	if got := l.renderLive(); got != nil {
		t.Fatalf("live frame drawn before a text frame blanked its cells: %q", got)
	}
	if _, post := l.render(false); !bytes.Contains(post, []byte("<v1>")) {
		t.Fatalf("frame did not draw the live placement: %q", post)
	}
	l.SetLive(frame("v2"))
	if got := l.renderLive(); !bytes.Contains(got, []byte("<v2>")) {
		t.Fatalf("next live frame not drawn between frames: %q", got)
	}
	if pre, _ := l.render(false); len(pre) != 0 {
		t.Fatalf("an animated rect must not be erased between its frames: %q", pre)
	}
	l.ArmLive(0, 0, 0, 0)
	l.SetLive(frame("late")) // a worker finishing after the UI moved on
	if got := l.renderLive(); got != nil {
		t.Fatalf("late live frame drawn after disarm: %q", got)
	}
	if pre, _ := l.render(false); !bytes.Contains(pre, []byte("\x1b[10X")) {
		t.Fatalf("stopping the animation must erase its rect: %q", pre)
	}
}

// A text rewrite on a row an image crosses wipes the image, so the layer
// redraws it even though the placement itself did not change.
func TestLayerRedrawsImagesOnTouchedRows(t *testing.T) {
	l := NewLayer()
	a := Placement{Key: "a", X: 10, Y: 5, W: 4, H: 3, Data: []byte("<A>")}
	b := Placement{Key: "b", X: 20, Y: 20, W: 4, H: 3, Data: []byte("<B>")}
	l.Set([]Placement{a, b})
	l.render(false)
	l.Touch(6, 40)
	_, post := l.render(false)
	if !bytes.Contains(post, []byte("<A>")) || bytes.Contains(post, []byte("<B>")) {
		t.Fatalf("touched row 6 should redraw only A: %q", post)
	}
	if _, post := l.render(false); len(post) != 0 {
		t.Fatalf("touch marks must clear after a frame: %q", post)
	}
}

// Reusing an encoder across widths must not leak color bits from a wider
// earlier image into a narrower or later wider one.
func TestSixelEncoderReuseAcrossWidths(t *testing.T) {
	red, green := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}
	pal := []color.RGBA{{0, 0, 0, 255}, red, green}
	e := NewSixelEncoder(pal, 0)
	half := image.NewRGBA(image.Rect(0, 0, 100, 6))
	for x := range 100 {
		for y := range 6 {
			c := red
			if x >= 50 {
				c = green
			}
			half.SetRGBA(x, y, c)
		}
	}
	fresh := string(NewSixelEncoder(pal, 0).Encode(half))
	e.Encode(solid(100, 6, green))
	e.Encode(solid(10, 6, red))
	if got := string(e.Encode(half)); got != fresh {
		t.Fatalf("reused encoder output differs:\n got %q\nwant %q", got, fresh)
	}
}

func TestIsFrame(t *testing.T) {
	tests := []struct {
		name string
		p    string
		want bool
	}{
		{"device attributes query", "\x1b[c", false},
		{"cell size query", "\x1b[16t", false},
		{"mode switches", "\x1b[?1049h\x1b[?25l\x1b[?1002h", false},
		{"window title", "\x1b]2;cliamp\x07", false},
		{"synchronized frame", "\x1b[?2026h\x1b[1;1H\x1b[?2026l", true},
		{"plain text frame", "\x1b[3;4Hhello", true},
		{"blank run", "\x1b[3;4H   ", true},
	}
	for _, tt := range tests {
		if got := isFrame([]byte(tt.p)); got != tt.want {
			t.Errorf("%s: isFrame = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A terminal query written just before a frame must not draw images: the
// frame's text would land on top of them and they would count as drawn.
func TestWriterDefersImagesPastQueries(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := NewLayer()
	l.Set([]Placement{{Key: "a", X: 1, Y: 1, W: 2, H: 1, Data: []byte("<A>")}})
	w := NewWriter(f, l)
	_, _ = w.Write([]byte("\x1b[c"))
	if out, _ := os.ReadFile(f.Name()); string(out) != "\x1b[c" {
		t.Fatalf("query write altered: %q", out)
	}
	_, _ = w.Write([]byte("\x1b[?2026hTEXT\x1b[?2026l"))
	out, _ := os.ReadFile(f.Name())
	if text, img := strings.Index(string(out), "TEXT"), strings.Index(string(out), "<A>"); img < text {
		t.Fatalf("image must follow the frame text: %q", out)
	}
}
