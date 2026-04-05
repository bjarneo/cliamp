package mediactl

import (
	"math"
	"testing"
)

func TestDbToLinear_Boundaries(t *testing.T) {
	if got := dbToLinear(-30); got != 0.0 {
		t.Errorf("dbToLinear(-30) = %f, want 0.0", got)
	}
	if got := dbToLinear(-50); got != 0.0 {
		t.Errorf("dbToLinear(-50) = %f, want 0.0", got)
	}
	if got := dbToLinear(6); got != 1.0 {
		t.Errorf("dbToLinear(6) = %f, want 1.0", got)
	}
	if got := dbToLinear(20); got != 1.0 {
		t.Errorf("dbToLinear(20) = %f, want 1.0", got)
	}
}

func TestLinearToDb_Boundaries(t *testing.T) {
	if got := linearToDb(0); got != -30 {
		t.Errorf("linearToDb(0) = %f, want -30", got)
	}
	if got := linearToDb(-1); got != -30 {
		t.Errorf("linearToDb(-1) = %f, want -30", got)
	}
	if got := linearToDb(1); got != 6 {
		t.Errorf("linearToDb(1) = %f, want 6", got)
	}
	if got := linearToDb(2); got != 6 {
		t.Errorf("linearToDb(2) = %f, want 6", got)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, db := range []float64{-30, -20, -10, -6, -3, 0, 3, 6} {
		linear := dbToLinear(db)
		got := linearToDb(linear)
		if math.Abs(got-db) > 0.01 {
			t.Errorf("round-trip: linearToDb(dbToLinear(%f)) = %f, want %f", db, got, db)
		}
	}
}

func TestDbToLinear_ZeroDb(t *testing.T) {
	got := dbToLinear(0)
	want := 1.0 / math.Pow(10, 6.0/20) // ~0.501
	if math.Abs(got-want) > 0.001 {
		t.Errorf("dbToLinear(0) = %f, want ~%f", got, want)
	}
}
