// Package kittyimg transmits images to a kitty terminal and returns the
// Unicode placeholder block that displays them.
//
// The image travels out of band (a graphics escape written straight to the
// terminal), so it does not disturb the renderer's stream. The placeholders are
// ordinary text: any host that keeps them in its buffer keeps the image on
// screen, and the terminal moves it with the text.
package kittyimg

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/x/term"

	"github.com/bjarneo/cliamp/applog"
)

// maxPixels caps the decoded image: a plugin can point ShowImage at a photo
// that is far bigger than any cover art, and image.Decode would allocate the
// full pixel buffer before the scale runs. A 32 MiB file is still accepted,
// but a huge pixel box is rejected up front.
const maxPixels = 32 * 1024 * 1024

// decodeImage checks the pixel size with image.DecodeConfig first and only
// then decodes the full image.
func decodeImage(raw []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("kittyimg: decode config: %w", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("kittyimg: image is %dx%d, over the %d pixel cap", cfg.Width, cfg.Height, maxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("kittyimg: decode: %w", err)
	}
	return img, nil
}

// placeholder is the private-use character kitty composites the image behind.
const placeholder = '\U0010EEEE'

// diacritics maps a row, column or high id byte to the combining mark kitty
// expects. The order comes from kitty's rowcolumn-diacritics.txt.
var diacritics = []rune{0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357, 0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369, 0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484, 0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597, 0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1, 0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658, 0x0659, 0x065A, 0x065B, 0x065D, 0x065E, 0x06D6, 0x06D7, 0x06D8, 0x06D9, 0x06DA, 0x06DB, 0x06DC, 0x06DF, 0x06E0, 0x06E1, 0x06E2, 0x06E4, 0x06E7, 0x06E8, 0x06EB, 0x06EC, 0x0730, 0x0732, 0x0733, 0x0735, 0x0736, 0x073A, 0x073D, 0x073F, 0x0740, 0x0741, 0x0743, 0x0745, 0x0747, 0x0749, 0x074A, 0x07EB, 0x07EC, 0x07ED, 0x07EE, 0x07EF, 0x07F0, 0x07F1, 0x07F3, 0x0816, 0x0817, 0x0818, 0x0819, 0x081B, 0x081C, 0x081D, 0x081E, 0x081F, 0x0820, 0x0821, 0x0822, 0x0823, 0x0825, 0x0826, 0x0827, 0x0829, 0x082A, 0x082B, 0x082C, 0x082D, 0x0951, 0x0953, 0x0954, 0x0F82, 0x0F83, 0x0F86, 0x0F87, 0x135D, 0x135E, 0x135F, 0x17DD, 0x193A, 0x1A17, 0x1A75, 0x1A76, 0x1A77, 0x1A78, 0x1A79, 0x1A7A, 0x1A7B, 0x1A7C, 0x1B6B, 0x1B6D, 0x1B6E, 0x1B6F, 0x1B70, 0x1B71, 0x1B72, 0x1B73, 0x1CD0, 0x1CD1, 0x1CD2, 0x1CDA, 0x1CDB, 0x1CE0, 0x1DC0, 0x1DC1, 0x1DC3, 0x1DC4, 0x1DC5, 0x1DC6, 0x1DC7, 0x1DC8, 0x1DC9, 0x1DCB, 0x1DCC, 0x1DD1, 0x1DD2, 0x1DD3, 0x1DD4, 0x1DD5, 0x1DD6, 0x1DD7, 0x1DD8, 0x1DD9, 0x1DDA, 0x1DDB, 0x1DDC, 0x1DDD, 0x1DDE, 0x1DDF, 0x1DE0, 0x1DE1, 0x1DE2, 0x1DE3, 0x1DE4, 0x1DE5, 0x1DE6, 0x1DFE, 0x20D0, 0x20D1, 0x20D4, 0x20D5, 0x20D6, 0x20D7, 0x20DB, 0x20DC, 0x20E1, 0x20E7, 0x20E9, 0x20F0, 0x2CEF, 0x2CF0, 0x2CF1, 0x2DE0, 0x2DE1, 0x2DE2, 0x2DE3, 0x2DE4, 0x2DE5, 0x2DE6, 0x2DE7, 0x2DE8, 0x2DE9, 0x2DEA, 0x2DEB, 0x2DEC, 0x2DED, 0x2DEE, 0x2DEF, 0x2DF0, 0x2DF1, 0x2DF2, 0x2DF3, 0x2DF4, 0x2DF5, 0x2DF6, 0x2DF7, 0x2DF8, 0x2DF9, 0x2DFA, 0x2DFB, 0x2DFC, 0x2DFD, 0x2DFE, 0x2DFF, 0xA66F, 0xA67C, 0xA67D, 0xA6F0, 0xA6F1, 0xA8E0, 0xA8E1, 0xA8E2, 0xA8E3, 0xA8E4, 0xA8E5, 0xA8E6, 0xA8E7, 0xA8E8, 0xA8E9, 0xA8EA, 0xA8EB, 0xA8EC, 0xA8ED, 0xA8EE, 0xA8EF, 0xA8F0, 0xA8F1, 0xAAB0, 0xAAB2, 0xAAB3, 0xAAB7, 0xAAB8, 0xAABE, 0xAABF, 0xAAC1, 0xFE20, 0xFE21, 0xFE22, 0xFE23, 0xFE24, 0xFE25, 0xFE26}

// Placement is a transmitted image plus the cell rectangle it was sized for.
type Placement struct {
	ID   int
	Cols int
	Rows int
}

const (
	// maxShowWorkers bounds concurrent image decode, scale, and transmit jobs.
	maxShowWorkers = 4

	// maxShowPending bounds total active and queued Show workers.
	maxShowPending = 16
)

var (
	// maxPlaced caps the entries kept in placed before evicting the oldest ID.
	maxPlaced = 256

	mu          sync.Mutex
	placed      = map[int]Placement{}
	placedOrder []int
	lastID      = 1000

	showSem     = make(chan struct{}, maxShowWorkers)
	showPending = make(chan struct{}, maxShowPending)

	pulse       atomic.Bool
	ttyDisabled atomic.Bool
	writeMu     sync.Mutex
	ttyOpened   bool
	ttyFile     io.WriteCloser
	ttyErr      error
	openTTY     = defaultOpenTTY

	errTTYUnavailable = errors.New("kittyimg: terminal is unavailable")
)

// SetTTYEnabled turns Kitty graphics TTY writes on or off. Headless mode
// calls SetTTYEnabled(false) so plugins never write escape sequences to a TTY.
func SetTTYEnabled(enabled bool) {
	ttyDisabled.Store(!enabled)
}

func defaultOpenTTY() (io.WriteCloser, error) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return nil, errTTYUnavailable
	}
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}

