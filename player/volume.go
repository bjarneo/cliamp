package player

import (
	"math"
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// volumeStreamer applies dB gain and optional mono downmix to an audio stream.
// It reads volume and mono via atomic operations to avoid mutex contention
// with the UI thread on the audio hot path.
type volumeStreamer struct {
	s          beep.Streamer
	vol        *atomic.Uint64 // dB stored as Float64bits
	mono       *atomic.Bool
	cachedDB   float64 // last dB value used to compute cachedGain, starts NaN
	cachedGain float64 // precomputed linear gain equal to 10^(dB/20)
}

func (v *volumeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := v.s.Stream(samples)
	if n == 0 {
		return 0, ok
	}
	db := math.Float64frombits(v.vol.Load())
	mono := v.mono.Load()
	// Recompute gain only when volume changes instead of on every Stream call.
	if db != v.cachedDB {
		v.cachedGain = math.Pow(10, db/20)
		v.cachedDB = db
	}
	gain := v.cachedGain
	if gain == 1.0 && !mono {
		return n, ok
	}
	s := samples[:n]
	if mono {
		halfGain := gain * 0.5
		for i := range s {
			mid := (s[i][0] + s[i][1]) * halfGain
			s[i][0] = mid
			s[i][1] = mid
		}
		return n, ok
	}
	for i := range s {
		s[i][0] *= gain
		s[i][1] *= gain
	}
	return n, ok
}

func (v *volumeStreamer) Err() error { return v.s.Err() }
