package mediactl

import "math"

func dbToLinear(db float64) float64 {
	if db <= -30 {
		return 0.0
	}
	if db >= 6 {
		return 1.0
	}
	return math.Pow(10, db/20) / math.Pow(10, 6.0/20)
}

func linearToDb(v float64) float64 {
	if v <= 0 {
		return -30
	}
	if v >= 1 {
		return 6
	}
	db := 20*math.Log10(v) + 6
	if db < -30 {
		return -30
	}
	return db
}