// storePlacedLocked stores p in placed and evicts the oldest entry when placed
// reaches maxPlaced. Caller must hold mu.
func storePlacedLocked(p Placement) {
	if _, exists := placed[p.ID]; exists {
		placed[p.ID] = p
		if idx := slices.Index(placedOrder, p.ID); idx >= 0 {
			placedOrder = append(placedOrder[:idx], placedOrder[idx+1:]...)
		}
		placedOrder = append(placedOrder, p.ID)
		return
	}
	for len(placed) >= maxPlaced && len(placedOrder) > 0 {
		oldest := placedOrder[0]
		placedOrder = placedOrder[1:]
		delete(placed, oldest)
	}
	placed[p.ID] = p
	placedOrder = append(placedOrder, p.ID)
}

// deletePlacedLocked removes id from placed and placedOrder. Caller must hold mu.
func deletePlacedLocked(id int) {
	delete(placed, id)
	if idx := slices.Index(placedOrder, id); idx >= 0 {
		placedOrder = append(placedOrder[:idx], placedOrder[idx+1:]...)
	}
}

// Show decodes source, scales it to the pixel size of a cols x rows cell block,
// and transmits it as a virtual placement. Call Block to get the text that
// displays it.
func Show(source string, cols, rows int, reuseID int) (Placement, error) {
	return ShowContext(context.Background(), source, cols, rows, reuseID)
}

