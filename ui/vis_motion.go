package ui

import (
	"math"
	"time"
)

// easeToward moves cur towards target by the share of the gap that an
// exponential ease covers in dt seconds. The ease uses rate rise when target is
// above cur and rate fall otherwise, so a meter can attack fast and decay slow.
func easeToward(cur, target, rise, fall, dt float64) float64 {
	rate := fall
	if target > cur {
		rate = rise
	}
	return cur + (target-cur)*(1-math.Exp(-rate*dt))
}

// clampFrameDT returns the time from last to now for a meter that steps once
// per frame. A missing clock, a gap that is not positive, or a gap of more than
// maxSmoothDtFrames frames gives one frame. A pause, a sleep or a stalled frame
// thus moves the meter one frame instead of the whole gap.
func clampFrameDT(now, last time.Time, frame time.Duration) time.Duration {
	dt := frame
	if !now.IsZero() && !last.IsZero() {
		dt = now.Sub(last)
	}
	if dt <= 0 || dt > maxSmoothDtFrames*frame {
		return frame
	}
	return dt
}
