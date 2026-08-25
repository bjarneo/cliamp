package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestStereoMetricsKeepChannelsIndependent(t *testing.T) {
	samples := [][2]float64{{0.75, 0.25}, {-0.75, -0.25}}
	level, peak := stereoMetrics(samples)

	if level[0] <= level[1] {
		t.Fatalf("levels = %v, want left greater than right", level)
	}
	for channel := range 2 {
		if level[channel] < 0 || level[channel] > 1 {
			t.Errorf("level[%d] = %v, want [0,1]", channel, level[channel])
		}
		if peak[channel] < level[channel] || peak[channel] > 1 {
			t.Errorf("peak[%d] = %v, want [%v,1]", channel, peak[channel], level[channel])
		}
	}
}

func TestStereoDBLevel(t *testing.T) {
	tests := []struct {
		name      string
		amplitude float64
		want      float64
	}{
		{name: "silence", amplitude: 0, want: 0},
		{name: "floor", amplitude: math.Pow(10, stereoFloorDB/20), want: 0},
		{name: "unity", amplitude: 1, want: 1},
		{name: "clipped", amplitude: 2, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stereoDBLevel(tt.amplitude); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("stereoDBLevel(%v) = %v, want %v", tt.amplitude, got, tt.want)
			}
		})
	}
}

func TestStereoDriverReadsAndRendersStereoSamples(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 48
	v.Rows = 5
	activateMode(t, v, VisStereo)

	samples := [][2]float64{{0.75, 0.25}, {-0.75, -0.25}}
	calls := 0
	v.Tick(VisTickContext{
		Now:     time.Unix(1, 0),
		Playing: true,
		StereoSamplesInto: func(dst [][2]float64) int {
			calls++
			return copy(dst, samples)
		},
	})

	driver, ok := v.driverFor(VisStereo).(*stereoDriver)
	if !ok {
		t.Fatal("driverFor(VisStereo) did not return *stereoDriver")
	}
	if calls != 1 {
		t.Fatalf("StereoSamplesInto() calls = %d, want 1", calls)
	}
	if driver.targetLevel[0] <= driver.targetLevel[1] {
		t.Fatalf("target levels = %v, want left greater than right", driver.targetLevel)
	}
	if got := v.TickInterval(VisTickContext{Playing: true}); got != TickAnim {
		t.Fatalf("TickInterval(playing) = %v, want %v", got, TickAnim)
	}

	plain := ansi.Strip(v.Render())
	for _, want := range []string{"L ", "R ", "■"} {
		if !strings.Contains(plain, want) {
			t.Errorf("render missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "PHASE") {
		t.Fatalf("render contains a third phase field:\n%s", plain)
	}
	if got := strings.Count(plain, "L "); got != 1 {
		t.Errorf("L field labels = %d, want 1", got)
	}
	if got := strings.Count(plain, "R "); got != 1 {
		t.Errorf("R field labels = %d, want 1", got)
	}
}

func TestStereoPausedDecaysMetersToRest(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 48
	v.Rows = 5
	activateMode(t, v, VisStereo)
	driver := v.driverFor(VisStereo).(*stereoDriver)

	// Charge the meters with loud audio first.
	samples := [][2]float64{{0.9, 0.9}, {-0.9, -0.9}}
	t0 := time.Unix(1, 0)
	for i := range 5 {
		v.Tick(VisTickContext{
			Now:     t0.Add(time.Duration(i+1) * TickSlow),
			Playing: true,
			StereoSamplesInto: func(dst [][2]float64) int {
				return copy(dst, samples)
			},
		})
	}
	for c := range 2 {
		if driver.level[c] < 0.8 {
			t.Fatalf("level[%d] = %v after charge, want >= 0.8", c, driver.level[c])
		}
	}

	// Paused ticks empty both needles and peaks instead of freezing them.
	settled := false
	prevLevel := driver.level
	prevPeak := driver.peak
	for i := 0; i < 240; i++ {
		v.Tick(VisTickContext{Now: t0.Add(time.Duration(i+1) * TickSlow), Paused: true})
		for c := range 2 {
			if driver.level[c] > prevLevel[c]+1e-9 || driver.peak[c] > prevPeak[c]+1e-9 {
				t.Fatalf("paused tick %d channel %d level %v->%v or peak %v->%v, want monotonic decay",
					i, c, prevLevel[c], driver.level[c], prevPeak[c], driver.peak[c])
			}
		}
		prevLevel, prevPeak = driver.level, driver.peak
		if !v.PausedDecayPending(VisTickContext{Paused: true}) {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("stereo meters never settled to rest while paused")
	}
	for c := range 2 {
		if driver.level[c] > stereoEpsilon || driver.peak[c] > stereoEpsilon {
			t.Fatalf("settled channel %d level=%v peak=%v, want <= %v", c, driver.level[c], driver.peak[c], stereoEpsilon)
		}
	}
}