// ShowContext behaves like Show and stops background work when ctx ends.
func ShowContext(ctx context.Context, source string, cols, rows int, reuseID int) (Placement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Placement{}, err
	}
	if cols <= 0 || rows <= 0 {
		return Placement{}, fmt.Errorf("kittyimg: bad size %dx%d", cols, rows)
	}
	if cols > len(diacritics) {
		cols = len(diacritics)
	}
	if rows > len(diacritics) {
		rows = len(diacritics)
	}
	// Reject an obviously wrong source before returning, so a typo is visible.
	if source == "" {
		return Placement{}, fmt.Errorf("kittyimg: empty source")
	}

	if reuseID < 0 || reuseID > 0xFFFFFF {
		return Placement{}, fmt.Errorf("kittyimg: image id out of range: %d", reuseID)
	}

	// Skip spawning a worker when TTY output is disabled in headless mode.
	disabled := ttyDisabled.Load()
	if !disabled {
		select {
		case showPending <- struct{}{}:
		default:
			return Placement{}, fmt.Errorf("kittyimg: too many pending image loads")
		}
	}

	mu.Lock()
	id := reuseID
	if id <= 0 {
		lastID++
		if lastID > 0xFFFFFF {
			lastID = 1
		}
		id = lastID
	} else if id > lastID {
		// Keep the counter ahead of a reused id so a later image cannot take it.
		lastID = id
	}
	placement := Placement{ID: id, Cols: cols, Rows: rows}
	storePlacedLocked(placement)
	mu.Unlock()

	if disabled {
		return placement, nil
	}

	// Decode, scale, and transmit off the caller's thread with bounded concurrency.
	go func() {
		defer func() { <-showPending }()
		select {
		case showSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-showSem }()

		if ctx.Err() != nil {
			return
		}
		raw, err := load(source)
		if err != nil {
			applog.Warn("kittyimg: load: %v", err)
			return
		}
		if ctx.Err() != nil {
			return
		}
		img, err := decodeImage(raw)
		if err != nil {
			applog.Warn("kittyimg: %v", err)
			return
		}
		if ctx.Err() != nil {
			return
		}
		scaled := scale(img, cols*8, rows*16)
		if ctx.Err() != nil {
			return
		}
		if err := transmitContext(ctx, id, scaled, cols, rows); err != nil &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, errTTYUnavailable) {
			applog.Warn("kittyimg: transmit: %v", err)
		}
	}()

	return placement, nil
}

// Block returns the placeholder text that displays the placement. It spans
// Cols cells by Rows lines.
func (p Placement) Block() string {
	if p.ID == 0 || p.Cols <= 0 || p.Rows <= 0 {
		return ""
	}
	pulse.Store(!pulse.Load())
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (p.ID>>16)&0xff, (p.ID>>8)&0xff, p.ID&0xff)
	var b strings.Builder
	for row := 0; row < p.Rows; row++ {
		b.WriteString(color)
		for col := 0; col < p.Cols; col++ {
			b.WriteRune(placeholder)
			b.WriteRune(diacritic(row))
			b.WriteRune(diacritic(col))
		}
		// The renderer redraws only the lines whose bytes changed. A placeholder
		// block is static, so without a change the terminal never re-emits the
		// cells and kitty drops the placement: the image disappears until some
		// other update repaints that line. Alternating an invisible reset changes
		// the bytes without changing the drawing and keeps the image alive.
		if pulse.Load() {
			b.WriteString("\x1b[0m")
		} else {
			b.WriteString("\x1b[0;0m")
		}
		if row < p.Rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Clear deletes a transmitted image. Placeholders that remain on screen stop
// showing anything.
func Clear(id int) {
	if id <= 0 || id > 0xFFFFFF {
		return
	}
	mu.Lock()
	deletePlacedLocked(id)
	mu.Unlock()
	if ttyDisabled.Load() {
		return
	}
	writeMu.Lock()
	_ = writeTTYLocked(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id))
	writeMu.Unlock()
}

// Placement returns the placement stored under id.
func PlacementByID(id int) (Placement, bool) {
	mu.Lock()
	defer mu.Unlock()
	p, ok := placed[id]
	return p, ok
}

func diacritic(n int) rune {
	if n < 0 {
		n = 0
	}
	if n >= len(diacritics) {
		n = len(diacritics) - 1
	}
	return diacritics[n]
}

