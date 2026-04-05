package mpris

import (
	"math"
	"testing"
)

func TestLinearToDbBounds(t *testing.T) {
	if got := LinearToDb(0); got != -30 {
		t.Fatalf("LinearToDb(0) = %f, want -30", got)
	}
	if got := LinearToDb(1); got != 6 {
		t.Fatalf("LinearToDb(1) = %f, want 6", got)
	}
}

func TestLinearToDbNegative(t *testing.T) {
	if got := LinearToDb(-1); got != -30 {
		t.Fatalf("LinearToDb(-1) = %f, want -30", got)
	}
}

func TestLinearToDbAboveOne(t *testing.T) {
	if got := LinearToDb(2); got != 6 {
		t.Fatalf("LinearToDb(2) = %f, want 6", got)
	}
}

func TestLinearToDbMidpoint(t *testing.T) {
	// 0.5 linear should produce a negative dB value
	got := LinearToDb(0.5)
	if got >= 0 || got <= -30 {
		t.Fatalf("LinearToDb(0.5) = %f, expected between -30 and 0", got)
	}
}

func TestLinearToDbMonotonic(t *testing.T) {
	prev := LinearToDb(0.01)
	for v := 0.02; v <= 1.0; v += 0.01 {
		cur := LinearToDb(v)
		if cur < prev {
			t.Fatalf("not monotonic: LinearToDb(%f) = %f < LinearToDb(%f) = %f", v, cur, v-0.01, prev)
		}
		prev = cur
	}
}

func TestLinearToDbSmallValue(t *testing.T) {
	got := LinearToDb(0.001)
	if got != -30 {
		// Very small linear values should clamp to -30
		if got > -25 {
			t.Fatalf("LinearToDb(0.001) = %f, expected <= -25", got)
		}
	}
}

func TestLinearToDbRoundTrip(t *testing.T) {
	// LinearToDb formula: 20*log10(v) + 6
	// So LinearToDb(1.0) = 6, LinearToDb at 0dB means 20*log10(v) + 6 = 0 → v = 10^(-6/20) ≈ 0.501
	v := math.Pow(10, -6.0/20)
	got := LinearToDb(v)
	if math.Abs(got) > 0.01 {
		t.Fatalf("LinearToDb(%f) = %f, expected near 0 dB", v, got)
	}
}
