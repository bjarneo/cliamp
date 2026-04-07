package mediactl

import (
	"math"
	"testing"
)

func TestVolumeConversionClamps(t *testing.T) {
	dbCases := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "db floor", in: -50, want: 0},
		{name: "db ceiling", in: 20, want: 1},
	}
	for _, tt := range dbCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := dbToLinear(tt.in); got != tt.want {
				t.Fatalf("dbToLinear(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}

	linearCases := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "linear floor", in: -1, want: -30},
		{name: "linear ceiling", in: 2, want: 6},
	}
	for _, tt := range linearCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := linearToDb(tt.in); got != tt.want {
				t.Fatalf("linearToDb(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestVolumeConversionRoundTrip(t *testing.T) {
	for _, db := range []float64{-30, -20, -10, -6, -3, 0, 3, 6} {
		linear := dbToLinear(db)
		got := linearToDb(linear)
		if math.Abs(got-db) > 0.01 {
			t.Fatalf("linearToDb(dbToLinear(%v)) = %v, want %v", db, got, db)
		}
	}
}
