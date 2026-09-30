// Package termimg draws raster images into terminal cells: Sixel for
// terminals that support it, and truecolor half-blocks everywhere else. It
// also owns the output splice that lets images ride along with Bubbletea's
// frames (see Writer).
package termimg

import (
	"bytes"
	"image"
	"image/color"
	"runtime"
	"sort"
	"strconv"
	"sync"
)

// SixelEncoder quantizes images to a fixed palette through a 5-bit-per-
// channel lookup table and writes compact Sixel: only the palette entries
// an image uses are defined, and runs are RLE-compressed. Sixel bands (six
// pixel rows) are independent, so large images encode in parallel.
//
// An encoder is not safe for concurrent use; its buffers are reused.
type SixelEncoder struct {
	pal    []color.RGBA
	lut    [32768]uint8
	dither int // ordered-dither amplitude, 0 = off

	idx     []uint8
	workers []*bandWorker
}

type bandWorker struct {
	bits    [][]byte
	used    []bool
	present []bool
	out     bytes.Buffer
	width   int // image width the bit rows were last used for
}

var bayer4 = [4][4]int{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// NewSixelEncoder builds an encoder for pal (at most 256 colors). dither is
// the ordered-dither amplitude in 8-bit steps; 0 disables it.
func NewSixelEncoder(pal []color.RGBA, dither int) *SixelEncoder {
	if len(pal) > 256 {
		pal = pal[:256]
	}
	e := &SixelEncoder{pal: pal, dither: dither}
	for i := range e.lut {
		r, g, b := (i>>10)<<3|4, (i>>5&31)<<3|4, (i&31)<<3|4
		best, bd := 0, 1<<30
		for j, p := range pal {
			dr, dg, db := r-int(p.R), g-int(p.G), b-int(p.B)
			if d := 2*dr*dr + 4*dg*dg + 3*db*db; d < bd {
				best, bd = j, d
			}
		}
		e.lut[i] = uint8(best)
	}
	return e
}

func clamp8(v int) int { return max(0, min(255, v)) }

func (e *SixelEncoder) quantizeRows(img *image.RGBA, y0, y1 int) {
	w := img.Rect.Dx()
	for y := y0; y < y1; y++ {
		row := img.Pix[y*img.Stride:]
		out := e.idx[y*w : (y+1)*w]
		for x := range out {
			p := row[x*4:]
			r, g, b := int(p[0]), int(p[1]), int(p[2])
			if e.dither > 0 {
				d := (bayer4[y&3][x&3]*2 - 15) * e.dither / 16
				r, g, b = clamp8(r+d), clamp8(g+d), clamp8(b+d)
			}
			out[x] = e.lut[(r>>3)<<10|(g>>3)<<5|b>>3]
		}
	}
}

func (e *SixelEncoder) worker(i, w int) *bandWorker {
	for len(e.workers) <= i {
		n := len(e.pal)
		e.workers = append(e.workers, &bandWorker{bits: make([][]byte, n), used: make([]bool, n), present: make([]bool, n)})
	}
	bw := e.workers[i]
	if w != bw.width {
		// encodeBands only clears the first w bytes of the colors it used,
		// so a width change must start from clean rows.
		for c := range bw.bits {
			if len(bw.bits[c]) < w {
				bw.bits[c] = make([]byte, w)
			} else {
				clear(bw.bits[c])
			}
		}
		clear(bw.used)
		bw.width = w
	}
	clear(bw.present)
	bw.out.Reset()
	return bw
}

// encodeBands writes Sixel data for pixel rows [y0, y1); y0 is a multiple
// of six.
func (e *SixelEncoder) encodeBands(bw *bandWorker, w, y0, y1 int) {
	b := &bw.out
	for by := y0; by < y1; by += 6 {
		for c := range bw.used {
			if bw.used[c] {
				clear(bw.bits[c][:w])
				bw.used[c] = false
			}
		}
		for r := 0; r < 6 && by+r < y1; r++ {
			for x, c := range e.idx[(by+r)*w : (by+r+1)*w] {
				bw.bits[c][x] |= 1 << r
				bw.used[c] = true
			}
		}
		first := true
		for c := range e.pal {
			if !bw.used[c] {
				continue
			}
			bw.present[c] = true
			if !first {
				b.WriteByte('$')
			}
			first = false
			b.WriteByte('#')
			b.WriteString(strconv.Itoa(c))
			bs := bw.bits[c][:w]
			end := w
			for end > 0 && bs[end-1] == 0 {
				end--
			}
			for x := 0; x < end; {
				v := bs[x]
				n := 1
				for x+n < end && bs[x+n] == v {
					n++
				}
				if n > 3 {
					b.WriteByte('!')
					b.WriteString(strconv.Itoa(n))
					b.WriteByte(63 + v)
				} else {
					for range n {
						b.WriteByte(63 + v)
					}
				}
				x += n
			}
		}
		b.WriteByte('-')
	}
}

// Encode returns a complete Sixel DCS sequence for img.
func (e *SixelEncoder) Encode(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if cap(e.idx) < w*h {
		e.idx = make([]uint8, w*h)
	}
	e.idx = e.idx[:w*h]

	bands := (h + 5) / 6
	k := max(1, min(runtime.NumCPU(), bands/8))
	per := (bands + k - 1) / k
	ws := make([]*bandWorker, k)
	for i := range ws {
		ws[i] = e.worker(i, w)
	}
	var wg sync.WaitGroup
	for i := range k {
		y0, y1 := i*per*6, min(h, (i+1)*per*6)
		if y0 >= y1 {
			continue
		}
		wg.Go(func() {
			e.quantizeRows(img, y0, y1)
			e.encodeBands(ws[i], w, y0, y1)
		})
	}
	wg.Wait()

	var b bytes.Buffer
	total := 64
	for _, bw := range ws {
		total += bw.out.Len()
	}
	b.Grow(total + len(e.pal)*16)
	// P2=1: pixels left at 0 stay transparent; raster attributes give the
	// exact size so the terminal does not have to infer it.
	b.WriteString("\x1bP0;1;0q\"1;1;")
	b.WriteString(strconv.Itoa(w))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(h))
	for c, p := range e.pal {
		used := false
		for _, bw := range ws {
			used = used || bw.present[c]
		}
		if !used {
			continue
		}
		b.WriteByte('#')
		b.WriteString(strconv.Itoa(c))
		b.WriteString(";2;")
		b.WriteString(strconv.Itoa(int(p.R) * 100 / 255))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(p.G) * 100 / 255))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(p.B) * 100 / 255))
	}
	for _, bw := range ws {
		b.Write(bw.out.Bytes())
	}
	b.WriteString("\x1b\\")
	return b.Bytes()
}

