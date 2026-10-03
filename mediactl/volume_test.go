package mediactl

import (
	"math"
	"testing"
)

// linearAt is the MPRIS value that dbToLinear gives for db above the floor.
func linearAt(db float64) float64 { return math.Pow(10, (db-6)/20) }

func TestVolumeConversionClamps(t *testing.T) {
	dbCases := []struct {
		name  string
		floor float64
		in    float64
		want  float64
	}{
		{name: "floor -30 at floor", floor: -30, in: -30, want: 0},
		{name: "floor -30 below floor", floor: -30, in: -50, want: 0},
		{name: "floor -30 above floor", floor: -30, in: -20, want: linearAt(-20)},
		{name: "floor -50 at floor", floor: -50, in: -50, want: 0},
		{name: "floor -50 keeps -40 audible", floor: -50, in: -40, want: linearAt(-40)},
		{name: "floor -90 at floor", floor: -90, in: -90, want: 0},
		{name: "floor -90 keeps -80 audible", floor: -90, in: -80, want: linearAt(-80)},
		{name: "floor 0 at floor", floor: 0, in: 0, want: 0},
		{name: "floor 0 keeps +3 audible", floor: 0, in: 3, want: linearAt(3)},
		{name: "db ceiling", floor: -50, in: 20, want: 1},
	}
	for _, tt := range dbCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := dbToLinear(tt.in, tt.floor); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("dbToLinear(%v, %v) = %v, want %v", tt.in, tt.floor, got, tt.want)
			}
		})
	}

	linearCases := []struct {
		name  string
		floor float64
		in    float64
		want  float64
	}{
		{name: "floor -30 at zero", floor: -30, in: 0, want: -30},
		{name: "floor -30 clamps small values", floor: -30, in: 0.001, want: -30},
		{name: "floor -50 at zero", floor: -50, in: 0, want: -50},
		{name: "floor -50 below zero", floor: -50, in: -1, want: -50},
		{name: "floor -50 clamps small values", floor: -50, in: 0.001, want: -50},
		{name: "floor -90 at zero", floor: -90, in: 0, want: -90},
		{name: "floor -90 keeps small values", floor: -90, in: 0.001, want: -54},
		{name: "floor 0 at zero", floor: 0, in: 0, want: 0},
		{name: "floor 0 clamps values below 0 dB", floor: 0, in: 0.25, want: 0},
		{name: "linear ceiling", floor: -50, in: 2, want: 6},
	}
	for _, tt := range linearCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := linearToDb(tt.in, tt.floor); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("linearToDb(%v, %v) = %v, want %v", tt.in, tt.floor, got, tt.want)
			}
		})
	}
}

func TestVolumeConversionRoundTrip(t *testing.T) {
	for _, floor := range []float64{0, -30, -50, -90} {
		for _, db := range []float64{-90, -80, -60, -50, -40, -30, -20, -10, -6, -3, 0, 3, 6} {
			if db < floor {
				continue
			}
			got := linearToDb(dbToLinear(db, floor), floor)
			if math.Abs(got-db) > 0.01 {
				t.Fatalf("floor %v: linearToDb(dbToLinear(%v)) = %v, want %v", floor, db, got, db)
			}
		}
	}
}
