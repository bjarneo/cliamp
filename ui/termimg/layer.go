package termimg

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

// Placement is one image pinned to a cell rectangle. X and Y are 0-based
// screen cells. Key identifies the content: a placement whose key and
// rectangle match what is on screen is not redrawn.
type Placement struct {
	Key        string
	X, Y, W, H int
	Data       []byte // complete Sixel sequence sized for W x H cells
}

func (p Placement) sameSpot(q Placement) bool {
	return p.Key == q.Key && p.sameRect(q)
}

func (p Placement) sameRect(q Placement) bool {
	return p.X == q.X && p.Y == q.Y && p.W == q.W && p.H == q.H
}

// Layer holds the images that should be on screen and what was last drawn.
// The UI publishes placements every frame with Set; the Writer applies the
// difference around Bubbletea's text for that frame.
//
// The cells under a placement must render as constant blanks in the text
// frame. Bubbletea only rewrites cells that change, so constant blanks keep
// the pixels intact between redraws; rows whose text did change are
// reported with Touch so their images are redrawn.
//
// One placement can be live (an animation such as a pixel visualizer). The
// UI arms its rectangle every frame with ArmLive; frames produced off the UI
// goroutine (SetLive) are accepted only for the armed rectangle and drawn
// between Bubbletea frames by the Writer's pump.
type Layer struct {
	mu    sync.Mutex
	want  []Placement
	live  *Placement
	armed *Placement   // rectangle a live frame may occupy; nil disarms
	drawn []Placement  // what is on screen
	full  bool         // redraw everything on the next frame
	dirty map[int]bool // screen rows whose text changed since the last frame
	kick  chan struct{}

	// engaged is set while any image is wanted, armed, or on screen. The
	// Writer passes bytes straight through while it is clear, so sessions
	// that never draw an image pay nothing for the layer.
	engaged atomic.Bool
}

// NewLayer returns an empty layer.
func NewLayer() *Layer { return &Layer{kick: make(chan struct{}, 1)} }

// Set replaces the desired placements. It is cheap and safe to call from
// View on every frame; nil clears every image.
func (l *Layer) Set(ps []Placement) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.want = append(l.want[:0], ps...)
	l.syncEngagedLocked()
	l.mu.Unlock()
}

// Touch marks screen rows whose text changed. Bubbletea rewrites a changed
// line from its first to its last changed cell (or clears to the end of the
// line), which wipes any image in between even where its blank cells did
// not change, so images crossing a touched row are redrawn with the next
// frame. Marks accumulate until a frame is written: Bubbletea may render
// fewer frames than it builds views.
func (l *Layer) Touch(rows ...int) {
	if l == nil || len(rows) == 0 {
		return
	}
	l.mu.Lock()
	if l.dirty == nil {
		l.dirty = map[int]bool{}
	}
	for _, r := range rows {
		l.dirty[r] = true
	}
	l.mu.Unlock()
}

// Invalidate forces a full redraw on the next frame (after a resize or
// anything else that repaints the screen).
func (l *Layer) Invalidate() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.full = true
	l.mu.Unlock()
}

// ArmLive sets the rectangle live frames may occupy this frame; w or h of 0
// disarms it and drops the live frame. Call it from View every frame so a
// frame rendered after the UI moved on is never drawn.
func (l *Layer) ArmLive(x, y, w, h int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	defer l.syncEngagedLocked()
	if w <= 0 || h <= 0 {
		l.armed, l.live = nil, nil
		return
	}
	r := Placement{X: x, Y: y, W: w, H: h}
	if l.armed == nil || !l.armed.sameRect(r) {
		l.armed, l.live = &r, nil
	}
}

