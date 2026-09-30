package model

// immersive_pixvis.go drives the pixel visualizers (Aurora, Phosphor) as
// Sixel in the immersive visualizer band. View hands the worker a snapshot
// of the spectrum and waveform each frame; the worker renders and encodes
// off the UI goroutine and publishes the result as the layer's live
// placement, which the output pump draws between Bubbletea frames. The band
// itself renders as blank cells while this runs.

import (
	"image"
	"image/color"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/ui/termimg"
)

// pixVisFrame is the pixel visualizer's frame budget (25 fps): enough for
// smooth motion, and it keeps the terminal's Sixel decoding light.
const pixVisFrame = 40 * time.Millisecond

// pixVisJob is one frame request, in screen cells and pixels.
type pixVisJob struct {
	mode        ui.VisMode
	colors      ui.PixelColors
	bands, wave []float64
	x, y, w, h  int // cells
	pw, ph      int // pixels
}

// pixVisWorker renders the pixel visualizer frames. Requests arriving while
// it is busy are dropped: the next frame supersedes them.
// Its goroutine starts with the first request.
type pixVisWorker struct {
	jobs  chan pixVisJob
	layer *termimg.Layer
	once  sync.Once
}

func newPixVisWorker(layer *termimg.Layer) *pixVisWorker {
	return &pixVisWorker{jobs: make(chan pixVisJob, 1), layer: layer}
}

func (w *pixVisWorker) submit(job pixVisJob) {
	w.once.Do(func() { go w.run() })
	select {
	case w.jobs <- job:
	default:
	}
}

func (w *pixVisWorker) run() {
	var (
		pv   *ui.PixelVis
		enc  *termimg.SixelEncoder
		pal  []color.RGBA
		img  *image.RGBA
		seq  int
		last time.Time
	)
	for job := range w.jobs {
		if wait := pixVisFrame - time.Since(last); wait > 0 {
			time.Sleep(wait)
			select { // render the newest request that arrived meanwhile
			case newer := <-w.jobs:
				job = newer
			default:
			}
		}
		last = time.Now()
		if pv == nil || pv.Mode() != job.mode {
			pv = ui.NewPixelVis(job.mode)
		}
		pv.SetColors(job.colors)
		if p := ui.PixelPalette(job.colors); enc == nil || !slices.Equal(p, pal) {
			pal, enc = p, termimg.NewSixelEncoder(p, 0)
		}
		if img == nil || img.Rect.Dx() != job.pw || img.Rect.Dy() != job.ph {
			img = image.NewRGBA(image.Rect(0, 0, job.pw, job.ph))
		}
		pv.Draw(img, job.bands, job.wave)
		seq++
		w.layer.SetLive(termimg.Placement{
			Key: "vis#" + strconv.Itoa(seq),
			X:   job.x, Y: job.y, W: job.w, H: job.h,
			Data: enc.Encode(img),
		})
	}
}

// immPixelVisOn reports whether the band shows a pixel visualizer as Sixel
// this frame (otherwise the text renderer draws it, pixel modes included).
func (m Model) immPixelVisOn() bool {
	return m.pixVis != nil && m.vis != nil && ui.IsPixelMode(m.vis.Mode) &&
		!m.visualizerDisabled() && m.artKindNow() == artSixel &&
		m.activeScreen() == screenImmersive
}

// feedPixelVis requests the next band frame, or clears the live image when
// the band is not showing a Sixel visualizer.
func (m Model) feedPixelVis() {
	if !m.immPixelVisOn() {
		m.imgLayer.ArmLive(0, 0, 0, 0)
		return
	}
	g := m.immGeom()
	cw, ch := m.cellPx()
	m.imgLayer.ArmLive(m.layout.paddingH, m.layout.paddingV, g.w, g.visH)
	bands, wave := m.vis.PixelInput()
	m.pixVis.submit(pixVisJob{
		mode: m.vis.Mode, colors: ui.CurrentPixelColors(),
		bands: bands, wave: wave,
		x: m.layout.paddingH, y: m.layout.paddingV, w: g.w, h: g.visH,
		pw: g.w * cw, ph: g.visH * ch,
	})
}
