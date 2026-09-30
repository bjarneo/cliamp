package ui

// vis_pixel.go holds the pixel visualizers, Aurora and Phosphor. They draw
// into an RGBA canvas at any resolution: pixel-exact through Sixel where the
// immersive layout can place images, and at half-block resolution as text
// everywhere else (their regular Render output), so both look the same, one
// sharper than the other.

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/bjarneo/cliamp/ui/termimg"
)

// IsPixelMode reports whether mode is drawn by a PixelVis.
func IsPixelMode(mode VisMode) bool {
	return mode == VisAurora || mode == VisPhosphor
}

// PixelVis renders one pixel visualizer. It keeps a float accumulation
// buffer between frames for trails and glow, so each canvas size needs its
// own PixelVis; it is not safe for concurrent use.
type PixelVis struct {
	mode   VisMode
	w, h   int
	acc    []float32 // RGB, 0..1, decayed every frame
	frame  uint64
	colors *PixelColors
}

// PixelColors is the spectrum palette a PixelVis draws with. Snapshot it on
// the UI goroutine (CurrentPixelColors) when drawing from another one.
type PixelColors struct{ Low, Mid, High color.Color }

// CurrentPixelColors returns the active theme's spectrum colors.
func CurrentPixelColors() PixelColors {
	return PixelColors{Low: SpectrumLow, Mid: SpectrumMid, High: SpectrumHigh}
}

// SetColors pins the palette; without it Draw reads the live theme.
func (p *PixelVis) SetColors(c PixelColors) { p.colors = &c }

// Mode is the visualizer mode p draws.
func (p *PixelVis) Mode() VisMode { return p.mode }

// NewPixelVis returns a renderer for a pixel mode.
func NewPixelVis(mode VisMode) *PixelVis { return &PixelVis{mode: mode} }

// PixelInput snapshots what the pixel visualizers draw from: the smoothed
// spectrum and the raw waveform. The slices are copies, safe to hand to
// another goroutine.
func (v *Visualizer) PixelInput() (bands, wave []float64) {
	if v == nil {
		return nil, nil
	}
	return append([]float64(nil), v.SmoothedBands()...), append([]float64(nil), v.waveBuf...)
}

// Draw renders the next frame into dst (its size may change between calls,
// which resets the trails).
func (p *PixelVis) Draw(dst *image.RGBA, bands, wave []float64) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	if w != p.w || h != p.h {
		p.w, p.h = w, h
		p.acc = make([]float32, w*h*3)
	}
	p.frame++
	var cs PixelColors
	if p.colors != nil {
		cs = *p.colors
	} else {
		cs = CurrentPixelColors() // UI goroutine only; workers pin colors
	}
	low, mid, high := rgbf(cs.Low), rgbf(cs.Mid), rgbf(cs.High)
	switch p.mode {
	case VisPhosphor:
		p.decay(0.78)
		p.drawPhosphor(wave, low, mid)
	default:
		p.decay(0.70)
		p.drawAurora(bands, low, mid, high)
	}
	for i, j := 0, 0; i < len(p.acc); i, j = i+3, j+4 {
		dst.Pix[j] = toByte(p.acc[i])
		dst.Pix[j+1] = toByte(p.acc[i+1])
		dst.Pix[j+2] = toByte(p.acc[i+2])
		dst.Pix[j+3] = 255
	}
}

func (p *PixelVis) decay(k float32) {
	for i := range p.acc {
		p.acc[i] *= k
	}
}

// glow adds color c at intensity v to pixel (x, y), keeping the brighter
// of what is there and what is added so overlapping glows do not blow out.
func (p *PixelVis) glow(x, y int, c [3]float32, v float32) {
	if x < 0 || y < 0 || x >= p.w || y >= p.h || v <= 0 {
		return
	}
	o := (y*p.w + x) * 3
	for k := range 3 {
		if nv := c[k] * v; nv > p.acc[o+k] {
			p.acc[o+k] = nv
		}
	}
}

// drawAurora draws the spectrum as a smooth luminous ridge over a horizon
// line, brightest at the crest, with a soft halo above and a shimmering
// reflection below. Color runs from the low to the high spectrum color
// across the width.
func (p *PixelVis) drawAurora(bands []float64, low, mid, high [3]float32) {
	w, h := p.w, p.h
	horizon := h * 64 / 100
	maxUp := float64(horizon - 2)
	maxDown := float64(h - horizon - 1)
	white := [3]float32{1, 1, 1}
	for x := range w {
		t := float64(x) / float64(max(1, w-1))
		val := sampleSmooth(bands, t)
		c := gradient3(low, mid, high, float32(t))
		up := val * maxUp
		crest := float64(horizon) - up
		for y := int(crest); y < horizon; y++ {
			frac := float32((float64(horizon) - float64(y)) / math.Max(1, up)) // 0 at horizon, 1 at crest
			p.glow(x, y, c, 0.14+0.55*frac*frac)
		}
		// Crest line and halo.
		cy := int(crest)
		p.glow(x, cy, mix(c, white, 0.45), 1)
		p.glow(x, cy+1, c, 0.9)
		for d := 1; d <= 7; d++ {
			p.glow(x, cy-d, c, 0.55*float32(math.Exp(-float64(d)/2.2)))
		}
		// Reflection: fainter, shorter, with scan-line shimmer.
		down := val * maxDown * 0.8
		for y := horizon; y < horizon+int(down); y++ {
			d := float64(y-horizon) / math.Max(1, down)
			shimmer := 0.65 + 0.35*math.Sin(float64(y)*0.9+float64(p.frame)*0.25+t*6)
			p.glow(x, y, c, float32((1-d)*0.32*shimmer))
		}
	}
	// A faint horizon keeps the scene anchored when the music is quiet.
	for x := range w {
		p.glow(x, horizon, mid, 0.18)
	}
}

