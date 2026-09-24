package globe

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// hemisphere is a Surface with land everywhere north of the equator.
type hemisphere struct{ lit bool }

func (h hemisphere) At(lat, _ float64) Terrain {
	switch {
	case lat <= 0:
		return Sea
	case h.lit:
		return Lit
	default:
		return Land
	}
}

// sea has no land at all.
type sea struct{}

func (sea) At(_, _ float64) Terrain { return Sea }

func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

func TestRenderShape(t *testing.T) {
	g := New(hemisphere{})
	g.SetTilt(0)
	g.Resize(40, 20)
	out := g.Render(nil, Palette{})
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20", len(lines))
	}
	for i, line := range lines {
		if n := len([]rune(line)); n != 40 {
			t.Errorf("line %d is %d cells wide, want 40", i, n)
		}
	}
	// Corners lie outside the disc and stay blank.
	for _, at := range [][2]int{{0, 0}, {0, 39}, {19, 0}, {19, 39}} {
		if r := []rune(lines[at[0]])[at[1]]; r != ' ' {
			t.Errorf("cell %v = %q, want blank", at, r)
		}
	}
	// The northern half is solid land; the southern half is sea broken
	// only by the rim and the graticule.
	countBraille := func(rows []string) (n int) {
		for _, l := range rows {
			for _, r := range l {
				if isBraille(r) {
					n++
				}
			}
		}
		return n
	}
	north, south := countBraille(lines[:10]), countBraille(lines[10:])
	if north < 2*south {
		t.Errorf("north has %d braille cells, south %d; want the land hemisphere much denser", north, south)
	}
	// The centre of the disc is land and shows all eight dots.
	if r := []rune(lines[9])[20]; r != '⣿' {
		t.Errorf("centre-north cell = %q, want full braille block", r)
	}
}

func TestRenderEmpty(t *testing.T) {
	g := New(sea{})
	if got := g.Render(nil, Palette{}); got != "" {
		t.Errorf("unsized globe rendered %q", got)
	}
	g.Resize(1, 1)
	if got := g.Render([]Mark{{Weight: 1}}, Palette{}); got != " " {
		t.Errorf("1x1 globe rendered %q, want one blank cell", got)
	}
}

func TestProjectUnprojectAgree(t *testing.T) {
	g := New(sea{})
	g.Resize(60, 30)
	g.SetLon(35)
	g.SetTilt(20)
	g.prepare()
	for _, p := range [][2]float64{{0, 35}, {20, 35}, {51.5, 0}, {-33.9, 151}, {60, 10}, {-10, -50}, {45, -100}} {
		x, y, depth := g.project(p[0], p[1])
		if depth <= 0 {
			continue
		}
		xi, yi := int(math.Round(x)), int(math.Round(y))
		d := g.dots[yi*2*g.cols+xi]
		if !d.inside {
			t.Errorf("projected %v to (%d,%d) which is outside the disc", p, xi, yi)
			continue
		}
		lat, lon := float64(d.lat), wrapLon(float64(d.lon)+g.lon)
		// The dot's own centre is up to half a dot away from the point, so
		// allow a few degrees.
		tol := 4.0 / math.Max(0.2, depth)
		if math.Abs(lat-p[0]) > tol || math.Abs(wrapLon(lon-p[1])) > tol {
			t.Errorf("point %v round-trips to (%.1f, %.1f), depth %.2f", p, lat, lon, depth)
		}
	}
}

func TestMarksAndLabels(t *testing.T) {
	g := New(sea{})
	g.SetTilt(0)
	g.Resize(60, 30)
	marks := []Mark{
		{Lat: 0, Lon: 0, Weight: 1, Label: "US 48"},
		{Lat: 0, Lon: 180, Weight: 1, Label: "far"}, // behind the globe
		{Lat: 40, Lon: -30, Weight: 0.5, Label: "DE 12"},
		{Lat: -40, Lon: 30, Weight: 0.1, Label: "BR 1"},
		{Lat: -20, Lon: 40, Weight: 0.05, Label: "fourth"}, // over the label budget
	}
	out := g.Render(marks, Palette{})
	if !strings.Contains(out, "◉ US 48") {
		t.Errorf("heaviest mark with label missing:\n%s", out)
	}
	if strings.Contains(out, "far") {
		t.Errorf("mark on the far side was drawn:\n%s", out)
	}
	if !strings.Contains(out, "● DE 12") || !strings.Contains(out, "• BR 1") {
		t.Errorf("lighter marks with labels missing:\n%s", out)
	}
	if strings.Contains(out, "fourth") {
		t.Errorf("only %d labels should be drawn:\n%s", maxLabels, out)
	}
	lines := strings.Split(out, "\n")
	if r := []rune(lines[14])[29]; r != '◉' {
		// centre of a 60x30 grid: dot (59.5, 59.5) -> cell (29, 14)
		t.Errorf("centre cell = %q, want the heaviest mark", r)
	}
}

func TestLabelFlipsLeftAtTheEdge(t *testing.T) {
	g := New(sea{})
	g.SetTilt(0)
	g.Resize(20, 10)
	// A mark on the eastern limb has no room to its right.
	out := g.Render([]Mark{{Lat: 0, Lon: 80, Weight: 1, Label: "JP 9"}}, Palette{})
	if !strings.Contains(out, "JP 9 ◉") {
		t.Errorf("label should sit left of a mark near the right edge:\n%s", out)
	}
}

func TestLitCountries(t *testing.T) {
	p := Palette{Lit: ansi.Style{}.Bold()}
	dull := New(hemisphere{}).renderAt(20, 10, p)
	bright := New(hemisphere{lit: true}).renderAt(20, 10, p)
	if strings.Contains(dull, "\x1b[1m") {
		t.Error("unlit render used the lit style")
	}
	if !strings.Contains(bright, "\x1b[1m") || !strings.Contains(bright, ansi.ResetStyle) {
		t.Error("lit render did not wrap land in the lit style")
	}
	if ansi.Strip(dull) != ansi.Strip(bright) {
		t.Error("lighting should only change styling, not the dots")
	}
}

func (g *Globe) renderAt(cols, rows int, p Palette) string {
	g.SetTilt(0)
	g.Resize(cols, rows)
	return g.Render(nil, p)
}

func TestSpinWraps(t *testing.T) {
	g := New(sea{})
	g.SetLon(170)
	g.Spin(20)
	if got := g.Lon(); math.Abs(got+170) > 1e-9 {
		t.Errorf("Lon after wrap = %v, want -170", got)
	}
	g.SetTilt(200)
	if g.tilt != 80 {
		t.Errorf("tilt clamps to 80, got %v", g.tilt)
	}
	for _, deg := range []float64{-359.9, -180, -179.9, 0, 179.9, 180, 359.9} {
		if got, want := wrapNear(deg), wrapLon(deg); math.Abs(got-want) > 1e-9 {
			t.Errorf("wrapNear(%v) = %v, wrapLon = %v", deg, got, want)
		}
	}
}
