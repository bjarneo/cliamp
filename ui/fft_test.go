package ui

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
)

func TestRealFFTPowersMatchesComplexFFT(t *testing.T) {
	sizes := []int{4, 16, 64, 512, defaultFFTSize, classicPeakFFTSize}
	for _, n := range sizes {
		r := rand.New(rand.NewPCG(uint64(n), 42))
		cases := []struct {
			name string
			fill func([]float64)
		}{
			{
				name: "sine",
				fill: func(x []float64) {
					for i := range x {
						x[i] = math.Sin(2 * math.Pi * 440 * float64(i) / 44100)
					}
				},
			},
			{
				name: "noise",
				fill: func(x []float64) {
					for i := range x {
						x[i] = r.Float64()*2 - 1
					}
				},
			},
			{
				name: "impulse",
				fill: func(x []float64) {
					x[1] = 1
				},
			},
			{
				name: "odd_tail",
				fill: func(x []float64) {
					limit := n/2 + 1
					if limit > n {
						limit = n
					}
					for i := range limit {
						x[i] = math.Cos(float64(i)*0.37) * 0.7
					}
				},
			},
		}

		wFull := buildTwiddles(n)
		wHalf := buildTwiddles(n / 2)

		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s_%d", tc.name, n), func(t *testing.T) {
				x := make([]float64, n)
				tc.fill(x)

				full := make([]complex128, n)
				for i, s := range x {
					full[i] = complex(s, 0)
				}
				fftInPlace(full, wFull)

				half := n / 2
				packed := make([]complex128, half)
				for k := range half {
					packed[k] = complex(x[2*k], x[2*k+1])
				}
				got := make([]float64, half)
				realFFTPowers(got, packed, wHalf, wFull)

				if got[0] != 0 {
					t.Fatalf("bin 0 power = %v, want 0", got[0])
				}
				for k := 1; k < half; k++ {
					re, im := real(full[k]), imag(full[k])
					want := re*re + im*im
					diff := math.Abs(got[k] - want)
					tol := 1e-9 * max(1.0, want)
					if diff > tol {
						t.Fatalf("bin %d power = %.12g, want %.12g (diff %.3g > tol %.3g)", k, got[k], want, diff, tol)
					}
				}
			})
		}
	}
}

func TestAnalyzeSilenceGateAndCacheBounds(t *testing.T) {
	t.Run("silence gate handles quiet and NaN samples", func(t *testing.T) {
		v := NewVisualizer(44100)
		spec := spectrumAnalysisSpec(DefaultSpectrumBands)
		v.prevBySpec[spec] = uniformBandsN(spec.BandCount, 0.5)

		quiet := make([]float64, defaultFFTSize)
		quiet[100] = 5e-6
		quiet[200] = math.NaN()
		got := v.Analyze(quiet, spec)
		if math.Abs(got[0]-0.4) > 1e-9 {
			t.Fatalf("quiet sample decayed to %v, want 0.4", got[0])
		}

		active := make([]float64, defaultFFTSize)
		active[len(active)-1] = -2e-5
		gotActive := v.Analyze(active, spec)
		if math.IsNaN(gotActive[0]) {
			t.Fatal("active sample produced NaN")
		}
	})

	t.Run("width-keyed caches stay bounded", func(t *testing.T) {
		v := NewVisualizer(44100)
		samples := benchSamplesSine(defaultFFTSize)
		for bands := 1; bands <= maxVisCacheEntries*4; bands++ {
			spec := VisAnalysisSpec{BandCount: bands, FFTSize: defaultFFTSize}
			out := v.Analyze(samples, spec)
			if len(out) != bands {
				t.Fatalf("Analyze bands len = %d, want %d", len(out), bands)
			}
			if len(v.prevBySpec) > maxVisCacheEntries {
				t.Fatalf("prevBySpec len = %d, exceeds cap %d", len(v.prevBySpec), maxVisCacheEntries)
			}
			if len(v.resultBufCache) > maxVisCacheEntries {
				t.Fatalf("resultBufCache len = %d, exceeds cap %d", len(v.resultBufCache), maxVisCacheEntries)
			}
			if len(v.edgeCache) > maxVisCacheEntries {
				t.Fatalf("edgeCache len = %d, exceeds cap %d", len(v.edgeCache), maxVisCacheEntries)
			}
		}
	})
}