// drawPhosphor draws the waveform as an oscilloscope trace: a hot core line
// with a wide soft glow, fading persistence (from decay), and a dim
// graticule.
func (p *PixelVis) drawPhosphor(wave []float64, low, mid [3]float32) {
	w, h := p.w, p.h
	cy := float64(h-1) / 2
	amp := float64(h)/2 - 3
	// Graticule.
	for gx := 0; gx < w; gx += max(1, w/10) {
		for y := 0; y < h; y += 3 {
			p.glow(gx, y, low, 0.10)
		}
	}
	for y := 0; y < h; y += max(1, h/4) {
		for x := 0; x < w; x += 3 {
			p.glow(x, y, low, 0.10)
		}
	}
	if len(wave) == 0 {
		for x := range w {
			p.glow(x, int(cy), low, 0.6)
		}
		return
	}
	core := mix(low, [3]float32{1, 1, 1}, 0.55)
	prev := -1
	for x := range w {
		s := wave[min(len(wave)-1, x*len(wave)/w)]
		y := int(cy - s*amp)
		y = max(1, min(h-2, y))
		if prev < 0 {
			prev = y
		}
		lo, hi := min(prev, y), max(prev, y)
		c := low
		if math.Abs(s) > 0.5 {
			c = mix(low, mid, float32(math.Min(1, (math.Abs(s)-0.5)*2)))
		}
		for yy := lo; yy <= hi; yy++ {
			p.glow(x, yy, core, 1)
			for d := 1; d <= 5; d++ {
				g := 0.6 * float32(math.Exp(-float64(d)/1.8))
				p.glow(x, yy-d, c, g)
				p.glow(x, yy+d, c, g)
			}
		}
		prev = y
	}
}

// sampleSmooth reads bands at position t in [0,1] with Catmull-Rom
// interpolation, clamped to [0,1].
func sampleSmooth(bands []float64, t float64) float64 {
	n := len(bands)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return clamp01(bands[0])
	}
	f := t * float64(n-1)
	i := int(f)
	u := f - float64(i)
	at := func(k int) float64 { return bands[max(0, min(n-1, k))] }
	p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
	v := 0.5 * (2*p1 + (-p0+p2)*u + (2*p0-5*p1+4*p2-p3)*u*u + (-p0+3*p1-3*p2+p3)*u*u*u)
	return clamp01(v)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func toByte(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v * 255)
}

func rgbf(c color.Color) [3]float32 {
	if c == nil {
		return [3]float32{0.4, 0.9, 0.5}
	}
	r, g, b, _ := c.RGBA()
	return [3]float32{float32(r) / 65535, float32(g) / 65535, float32(b) / 65535}
}

func mix(a, b [3]float32, t float32) [3]float32 {
	return [3]float32{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

func gradient3(a, b, c [3]float32, t float32) [3]float32 {
	if t < 0.5 {
		return mix(a, b, t*2)
	}
	return mix(b, c, (t-0.5)*2)
}

// PixelPalette is a fixed palette for encoding pixel visualizer frames:
// black, then intensity ramps of the three spectrum colors, their blends,
// and white-hot highlights. A fixed palette lets one Sixel encoder (and its
// lookup table) serve every frame.
func PixelPalette(cs PixelColors) []color.RGBA {
	low, mid, high := rgbf(cs.Low), rgbf(cs.Mid), rgbf(cs.High)
	white := [3]float32{1, 1, 1}
	hues := [][3]float32{low, mix(low, mid, 0.5), mid, mix(mid, high, 0.5), high, mix(low, white, 0.5), mix(mid, white, 0.5)}
	pal := []color.RGBA{{0, 0, 0, 255}}
	const steps = 14
	for _, c := range hues {
		for i := 1; i <= steps; i++ {
			k := float32(i) / steps
			pal = append(pal, color.RGBA{toByte(c[0] * k), toByte(c[1] * k), toByte(c[2] * k), 255})
		}
	}
	return append(pal, color.RGBA{255, 255, 255, 255})
}

// renderPixelText is the text rendition of a pixel mode: the same renderer
// at half-block resolution (two pixel rows per cell).
func (v *Visualizer) renderPixelText(mode VisMode) string {
	cols, rows := PanelWidth, v.Rows
	if cols <= 0 || rows <= 0 {
		return ""
	}
	if v.pixelText == nil || v.pixelText.mode != mode {
		v.pixelText = NewPixelVis(mode)
	}
	img := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	bands, wave := v.SmoothedBands(), v.waveBuf
	v.pixelText.Draw(img, bands, wave)
	return strings.Join(termimg.HalfBlocks(img), "\n")
}
