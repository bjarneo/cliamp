package mediactl

import "math"

// dbToLinear converts a volume in dB to the MPRIS range 0 to 1. A volume at
// or below the engine floor in dB converts to 0.
func dbToLinear(db, floor float64) float64 {
	if db <= floor {
		return 0.0
	}
	if db >= 6 {
		return 1.0
	}
	return math.Pow(10, db/20) / math.Pow(10, 6.0/20)
}

// linearToDb converts an MPRIS volume from 0 to 1 to dB. The result is never
// below the engine floor in dB.
func linearToDb(v, floor float64) float64 {
	if v <= 0 {
		return floor
	}
	if v >= 1 {
		return 6
	}
	db := 20*math.Log10(v) + 6
	if db < floor {
		return floor
	}
	return db
}
