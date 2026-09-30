package termimg

import (
	"fmt"
	"image"
	"image/draw"
	"strings"
)

// Fill scales src to exactly w x h pixels, center-cropping to the target
// aspect first (like CSS object-fit: cover). Downscaling averages every
// source pixel under each target pixel, which keeps covers crisp without
// moiré; upscaling falls back to nearest neighbor.
func Fill(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
	if src == nil || w <= 0 || h <= 0 {
		return dst
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	// Crop the source to the destination aspect ratio.
	crop := sb
	if sw*h > sh*w { // source wider: trim the sides
		cw := sh * w / h
		crop.Min.X += (sw - cw) / 2
		crop.Max.X = crop.Min.X + cw
	} else { // source taller: trim top and bottom
		ch := sw * h / w
		crop.Min.Y += (sh - ch) / 2
		crop.Max.Y = crop.Min.Y + ch
	}
	rgba := toRGBA(src)
	cw, ch := crop.Dx(), crop.Dy()
	for y := range h {
		y0 := crop.Min.Y + y*ch/h
		y1 := max(y0+1, crop.Min.Y+(y+1)*ch/h)
		for x := range w {
			x0 := crop.Min.X + x*cw/w
			x1 := max(x0+1, crop.Min.X+(x+1)*cw/w)
			var r, g, b, n int
			for sy := y0; sy < y1; sy++ {
				off := rgba.PixOffset(x0, sy)
				for sx := x0; sx < x1; sx++ {
					r += int(rgba.Pix[off])
					g += int(rgba.Pix[off+1])
					b += int(rgba.Pix[off+2])
					off += 4
					n++
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o] = uint8(r / n)
			dst.Pix[o+1] = uint8(g / n)
			dst.Pix[o+2] = uint8(b / n)
			dst.Pix[o+3] = 255
		}
	}
	return dst
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, src, b.Min, draw.Src)
	return r
}

// HalfBlocks renders img as text: each cell is an upper-half block whose
// foreground is the top pixel and background the bottom one, so a w x 2h
// image fills w x h cells. It is the image fallback for terminals without a
// pixel protocol; every line is exactly img width cells.
func HalfBlocks(img *image.RGBA) []string {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	lines := make([]string, 0, (h+1)/2)
	var b strings.Builder
	for y := 0; y < h; y += 2 {
		b.Reset()
		var lastFg, lastBg [3]uint8
		first := true
		for x := range w {
			top := img.Pix[img.PixOffset(x, y):]
			fg := [3]uint8{top[0], top[1], top[2]}
			bg := fg
			if y+1 < h {
				bot := img.Pix[img.PixOffset(x, y+1):]
				bg = [3]uint8{bot[0], bot[1], bot[2]}
			}
			if first || fg != lastFg {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", fg[0], fg[1], fg[2])
			}
			if first || bg != lastBg {
				fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm", bg[0], bg[1], bg[2])
			}
			first = false
			lastFg, lastBg = fg, bg
			b.WriteString("▀")
		}
		b.WriteString("\x1b[m")
		lines = append(lines, b.String())
	}
	return lines
}
