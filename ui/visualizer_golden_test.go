//go:build amd64 && !amd64.v3

// The golden hashes pin the exact bytes that every built-in visualizer renders
// from a fixed input. The compiler fuses multiply and add on arm64 and on
// amd64.v3, which changes float rounding, so the hashes hold for plain amd64
// only.

package ui

import (
	"bufio"
	"flag"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/theme"
)

var updateVisGolden = flag.Bool("update-vis-golden", false,
	"rewrite "+visGoldenPath+" from the current renderers")

const (
	visGoldenPath   = "testdata/vis_golden.txt"
	visGoldenFrames = 48
)

// visGoldenSizes holds one roomy panel, one narrow panel, one 1-row panel
// and one wide panel. The narrow panel has fewer columns than 10 bands need
// with a gap between each pair. The layout gives the visualizer 1 row when
// the metadata pane borrows its rows. The wide panel gives each band more
// than 5 columns and makes ClassicPeak expand its 64 bands.
var visGoldenSizes = []struct{ cols, rows int }{
	{48, 9},
	{13, 3},
	{40, 1},
	{140, 16},
}

// visGoldenStep is one frame of the fixed schedule that every mode runs.
type visGoldenStep struct {
	dt      time.Duration
	playing bool
	paused  bool
	overlay bool
	silent  bool
}

// visGoldenSchedule plays, repeats a timestamp, shows an overlay, skips a long
// gap that the dt clamps must absorb, goes silent, and then pauses.
func visGoldenSchedule(frame int) visGoldenStep {
	switch {
	case frame == 12:
		return visGoldenStep{playing: true}
	case frame < 24:
		return visGoldenStep{dt: TickAnim, playing: true}
	case frame < 26:
		return visGoldenStep{dt: TickSlow, playing: true, overlay: true}
	case frame == 26:
		return visGoldenStep{dt: 2 * time.Second, playing: true}
	case frame < 34:
		return visGoldenStep{dt: TickAnim, playing: true}
	case frame < 38:
		return visGoldenStep{dt: TickAnim, playing: true, silent: true}
	default:
		return visGoldenStep{dt: TickFast, paused: true}
	}
}

// visGoldenSample is a bass sine under a sawtooth whose period changes per
// frame. Every eighth frame is a loud kick, so the transient paths run too.
func visGoldenSample(frame, i int) float64 {
	amp := 0.35
	if frame%8 == 0 {
		amp = 0.95
	}
	saw := float64((i*(7+frame%5))%97)/97 - 0.5
	bass := math.Sin(2 * math.Pi * float64(i) / 400)
	return amp * (0.6*bass + 0.8*saw)
}

// visGoldenHash ticks and renders mode through the fixed schedule and hashes
// every frame in order.
func visGoldenHash(mode VisMode, cols, rows int) uint64 {
	v := NewVisualizer(44100)
	v.Mode = mode
	v.Cols = cols
	v.Rows = rows

	samples := make([]float64, 2*defaultFFTSize)
	h := fnv.New64a()
	now := time.Unix(1_700_000_000, 0)
	for frame := range visGoldenFrames {
		step := visGoldenSchedule(frame)
		now = now.Add(step.dt)
		ctx := VisTickContext{
			Now:           now,
			Playing:       step.playing,
			Paused:        step.paused,
			OverlayActive: step.overlay,
			Analyze: func(spec VisAnalysisSpec) []float64 {
				spec = NormalizeAnalysisSpec(spec)
				if step.paused {
					return v.Analyze(nil, spec)
				}
				buf := samples[:spec.FFTSize]
				for i := range buf {
					buf[i] = 0
					if !step.silent {
						buf[i] = visGoldenSample(frame, i)
					}
				}
				return v.Analyze(buf, spec)
			},
			StereoSamplesInto: func(dst [][2]float64) int {
				for i := range dst {
					dst[i] = [2]float64{}
					if !step.silent {
						dst[i] = [2]float64{visGoldenSample(frame, i), 0.5 * visGoldenSample(frame, i+37)}
					}
				}
				return len(dst)
			},
		}
		v.Tick(ctx)
		fmt.Fprintf(h, "%s\x00", v.Render())
	}
	return h.Sum64()
}

func visGoldenKey(mode VisMode, cols, rows int) string {
	return fmt.Sprintf("%s %dx%d", visModes[mode].name, cols, rows)
}

func readVisGolden(t *testing.T) map[string]uint64 {
	t.Helper()
	f, err := os.Open(visGoldenPath)
	if err != nil {
		t.Fatalf("open golden file: %v; run go test ./ui -run TestVisualizerGoldenFrames -update-vis-golden", err)
	}
	defer f.Close()

	want := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("golden line %q: want mode, size and hash", line)
		}
		hash, err := strconv.ParseUint(fields[2], 16, 64)
		if err != nil {
			t.Fatalf("golden line %q: %v", line, err)
		}
		want[fields[0]+" "+fields[1]] = hash
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	return want
}

func writeVisGolden(t *testing.T, keys []string, got map[string]uint64) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("# FNV-64a hash of every frame that TestVisualizerGoldenFrames renders.\n")
	sb.WriteString("# Regenerate: go test ./ui -run TestVisualizerGoldenFrames -update-vis-golden\n")
	for _, key := range keys {
		fmt.Fprintf(&sb, "%s %016x\n", key, got[key])
	}
	if err := os.MkdirAll(filepath.Dir(visGoldenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visGoldenPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVisualizerGoldenFrames proves that a refactor keeps the rendered output
// of every built-in mode. A hash may change only on purpose, and the commit
// that changes it must say why.
func TestVisualizerGoldenFrames(t *testing.T) {
	ApplyThemeColors(theme.Default())

	var keys []string
	got := map[string]uint64{}
	for mode := range VisCount {
		for _, size := range visGoldenSizes {
			key := visGoldenKey(mode, size.cols, size.rows)
			keys = append(keys, key)
			got[key] = visGoldenHash(mode, size.cols, size.rows)
		}
	}

	if *updateVisGolden {
		writeVisGolden(t, keys, got)
		return
	}

	want := readVisGolden(t)
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			w, ok := want[key]
			if !ok {
				t.Fatalf("no golden hash; got %016x", got[key])
			}
			if got[key] != w {
				t.Errorf("hash = %016x, want %016x", got[key], w)
			}
		})
	}
	if len(want) != len(keys) {
		t.Errorf("golden file has %d entries, want %d", len(want), len(keys))
	}
}

// TestVisualizerGoldenHashIsStable guards the golden test itself: the same
// input must give the same hash, or the golden file proves nothing.
func TestVisualizerGoldenHashIsStable(t *testing.T) {
	for _, mode := range []VisMode{VisBars, VisSand, VisRedSector, VisStereo} {
		first := visGoldenHash(mode, 48, 9)
		if again := visGoldenHash(mode, 48, 9); again != first {
			t.Errorf("%s: hash %016x, then %016x", visModes[mode].name, first, again)
		}
	}
}
