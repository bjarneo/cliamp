package ui

import (
	"math"
	"testing"
	"time"
)

func TestEaseToward(t *testing.T) {
	const rise, fall = 30.0, 5.0
	tests := []struct {
		name        string
		cur, target float64
		dt          float64
		want        float64
	}{
		{name: "rise uses rise rate", cur: 0, target: 1, dt: 0.1, want: 1 - math.Exp(-rise*0.1)},
		{name: "fall uses fall rate", cur: 1, target: 0, dt: 0.1, want: math.Exp(-fall * 0.1)},
		{name: "at target stays", cur: 0.4, target: 0.4, dt: 0.1, want: 0.4},
		{name: "zero dt stays", cur: 0.2, target: 0.9, dt: 0, want: 0.2},
		{name: "long dt reaches target", cur: 0.2, target: 0.9, dt: 100, want: 0.9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := easeToward(tt.cur, tt.target, rise, fall, tt.dt); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("easeToward(%v, %v, dt %v) = %v, want %v", tt.cur, tt.target, tt.dt, got, tt.want)
			}
		})
	}
}

func TestClampFrameDT(t *testing.T) {
	const frame = 20 * time.Millisecond
	last := time.Unix(100, 0)
	tests := []struct {
		name      string
		now, last time.Time
		want      time.Duration
	}{
		{name: "one frame", now: last.Add(frame), last: last, want: frame},
		{name: "short gap", now: last.Add(3 * time.Millisecond), last: last, want: 3 * time.Millisecond},
		{name: "cap is kept", now: last.Add(maxSmoothDtFrames * frame), last: last, want: maxSmoothDtFrames * frame},
		{name: "past cap", now: last.Add(maxSmoothDtFrames*frame + 1), last: last, want: frame},
		{name: "same instant", now: last, last: last, want: frame},
		{name: "backwards", now: last.Add(-time.Second), last: last, want: frame},
		{name: "no now", last: last, want: frame},
		{name: "no last", now: last, want: frame},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampFrameDT(tt.now, tt.last, frame); got != tt.want {
				t.Fatalf("clampFrameDT = %v, want %v", got, tt.want)
			}
		})
	}
}