func load(source string) ([]byte, error) {
	switch {
	case strings.HasPrefix(source, "data:"):
		comma := strings.IndexByte(source, ',')
		if comma < 0 {
			return nil, fmt.Errorf("kittyimg: malformed data URL")
		}
		header, payload := source[:comma], source[comma+1:]
		if !strings.Contains(header, ";base64") {
			return nil, fmt.Errorf("kittyimg: data URL is not base64")
		}
		if base64.StdEncoding.DecodedLen(len(payload)) > maxImageBytes {
			return nil, fmt.Errorf("kittyimg: image is larger than %d bytes", maxImageBytes)
		}
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, err
		}
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("kittyimg: image is larger than %d bytes", maxImageBytes)
		}
		return data, nil
	case strings.HasPrefix(source, "file://"):
		path, err := fileURLToPath(source)
		if err != nil {
			return nil, err
		}
		return readFile(path)
	case source != "":
		return readFile(source)
	}
	return nil, fmt.Errorf("kittyimg: empty source")
}

func fileURLToPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("kittyimg: invalid file url: %w", err)
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("kittyimg: remote file url host %q is not supported", u.Host)
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' &&
		((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), nil
}

// maxImageBytes caps what a plugin can read as an image. A plugin with only the
// image permission must not be able to allocate the process out of memory with a
// device or a huge file.
const maxImageBytes = 32 << 20

// readFile reads a regular file up to maxImageBytes. Devices such as /dev/zero
// are rejected: reading them never ends.
func readFile(path string) ([]byte, error) {
	file, err := openImage(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("kittyimg: %s is not a regular file", path)
	}
	if info.Size() > maxImageBytes {
		return nil, fmt.Errorf("kittyimg: %s is larger than %d bytes", path, maxImageBytes)
	}
	// Read from the open descriptor with a hard cap: the file cannot be swapped
	// for a device between the check and the read.
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("kittyimg: image is larger than %d bytes", maxImageBytes)
	}
	return data, nil
}

// scale box-filters img down to w x h.
func scale(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	if sw <= 0 || sh <= 0 {
		return dst
	}
	for y := range h {
		y0 := b.Min.Y + y*sh/h
		y1 := max(b.Min.Y+(y+1)*sh/h, y0+1)
		for x := range w {
			x0 := b.Min.X + x*sw/w
			x1 := max(b.Min.X+(x+1)*sw/w, x0+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8((r / n) >> 8),
				G: uint8((g / n) >> 8),
				B: uint8((bl / n) >> 8),
				A: uint8((a / n) >> 8),
			})
		}
	}
	return dst
}

// transmitContext encodes img as PNG and writes it to the terminal in 4096-byte
// base64 chunks. It cancels before writing when ctx ends.
func transmitContext(ctx context.Context, id int, img *image.NRGBA, cols, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttyDisabled.Load() {
		return nil
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Hold writeMu across all chunks so transfers and Clear never interleave.
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttyDisabled.Load() {
		return nil
	}

	payload := base64.StdEncoding.EncodeToString(encoded.Bytes())
	const chunk = 4096
	first := true
	for len(payload) > 0 {
		n := min(chunk, len(payload))
		part := payload[:n]
		payload = payload[n:]
		more := 0
		if len(payload) > 0 {
			more = 1
		}
		var seq string
		if first {
			seq = fmt.Sprintf("\x1b_Ga=T,f=100,i=%d,U=1,c=%d,r=%d,q=2,m=%d;%s\x1b\\", id, cols, rows, more, part)
			first = false
		} else {
			seq = fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, part)
		}
		if err := writeTTYLocked(seq); err != nil {
			return err
		}
	}
	return nil
}

// writeTTYLocked writes seq to the terminal device. Caller must hold writeMu.
func writeTTYLocked(seq string) error {
	if ttyDisabled.Load() {
		return nil
	}
	if !ttyOpened {
		ttyOpened = true
		ttyFile, ttyErr = openTTY()
	}
	if ttyErr != nil {
		return ttyErr
	}
	_, err := io.WriteString(ttyFile, seq)
	return err
}
