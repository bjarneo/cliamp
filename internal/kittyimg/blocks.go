package kittyimg

import (
	"fmt"
	"math"
	"strings"
)

// Blocks renders source as ANSI half-block art: every cell is a "▀" whose
// foreground is the top pixel and whose background is the bottom one. It needs
// no graphics protocol, so it works in any terminal, and it is the text-only
// fallback for the image panes.
func Blocks(source string, cols, rows int) (string, error) {
	if cols <= 0 || rows <= 0 {
		return "", fmt.Errorf("kittyimg: bad size %dx%d", cols, rows)
	}
	// The dimensions come from a plugin; cap them so a huge call cannot
	// allocate an oversized image or build a giant string.
	const maxSide = 512
	cols = min(cols, maxSide)
	rows = min(rows, maxSide)
	raw, err := load(source)
	if err != nil {
		return "", err
	}
	img, err := decodeImage(raw)
	if err != nil {
		return "", err
	}
	// Fit the image inside the cell box instead of stretching it: the block is
	// cols x rows cells, and each cell is two pixels tall, so the pixel box is
	// cols x rows*2. Keep the source aspect and letterbox the rest.
	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	boxW, boxH := cols, rows*2
	ratio := math.Min(float64(boxW)/float64(srcW), float64(boxH)/float64(srcH))
	w := max(1, int(float64(srcW)*ratio))
	h := max(1, int(float64(srcH)*ratio))
	scaled := scale(img, w, h)
	offX, offY := (boxW-w)/2, (boxH-h)/2

	var b strings.Builder
	for row := range rows {
		for col := range cols {
			x := col - offX
			y := row*2 - offY
			if x < 0 || x >= w || y < 0 || y >= h {
				// One column per cell: two spaces here would make the line wider
				// than the pane and push the block out of place. Reset first so
				// the padding does not inherit the previous background.
				b.WriteString("\x1b[0m ")
				continue
			}
			top := scaled.NRGBAAt(x, y)
			bottom := scaled.NRGBAAt(x, min(y+1, h-1))
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}
		b.WriteString("\x1b[0m")
		if row < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}