// SetLive offers a new live frame. It is dropped unless its rectangle is
// the armed one; otherwise the Writer draws it without waiting for
// Bubbletea's next frame. Safe to call from any goroutine.
func (l *Layer) SetLive(p Placement) {
	if l == nil {
		return
	}
	l.mu.Lock()
	ok := l.armed != nil && l.armed.sameRect(p)
	if ok {
		l.live = &p
	}
	l.mu.Unlock()
	if ok {
		select {
		case l.kick <- struct{}{}:
		default:
		}
	}
}

// syncEngagedLocked recomputes engaged. It stays set until the frame that
// erases the last drawn image. Caller holds mu.
func (l *Layer) syncEngagedLocked() {
	l.engaged.Store(len(l.want) > 0 || l.armed != nil || l.live != nil || len(l.drawn) > 0)
}

// active reports whether the Writer must process frames for this layer.
func (l *Layer) active() bool { return l != nil && l.engaged.Load() }

// wanted is every placement that should be on screen. Caller holds mu.
func (l *Layer) wanted() []Placement {
	if l.live == nil {
		return l.want
	}
	return append(l.want[:len(l.want):len(l.want)], *l.live)
}

func (l *Layer) touched(p Placement) bool {
	for r := p.Y; r < p.Y+p.H; r++ {
		if l.dirty[r] {
			return true
		}
	}
	return false
}

// renderLive draws a new live frame over a live rectangle a Bubbletea frame
// already put on screen. Anything else waits for the next frame.
func (l *Layer) renderLive() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live == nil || len(l.live.Data) == 0 || l.full {
		return nil
	}
	for i, d := range l.drawn {
		if !d.sameRect(*l.live) {
			continue
		}
		if d.Key == l.live.Key {
			return nil
		}
		var b bytes.Buffer
		cup(&b, l.live.Y, l.live.X)
		b.Write(l.live.Data)
		l.drawn[i] = *l.live
		return b.Bytes()
	}
	return nil
}

// render returns the bytes that bring the screen from drawn to want around
// one Bubbletea frame. pre erases placements that went away or moved; it
// runs before the frame's text so it cannot wipe text that now occupies
// those cells. post draws new, changed, or touched images after the text,
// or all of them when the frame cleared the screen (cleared).
func (l *Layer) render(cleared bool) (pre, post []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	all := cleared || l.full
	l.full = false
	want := l.wanted()
	var e, d bytes.Buffer
	for _, old := range l.drawn {
		if !containsRect(want, old) {
			erase(&e, old) // nothing opaque will cover it
		}
	}
	for _, p := range want {
		if len(p.Data) == 0 || (!all && containsSpot(l.drawn, p) && !l.touched(p)) {
			continue
		}
		cup(&d, p.Y, p.X)
		d.Write(p.Data)
	}
	l.drawn = append(l.drawn[:0], want...)
	clear(l.dirty)
	l.syncEngagedLocked()
	return e.Bytes(), d.Bytes()
}

func containsRect(ps []Placement, p Placement) bool {
	for _, q := range ps {
		if q.sameRect(p) {
			return true
		}
	}
	return false
}

func containsSpot(ps []Placement, p Placement) bool {
	for _, q := range ps {
		if q.sameSpot(p) {
			return true
		}
	}
	return false
}

// erase clears a rectangle's cells (ECH), which removes image pixels too.
func erase(b *bytes.Buffer, p Placement) {
	for r := range p.H {
		cup(b, p.Y+r, p.X)
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(p.W))
		b.WriteByte('X')
	}
}

func cup(b *bytes.Buffer, row, col int) {
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(row + 1))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(col + 1))
	b.WriteByte('H')
}

var (
	syncBegin = []byte("\x1b[?2026h")
	syncEnd   = []byte("\x1b[?2026l")
	saveCur   = []byte("\x1b7")
	restCur   = []byte("\x1b8")
	// Erase-display sequences: a frame containing one may have wiped images
	// anywhere below the cursor, so every image is redrawn after it.
	clearSeqs = [][]byte{[]byte("\x1b[2J"), []byte("\x1b[J"), []byte("\x1b[0J")}
)

