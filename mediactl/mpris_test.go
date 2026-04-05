package mediactl

import (
	"math"
	"testing"
)

func TestLinearToDbBounds(t *testing.T) {
	if got := linearToDb(0); got != -30 {
		t.Fatalf("linearToDb(0) = %f, want -30", got)
	}
	if got := linearToDb(1); got != 6 {
		t.Fatalf("linearToDb(1) = %f, want 6", got)
	}
}

func TestLinearToDbNegative(t *testing.T) {
	if got := linearToDb(-1); got != -30 {
		t.Fatalf("linearToDb(-1) = %f, want -30", got)
	}
}

func TestLinearToDbAboveOne(t *testing.T) {
	if got := linearToDb(2); got != 6 {
		t.Fatalf("linearToDb(2) = %f, want 6", got)
	}
}

func TestLinearToDbMidpoint(t *testing.T) {
	got := linearToDb(0.5)
	if got >= 0 || got <= -30 {
		t.Fatalf("linearToDb(0.5) = %f, expected between -30 and 0", got)
	}
}

func TestLinearToDbMonotonic(t *testing.T) {
	prev := linearToDb(0.01)
	for v := 0.02; v <= 1.0; v += 0.01 {
		cur := linearToDb(v)
		if cur < prev {
			t.Fatalf("not monotonic: linearToDb(%f) = %f < linearToDb(%f) = %f", v, cur, v-0.01, prev)
		}
		prev = cur
	}
}

func TestLinearToDbSmallValue(t *testing.T) {
	got := linearToDb(0.001)
	if got != -30 {
		if got > -25 {
			t.Fatalf("linearToDb(0.001) = %f, expected <= -25", got)
		}
	}
}

func TestLinearToDbRoundTrip(t *testing.T) {
	v := math.Pow(10, -6.0/20)
	got := linearToDb(v)
	if math.Abs(got) > 0.01 {
		t.Fatalf("linearToDb(%f) = %f, expected near 0 dB", v, got)
	}
}