// AdaptivePalette builds an n-color palette for img by median cut over a
// sample of its pixels.
func AdaptivePalette(img *image.RGBA, n int) []color.RGBA {
	step := max(1, len(img.Pix)/4/4096)
	var samples []color.RGBA
	for i := 0; i+3 < len(img.Pix); i += 4 * step {
		samples = append(samples, color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], 255})
	}
	return medianCut(samples, n)
}

// medianCut splits the widest-range box at its median until there are n.
func medianCut(samples []color.RGBA, n int) []color.RGBA {
	if len(samples) == 0 {
		return []color.RGBA{{0, 0, 0, 255}}
	}
	boxes := [][]color.RGBA{samples}
	chRange := func(px []color.RGBA) (int, int) {
		lo, hi := [3]int{255, 255, 255}, [3]int{}
		for _, p := range px {
			for c, v := range [3]int{int(p.R), int(p.G), int(p.B)} {
				lo[c], hi[c] = min(lo[c], v), max(hi[c], v)
			}
		}
		best, br := 0, -1
		for c := range 3 {
			if r := hi[c] - lo[c]; r > br {
				best, br = c, r
			}
		}
		return best, br
	}
	for len(boxes) < n {
		bi, bc, score := -1, 0, 0
		for i, px := range boxes {
			if len(px) < 2 {
				continue
			}
			c, r := chRange(px)
			if s := r * len(px); s > score {
				bi, bc, score = i, c, s
			}
		}
		if bi < 0 {
			break
		}
		px := boxes[bi]
		sort.Slice(px, func(a, b int) bool {
			ca, cb := [3]uint8{px[a].R, px[a].G, px[a].B}, [3]uint8{px[b].R, px[b].G, px[b].B}
			return ca[bc] < cb[bc]
		})
		mid := len(px) / 2
		boxes[bi] = px[:mid]
		boxes = append(boxes, px[mid:])
	}
	pal := make([]color.RGBA, 0, len(boxes))
	for _, px := range boxes {
		var r, g, b int
		for _, p := range px {
			r, g, b = r+int(p.R), g+int(p.G), b+int(p.B)
		}
		k := max(1, len(px))
		pal = append(pal, color.RGBA{uint8(r / k), uint8(g / k), uint8(b / k), 255})
	}
	return pal
}

// EncodeSixel encodes a one-off image (cover art) with its own adaptive
// palette. Use a shared SixelEncoder for repeated frames instead.
func EncodeSixel(img *image.RGBA) []byte {
	return NewSixelEncoder(AdaptivePalette(img, 96), 0).Encode(img)
}
