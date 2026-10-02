package ui

import (
	"math"
	"testing"
	"time"
)

const classicLEDTestBars = 3

// classicLEDTestDriver returns a ClassicLED driver on a panel wide enough for
// classicLEDTestBars bars, with body and peak at rest.
func classicLEDTestDriver(t *testing.T) (*Visualizer, *classicLEDDriver, time.Time) {
	t.Helper()
	v := NewVisualizer(44100)
	v.Cols = 8
	d := activateMode(t, v, VisClassicLED).(*classicLEDDriver)
	if got := classicLEDBarCount(v.Cols); got != classicLEDTestBars {
		t.Fatalf("bar count = %d, want %d", got, classicLEDTestBars)
	}
	t0 := time.Unix(10, 0)
	v.bands = uniformBandsN(classicLEDTestBars, 0)
	d.advance(v, t0)
	return v, d, t0
}

func TestClassicLEDPeakLaunchHoldFall(t *testing.T) {
	v, d, now := classicLEDTestDriver(t)
	frame := d.frameInterval()

	// Launch: while the body rises, the peak rides on top of it.
	v.bands = uniformBandsN(classicLEDTestBars, 1)
	for i := range 10 {
		now = now.Add(frame)
		d.advance(v, now)
		for b := range classicLEDTestBars {
			if d.peak[b] != d.body[b] {
				t.Fatalf("rise frame %d bar %d: peak %v, want body %v", i, b, d.peak[b], d.body[b])
			}
			if d.hold[b] != classicLEDPeakHold {
				t.Fatalf("rise frame %d bar %d: hold %v, want %v", i, b, d.hold[b], classicLEDPeakHold)
			}
		}
	}
	apex := d.peak[0]
	if apex < 0.99 {
		t.Fatalf("apex = %v after 10 rising frames, want >= 0.99", apex)
	}

	// Hold: the body drops away and the peak stays at the apex until the hold
	// time runs out.
	v.bands = uniformBandsN(classicLEDTestBars, 0)
	holdFrames := int(math.Ceil(classicLEDPeakHold / frame.Seconds()))
	for i := range holdFrames {
		now = now.Add(frame)
		d.advance(v, now)
		if d.peak[0] != apex {
			t.Fatalf("hold frame %d: peak %v, want apex %v", i, d.peak[0], apex)
		}
	}
	if d.hold[0] != 0 {
		t.Fatalf("hold = %v after %d frames, want 0", d.hold[0], holdFrames)
	}

	// Fall: the peak drops at a constant rate and never below the body.
	step := classicLEDPeakFall * frame.Seconds()
	for i := 0; d.peak[0] > d.body[0]; i++ {
		if i > 200 {
			t.Fatal("peak never met the body")
		}
		prev := d.peak[0]
		now = now.Add(frame)
		d.advance(v, now)
		if d.peak[0] < d.body[0] {
			t.Fatalf("fall frame %d: peak %v below body %v", i, d.peak[0], d.body[0])
		}
		if d.peak[0] > d.body[0] && math.Abs(prev-d.peak[0]-step) > 1e-12 {
			t.Fatalf("fall frame %d: peak fell %v, want %v", i, prev-d.peak[0], step)
		}
	}
}

// A long or backwards gap must step like one frame, not integrate the gap.
func TestClassicLEDClampsFrameGap(t *testing.T) {
	frame := (&classicLEDDriver{}).frameInterval()
	oneFrame := 1 - math.Exp(-classicLEDRiseRate*frame.Seconds())
	tests := []struct {
		name     string
		gap      time.Duration
		zeroNow  bool
		wantBody float64
	}{
		{name: "one frame", gap: frame, wantBody: oneFrame},
		{name: "five frames", gap: 5 * frame, wantBody: 1 - math.Exp(-classicLEDRiseRate*(5*frame).Seconds())},
		{name: "ten frames", gap: 10 * frame, wantBody: 1 - math.Exp(-classicLEDRiseRate*(10*frame).Seconds())},
		{name: "long gap", gap: 2 * time.Second, wantBody: oneFrame},
		{name: "same instant", gap: 0, wantBody: oneFrame},
		{name: "backwards", gap: -time.Second, wantBody: oneFrame},
		{name: "no clock", zeroNow: true, wantBody: oneFrame},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, d, t0 := classicLEDTestDriver(t)
			v.bands = uniformBandsN(classicLEDTestBars, 1)
			now := t0.Add(tt.gap)
			if tt.zeroNow {
				now = time.Time{}
			}
			d.advance(v, now)
			if math.Abs(d.body[0]-tt.wantBody) > 1e-12 {
				t.Fatalf("body = %v, want %v", d.body[0], tt.wantBody)
			}
		})
	}
}

func TestClassicLEDPausedDecaysToRest(t *testing.T) {
	v, d, t0 := classicLEDTestDriver(t)
	frame := d.frameInterval()

	// Charge the bars with loud bands first.
	for i := range 10 {
		v.Tick(VisTickContext{
			Now:     t0.Add(time.Duration(i+1) * frame),
			Playing: true,
			Analyze: func(VisAnalysisSpec) []float64 { return uniformBandsN(classicLEDTestBars, 1) },
		})
	}
	if d.body[0] < 0.9 {
		t.Fatalf("body = %v after charge, want >= 0.9", d.body[0])
	}

	// Paused ticks empty the bars and the peaks instead of freezing them.
	settled := false
	prevBody, prevPeak := d.body[0], d.peak[0]
	for i := range 240 {
		v.Tick(VisTickContext{Now: t0.Add(time.Duration(i+11) * frame), Paused: true})
		if d.body[0] > prevBody+1e-9 || d.peak[0] > prevPeak+1e-9 {
			t.Fatalf("paused tick %d: body %v->%v or peak %v->%v, want monotonic decay",
				i, prevBody, d.body[0], prevPeak, d.peak[0])
		}
		prevBody, prevPeak = d.body[0], d.peak[0]
		if !v.PausedDecayPending(VisTickContext{Paused: true}) {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("ClassicLED never settled to rest while paused")
	}
	for b := range classicLEDTestBars {
		if d.body[b] > classicLEDEpsilon || d.peak[b] > d.body[b]+classicLEDEpsilon {
			t.Fatalf("settled bar %d body=%v peak=%v", b, d.body[b], d.peak[b])
		}
	}
}
