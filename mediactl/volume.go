package mediactl

import "github.com/bjarneo/cliamp/internal/playback"

func dbToLinear(db float64) float64 {
	return playback.DBToLinear(db)
}

func linearToDb(v float64) float64 {
	return playback.LinearToDB(v)
}
