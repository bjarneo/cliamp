package model

const eqBandCount = 10

// EQPreset is a named 10-band EQ curve.
type EQPreset struct {
	Name  string
	Bands [eqBandCount]float64
}

// eqPresets is the ordered list of built-in EQ presets.
// Bands: 70Hz, 180Hz, 320Hz, 600Hz, 1kHz, 3kHz, 6kHz, 12kHz, 14kHz, 16kHz
var eqPresets = []EQPreset{
	{"Flat", [eqBandCount]float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	{"Rock", [eqBandCount]float64{5, 4, 2, -1, -2, 2, 4, 5, 5, 5}},
	{"Pop", [eqBandCount]float64{-1, 2, 4, 5, 4, 1, -1, -1, 1, 2}},
	{"Jazz", [eqBandCount]float64{3, 4, 2, 1, -1, -1, 1, 2, 3, 4}},
	{"Classical", [eqBandCount]float64{3, 2, 1, 0, -1, -1, 0, 2, 3, 4}},
	{"Bass Boost", [eqBandCount]float64{8, 6, 4, 2, 0, 0, 0, 0, 0, 0}},
	{"Treble Boost", [eqBandCount]float64{0, 0, 0, 0, 0, 1, 3, 5, 6, 7}},
	{"Vocal", [eqBandCount]float64{-2, -1, 1, 4, 5, 4, 2, 0, -1, -2}},
	{"Electronic", [eqBandCount]float64{6, 4, 1, -1, -2, 1, 3, 4, 5, 6}},
	{"Acoustic", [eqBandCount]float64{3, 3, 2, 0, 1, 2, 3, 3, 2, 1}},
	{"Hip-Hop", [eqBandCount]float64{7, 5, 3, 1, -1, -1, 1, 3, 3, 3}},
	{"R&B", [eqBandCount]float64{4, 6, 3, 1, -1, 1, 2, 2, 1, 0}},
	{"Loudness", [eqBandCount]float64{6, 4, 1, 0, -2, -1, 1, 4, 5, 5}},
	{"Late Night", [eqBandCount]float64{5, 3, 1, 0, -2, -1, 0, 2, 3, 3}},
	{"Podcast", [eqBandCount]float64{-3, -1, 2, 4, 4, 3, 1, -1, -2, -3}},
	{"Small Speakers", [eqBandCount]float64{7, 5, 4, 2, 1, 0, -1, 0, 1, 2}},
}
