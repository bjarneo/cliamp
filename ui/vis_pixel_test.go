package ui

import (
	"image"
	"strings"
	"testing"
)

func TestPixelVisDrawsSomething(t *testing.T) {
	bands := []float64{0.2, 0.8, 0.5, 0.9, 0.1, 0.4}
	wave := []float64{0, 0.5, -0.5, 0.3, -0.2, 0}
	for _, mode := range []VisMode{VisAurora, VisPhosphor} {
		pv := NewPixelVis(mode)
		img := image.NewRGBA(image.Rect(0, 0, 60, 30))
		pv.Draw(img, bands, wave)
		lit := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i]|img.Pix[i+1]|img.Pix[i+2] != 0 {
				lit++
			}
		}
		if lit == 0 {
			t.Fatalf("%s drew a black frame", visModes[mode].name)
		}
		// A size change resets the trails instead of indexing out of range.
		pv.Draw(image.NewRGBA(image.Rect(0, 0, 20, 10)), bands, wave)
	}
}

func TestPixelModesRenderAsTextFallback(t *testing.T) {
	for _, mode := range []VisMode{VisAurora, VisPhosphor} {
		v := NewVisualizer(44100)
		v.Rows = 4
		defer WithPanelWidth(30)()
		out := v.renderPixelText(mode)
		lines := strings.Split(out, "\n")
		if len(lines) != 4 || strings.Count(lines[0], "▀") != 30 {
			t.Fatalf("%s text fallback: %d lines, first has %d cells", visModes[mode].name, len(lines), strings.Count(lines[0], "▀"))
		}
	}
	if !IsPixelMode(VisAurora) || !IsPixelMode(VisPhosphor) || IsPixelMode(VisBars) {
		t.Fatal("IsPixelMode misclassifies modes")
	}
}
