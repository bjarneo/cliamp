package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The LCG constants are part of every seeded animation, so the stream must
// stay the same bit for bit.
func TestLCGNext(t *testing.T) {
	tests := []struct {
		name string
		seed uint64
		want []uint64
	}{
		{name: "zero seed", seed: 0, want: []uint64{167951807, 218396424, 1299921937}},
		{name: "sand seed", seed: 0x5A4D5A4D5A4D, want: []uint64{895402528, 2039119844, 1944890791}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.seed
			for i, want := range tt.want {
				if got := lcgNext(&state); got != want {
					t.Fatalf("draw %d = %d, want %d", i, got, want)
				}
			}
		})
	}
}

func TestRng64UsesLCGNext(t *testing.T) {
	a, b := uint64(0xFEED5EED), uint64(0xFEED5EED)
	for i := range 8 {
		want := float64(lcgNext(&b)%1000) / 1000.0
		if got := rng64(&a); got != want {
			t.Fatalf("draw %d = %v, want %v", i, got, want)
		}
	}
}

func TestPackBraille(t *testing.T) {
	// dots lists the lit (x, y) dots of a mask that is cols*2 wide.
	type dot struct{ x, y int }
	tests := []struct {
		name       string
		rows, cols int
		dots       []dot
		want       string
	}{
		{name: "blank cell", rows: 1, cols: 1, want: "⠀"},
		{name: "top left dot", rows: 1, cols: 1, dots: []dot{{0, 0}}, want: "⠁"},
		{name: "bottom right dot", rows: 1, cols: 1, dots: []dot{{1, 3}}, want: "⢀"},
		{name: "full cell", rows: 1, cols: 1,
			dots: []dot{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 0}, {1, 1}, {1, 2}, {1, 3}}, want: "⣿"},
		{name: "second column", rows: 1, cols: 2, dots: []dot{{3, 1}}, want: "⠀⠐"},
		{name: "second row", rows: 2, cols: 1, dots: []dot{{0, 4}}, want: "⠀\n⠁"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dotCols := tt.cols * 2
			mask := make([]bool, tt.rows*4*dotCols)
			for _, d := range tt.dots {
				mask[d.y*dotCols+d.x] = true
			}
			got := ansi.Strip(packBraille(mask, dotCols, tt.rows, tt.cols, specRowLevel))
			if got != tt.want {
				t.Fatalf("packBraille = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPackBrailleColoursEachRow(t *testing.T) {
	mask := make([]bool, 3*4*2)
	lines := strings.Split(packBraille(mask, 2, 3, 1, specRowLevel), "\n")
	want := []string{
		specWrap(specRowLevel(0, 3), "⠀"),
		specWrap(specRowLevel(1, 3), "⠀"),
		specWrap(specRowLevel(2, 3), "⠀"),
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestDotMaskForReusesAndClears(t *testing.T) {
	v := NewVisualizer(44100)
	first := v.dotMaskFor(16)
	for i := range first {
		first[i] = true
	}
	tests := []struct {
		name string
		n    int
	}{
		{name: "same size", n: 16},
		{name: "smaller", n: 8},
		{name: "larger", n: 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mask := v.dotMaskFor(tt.n)
			if len(mask) != tt.n {
				t.Fatalf("len = %d, want %d", len(mask), tt.n)
			}
			for i, on := range mask {
				if on {
					t.Fatalf("dot %d still lit from the previous frame", i)
				}
			}
			for i := range mask {
				mask[i] = true
			}
		})
	}
	large := v.dotMask
	if again := v.dotMaskFor(16); &again[0] != &large[0] {
		t.Error("dotMaskFor reallocated a buffer that was large enough")
	}
}

// The render-only Braille modes share one dot mask, one tier grid and one y
// buffer. A mode must draw the same frame whichever mode used them before.
func TestSharedDotBuffersDoNotLeakBetweenModes(t *testing.T) {
	modes := []VisMode{
		VisWave, VisScope, VisHeartbeat, VisFirework, VisBubbles,
		VisLogo, VisSakura, VisButterfly, VisFirefly, VisMirror,
	}
	prime := func(mode VisMode) *Visualizer {
		v := NewVisualizer(44100)
		v.Cols, v.Rows, v.Mode = 24, 4, mode
		for i := range v.bands {
			v.bands[i] = 0.2 + 0.07*float64(i)
		}
		v.waveBuf = make([]float64, 512)
		for i := range v.waveBuf {
			v.waveBuf[i] = float64((i*13)%50)/50 - 0.5
		}
		return v
	}
	for _, next := range modes {
		want := prime(next).Render()
		for _, prev := range modes {
			if prev == next {
				continue
			}
			t.Run(visModes[prev].name+"_then_"+visModes[next].name, func(t *testing.T) {
				v := prime(prev)
				v.Render()
				v.Mode = next
				if got := v.Render(); got != want {
					t.Errorf("frame differs after %s:\n%s\nwant:\n%s",
						visModes[prev].name, ansi.Strip(got), ansi.Strip(want))
				}
			})
		}
	}
}

func TestBrailleGridRenderPalettes(t *testing.T) {
	spec := func(tier int, body string) string {
		var sb, run strings.Builder
		run.WriteString(body)
		flushSpectrumTier(&sb, &run, tier)
		return sb.String()
	}
	star := func(tag int, body string) string {
		var sb, run strings.Builder
		run.WriteString(body)
		flushRedSectorRun(&sb, &run, tag)
		return sb.String()
	}
	tests := []struct {
		name  string
		grid  brailleGrid
		tiers [3]int8 // tier of the top-left dot of each cell
		want  string
	}{
		{
			name:  "spectrum blank cell breaks the run",
			grid:  brailleGrid{},
			tiers: [3]int8{3, 0, 3},
			want:  spec(3, "⠁") + spec(1, "⠀") + spec(3, "⠁"),
		},
		{
			name:  "spectrum keeps a run of one tier",
			grid:  brailleGrid{},
			tiers: [3]int8{2, 2, 1},
			want:  spec(2, "⠁⠁") + spec(1, "⠁"),
		},
		{
			name:  "red sector blank cell joins the run",
			grid:  newRedSectorGrid(),
			tiers: [3]int8{redSectorTagLow, 0, redSectorTagLow},
			want:  star(redSectorTagLow, "⠁⠀⠁"),
		},
		{
			name:  "red sector leading blank is unstyled",
			grid:  newRedSectorGrid(),
			tiers: [3]int8{0, 2, redSectorTagHigh},
			want:  star(0, "⠀") + star(2, "⠁") + star(redSectorTagHigh, "⠁"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.grid
			g.ensure(4, 6)
			for col, tier := range tt.tiers {
				g.set(col*2, 0, tier)
			}
			if got := g.render(1, 3); got != tt.want {
				t.Fatalf("render = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrailleGridResizeKeepsCells(t *testing.T) {
	tests := []struct {
		name       string
		rows, cols int
		keep       bool
	}{
		{name: "same size keeps the cells", rows: 4, cols: 2, keep: true},
		{name: "new size starts empty", rows: 8, cols: 2, keep: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var g brailleGrid
			g.resize(4, 2)
			g.set(1, 1, 2)
			g.resize(tt.rows, tt.cols)
			if got := g.cells[1*g.dotCols+1] == 2; got != tt.keep {
				t.Fatalf("cell kept = %v, want %v", got, tt.keep)
			}
			g.ensure(tt.rows, tt.cols)
			for i, c := range g.cells {
				if c != 0 {
					t.Fatalf("ensure left cell %d at tier %d", i, c)
				}
			}
		})
	}
}