// Writer is the program output. It passes Bubbletea's bytes through and
// wraps each frame (Bubbletea writes a whole frame per call) with the
// layer's image updates, inside the frame's synchronized update when it has
// one, saving and restoring the cursor around them. Writes that only carry
// control sequences (terminal queries, mode switches) are not frames: they
// go straight through, so images are never drawn just before a frame whose
// text lands on top of them. It embeds *os.File so Bubbletea still sees a
// terminal.
type Writer struct {
	*os.File
	layer    *Layer
	mu       sync.Mutex
	pumpOnce sync.Once
}

// NewWriter wraps f (normally os.Stdout) for layer. The pump that draws live
// frames between Bubbletea frames starts with the first image frame.
func NewWriter(f *os.File, layer *Layer) *Writer {
	return &Writer{File: f, layer: layer}
}

func (w *Writer) pump() {
	for range w.layer.kick {
		w.mu.Lock()
		if seq := w.layer.renderLive(); len(seq) > 0 {
			var b bytes.Buffer
			b.Write(syncBegin)
			wrap(&b, seq)
			b.Write(syncEnd)
			_, _ = w.File.Write(b.Bytes())
		}
		w.mu.Unlock()
	}
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.layer.active() || !isFrame(p) {
		return w.File.Write(p)
	}
	w.pumpOnce.Do(func() { go w.pump() })
	cleared := false
	for _, s := range clearSeqs {
		if bytes.Contains(p, s) {
			cleared = true
			break
		}
	}
	pre, post := w.layer.render(cleared)
	if len(pre) == 0 && len(post) == 0 {
		return w.File.Write(p)
	}
	head, body, tail := []byte(nil), p, []byte(nil)
	if bytes.HasPrefix(body, syncBegin) {
		head, body = syncBegin, body[len(syncBegin):]
	}
	if bytes.HasSuffix(body, syncEnd) {
		body, tail = body[:len(body)-len(syncEnd)], syncEnd
	}
	var b bytes.Buffer
	b.Grow(len(p) + len(pre) + len(post) + 16)
	b.Write(head)
	wrap(&b, pre)
	b.Write(body)
	wrap(&b, post)
	b.Write(tail)
	if _, err := w.File.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteString and ReadFrom keep io.WriteString and io.Copy callers (such as
// Bubbletea's print-above path) on Write's lock; the embedded *os.File would
// otherwise take them straight to the terminal.
func (w *Writer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *Writer) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}

// isFrame reports whether p is a rendered frame: a synchronized update, or
// anything that prints text. Pure control-sequence writes are not.
func isFrame(p []byte) bool {
	if bytes.HasPrefix(p, syncBegin) || bytes.HasSuffix(p, syncEnd) {
		return true
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == 0x1b && i+1 < len(p):
			i = skipEscape(p, i)
		case c >= 0x20 && c != 0x7f:
			return true
		}
	}
	return false
}

// skipEscape returns the index of the last byte of the escape sequence that
// starts at p[i] (ESC): CSI, OSC/DCS/APC strings, or a two-byte escape.
func skipEscape(p []byte, i int) int {
	switch p[i+1] {
	case '[': // CSI: parameters, then a final byte in 0x40-0x7e
		for j := i + 2; j < len(p); j++ {
			if p[j] >= 0x40 && p[j] <= 0x7e {
				return j
			}
		}
	case ']', 'P', '_', '^': // string: ends with BEL or ST (ESC \)
		for j := i + 2; j < len(p); j++ {
			if p[j] == 0x07 {
				return j
			}
			if p[j] == 0x1b && j+1 < len(p) && p[j+1] == '\\' {
				return j + 1
			}
		}
	default:
		return i + 1
	}
	return len(p) - 1
}

func wrap(b *bytes.Buffer, seq []byte) {
	if len(seq) == 0 {
		return
	}
	b.Write(saveCur)
	b.Write(seq)
	b.Write(restCur)
}
