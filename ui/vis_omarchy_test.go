package ui

import (
	"strings"
	"testing"
)

// The wordmark arrives as half-block art, so decoding it is where a silent
// mistake would live: an off-by-one in the row pairing puts every pixel on the
// wrong line and nothing else in the package would notice.
func TestOmarchyWordmarkDecodes(t *testing.T) {
	g := decodeHalfBlockArt(omarchyWordmarkArt)

	if g.w != 81 {
		t.Errorf("wordmark width = %d, want 81 (the lattice omarchy.org uses)", g.w)
	}
	if want := len(omarchyWordmarkArt) * 2; g.h != want {
		t.Errorf("wordmark height = %d, want %d (two pixel rows per line of art)", g.h, want)
	}

	// "███" on the third line of art is a solid block: both pixel rows lit.
	line := []rune(omarchyWordmarkArt[2])
	col := 0
	for col < len(line) && line[col] != '█' {
		col++
	}
	if col == len(line) {
		t.Fatal("no full block found on the third line of art")
	}
	if !g.at(col, 4) || !g.at(col, 5) {
		t.Errorf("full block at art column %d should light both pixel rows 4 and 5", col)
	}

	// Nothing is lit outside the glyph, whichever side you ask from.
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {g.w, 0}, {0, g.h}} {
		if g.at(p[0], p[1]) {
			t.Errorf("at(%d, %d) outside the glyph should be false", p[0], p[1])
		}
	}
}

func TestOmarchyMarkFor(t *testing.T) {
	word := decodeHalfBlockArt(omarchyWordmarkArt)
	square := decodeBitRows(omarchySquareBits)

	cases := []struct {
		name           string
		pxRows, pxCols int
		wantW          int // 0 means no mark at all
		wantScale      int
	}{
		{"a five-row panel holds neither", 10, 76, 0, 0},
		{"narrow but tall falls back to the square", 40, 40, square.w, 2},
		{"wide enough for the wordmark", word.h + 2, word.w + 2, word.w, 1},
		{"plenty of room caps the scale", 400, 400, word.w, omarchyMaxScale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, scale := omarchyMarkFor(tc.pxRows, tc.pxCols)
			if g.w != tc.wantW || scale != tc.wantScale {
				t.Errorf("omarchyMarkFor(%d, %d) = width %d at scale %d, want width %d at scale %d",
					tc.pxRows, tc.pxCols, g.w, scale, tc.wantW, tc.wantScale)
			}
		})
	}
}

// The mark is the one part of the field that does not dissolve: it is stamped
// from the bitmap every frame, whatever the music is doing. Silence is the case
// worth pinning, because every other pixel is free to go dark there.
func TestOmarchyMarkSurvivesSilence(t *testing.T) {
	defer restorePanelWidth(PanelWidth)
	PanelWidth = 100

	v := NewVisualizer(44100)
	v.Rows = 14
	silent := make([]float64, DefaultSpectrumBands)

	pxRows, pxCols := v.Rows*2, PanelWidth
	mark, scale := omarchyMarkFor(pxRows, pxCols)
	if scale == 0 {
		t.Fatalf("panel %dx%d should be large enough for a mark", pxRows, pxCols)
	}
	markX := (pxCols - mark.w*scale) / 2
	markY := (pxRows - mark.h*scale) / 2

	lines := strings.Split(stripSGR(v.renderOmarchy(silent)), "\n")
	if len(lines) != v.Rows {
		t.Fatalf("got %d lines, want %d", len(lines), v.Rows)
	}

	// Every lit pixel of the mark's top half has to show up on screen, and it
	// sits inside the cleared zone, so nothing else could have put it there.
	checked, missing := 0, 0
	for y := 0; y < mark.h*scale && markY+y < pxRows; y++ {
		for x := 0; x < mark.w*scale && markX+x < pxCols; x++ {
			if !mark.at(x/scale, y/scale) {
				continue
			}
			checked++
			row := []rune(lines[(markY+y)/2])
			if markX+x < len(row) && row[markX+x] == ' ' {
				missing++
			}
		}
	}
	if checked == 0 {
		t.Fatal("the mark has no lit pixels to check")
	}
	if missing > 0 {
		t.Errorf("%d of %d lit mark pixels are blank in silence", missing, checked)
	}
}

func TestOmarchyRowCountMatchesPanel(t *testing.T) {
	defer restorePanelWidth(PanelWidth)
	PanelWidth = 40

	v := NewVisualizer(44100)
	bands := make([]float64, DefaultSpectrumBands)
	for i := range bands {
		bands[i] = 0.5
	}
	for _, rows := range []int{1, 3, 5, 12, 30} {
		v.Rows = rows
		got := strings.Count(v.renderOmarchy(bands), "\n") + 1
		if got != rows {
			t.Errorf("Rows=%d produced %d lines, want %d", rows, got, rows)
		}
	}
}

func restorePanelWidth(w int) { PanelWidth = w }

// stripSGR removes the colour runs so a test can look at the shape alone.
func stripSGR(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = min(j+1, len(s))
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
