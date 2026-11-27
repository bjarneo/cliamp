package ui

import "math"

var legacySpectrumEdges = [DefaultSpectrumBands + 1]float64{
	minSpectrumHz,
	100,
	200,
	400,
	800,
	1600,
	3200,
	6400,
	12800,
	16000,
	maxSpectrumHz,
}

func averageSpectrumRangeLinear(magnitudes []float64, loPos, hiPos float64) float64 {
	if len(magnitudes) == 0 {
		return 0
	}
	minPos := 1.0
	maxPos := float64(len(magnitudes) - 1)
	loPos = max(minPos, min(maxPos, loPos))
	hiPos = max(loPos, min(maxPos, hiPos))
	span := hiPos - loPos
	if span <= 0 {
		return sampleBandLinear(magnitudes, loPos)
	}
	sampleCount := max(4, min(32, int(math.Ceil(span*2))))
	var sum float64
	for i := range sampleCount {
		t := (float64(i) + 0.5) / float64(sampleCount)
		sum += sampleBandLinear(magnitudes, loPos+t*span)
	}
	return sum / float64(sampleCount)
}

func buildSpectrumEdges(count int) []float64 {
	if count <= 0 {
		return nil
	}
	edges := make([]float64, count+1)
	lastAnchor := len(legacySpectrumEdges) - 1
	for i := range count + 1 {
		numerator := i * lastAnchor
		idx := numerator / count
		if idx >= lastAnchor {
			edges[i] = legacySpectrumEdges[lastAnchor]
			continue
		}
		if numerator%count == 0 {
			edges[i] = legacySpectrumEdges[idx]
			continue
		}
		frac := float64(numerator%count) / float64(count)
		lo := legacySpectrumEdges[idx]
		hi := legacySpectrumEdges[idx+1]
		edges[i] = math.Pow(10, math.Log10(lo)*(1-frac)+math.Log10(hi)*frac)
	}
	return edges
}

func buildHannWindow(size int) []float64 {
	window := make([]float64, size)
	for i := range size {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}
	return window
}

// maxVisCacheEntries caps width-keyed caches so repeated terminal resizes do
// not grow maps without bound.
const maxVisCacheEntries = 16

func (v *Visualizer) prevBands(spec VisAnalysisSpec) []float64 {
	if prev, ok := v.prevBySpec[spec]; ok {
		return prev
	}
	if len(v.prevBySpec) >= maxVisCacheEntries {
		clear(v.prevBySpec)
	}
	prev := make([]float64, spec.BandCount)
	v.prevBySpec[spec] = prev
	return prev
}

func (v *Visualizer) spectrumEdges(count int) []float64 {
	if edges, ok := v.edgeCache[count]; ok {
		return edges
	}
	if len(v.edgeCache) >= maxVisCacheEntries {
		clear(v.edgeCache)
	}
	edges := buildSpectrumEdges(count)
	v.edgeCache[count] = edges
	return edges
}

func (v *Visualizer) fftBuffer(size int) []float64 {
	if buf, ok := v.fftBufCache[size]; ok {
		return buf
	}
	buf := make([]float64, size)
	v.fftBufCache[size] = buf
	return buf
}

func (v *Visualizer) fftComplexBuffer(size int) []complex128 {
	if buf, ok := v.fftCplxCache[size]; ok {
		return buf
	}
	buf := make([]complex128, size)
	v.fftCplxCache[size] = buf
	return buf
}

func (v *Visualizer) fftTwiddles(size int) []complex128 {
	if w, ok := v.fftTwiddleCache[size]; ok {
		return w
	}
	w := buildTwiddles(size)
	v.fftTwiddleCache[size] = w
	return w
}

// resultBufFor returns a reusable []float64 for Analyze output, keyed by the
// full analysis spec so different specs with the same band count don't alias.
// Avoids allocating a new slice on every tick (20x/sec).
func (v *Visualizer) resultBufFor(spec VisAnalysisSpec) []float64 {
	if buf, ok := v.resultBufCache[spec]; ok {
		clear(buf)
		return buf
	}
	if len(v.resultBufCache) >= maxVisCacheEntries {
		clear(v.resultBufCache)
	}
	buf := make([]float64, spec.BandCount)
	v.resultBufCache[spec] = buf
	return buf
}

func (v *Visualizer) hannWindow(size int) []float64 {
	if window, ok := v.windowCache[size]; ok {
		return window
	}
	window := buildHannWindow(size)
	v.windowCache[size] = window
	return window
}

func (v *Visualizer) resetSpectrumHistory() {
	if v == nil {
		return
	}
	clear(v.prevBySpec)
}

func (v *Visualizer) EnsureSampleBuf(size int) []float64 {
	size = NormalizeAnalysisSpec(VisAnalysisSpec{FFTSize: size}).FFTSize
	if cap(v.sampleBuf) < size {
		v.sampleBuf = make([]float64, size)
	} else {
		v.sampleBuf = v.sampleBuf[:size]
	}
	return v.sampleBuf
}

// Analyze runs FFT on raw audio samples and returns normalized band levels (0-1).
func (v *Visualizer) Analyze(samples []float64, spec VisAnalysisSpec) []float64 {
	spec = NormalizeAnalysisSpec(spec)

	// Store raw samples for wave mode.
	if n := len(samples); n > 0 {
		if cap(v.waveBuf) >= n {
			v.waveBuf = v.waveBuf[:n]
		} else {
			v.waveBuf = make([]float64, n)
		}
		copy(v.waveBuf, samples)
	} else {
		v.waveBuf = v.waveBuf[:0]
	}

	if spec.BandCount <= 0 {
		return nil
	}

	prev := v.prevBands(spec)
	bands := v.resultBufFor(spec)

	// Silence gate skips the FFT pipeline when input is empty or quiet.
	silent := true
	for _, s := range samples {
		if s >= 1e-5 || s <= -1e-5 {
			silent = false
			break
		}
	}
	if silent {
		for b := range spec.BandCount {
			bands[b] = prev[b] * 0.8
			prev[b] = bands[b]
		}
		return bands
	}

	// Pack real windowed samples into N/2 complex pairs and compute the power
	// spectrum through a half-size FFT.
	halfLen := spec.FFTSize / 2
	window := v.hannWindow(spec.FFTSize)
	have := min(len(samples), spec.FFTSize)
	powers := v.fftBuffer(spec.FFTSize)[:halfLen]
	if spec.FFTSize >= 4 && spec.FFTSize&(spec.FFTSize-1) == 0 {
		cbuf := v.fftComplexBuffer(halfLen)
		pairs := have / 2
		for k := range pairs {
			i := k * 2
			cbuf[k] = complex(samples[i]*window[i], samples[i+1]*window[i+1])
		}
		if have%2 == 1 {
			cbuf[pairs] = complex(samples[have-1]*window[have-1], 0)
			pairs++
		}
		for k := pairs; k < halfLen; k++ {
			cbuf[k] = 0
		}
		realFFTPowers(powers, cbuf, v.fftTwiddles(halfLen), v.fftTwiddles(spec.FFTSize))
	} else {
		cbuf := v.fftComplexBuffer(spec.FFTSize)
		for i := range have {
			cbuf[i] = complex(samples[i]*window[i], 0)
		}
		for i := have; i < spec.FFTSize; i++ {
			cbuf[i] = 0
		}
		fftInPlace(cbuf, v.fftTwiddles(spec.FFTSize))
		if halfLen > 0 {
			powers[0] = 0
		}
		for i := 1; i < halfLen; i++ {
			re := real(cbuf[i])
			im := imag(cbuf[i])
			powers[i] = re*re + im*im
		}
	}

	binHz := v.sr / float64(spec.FFTSize)
	edges := v.spectrumEdges(spec.BandCount)

	for b := range spec.BandCount {
		sum := averageSpectrumRangeLinear(powers, edges[b]/binHz, edges[b+1]/binHz)

		// Convert to dB-like scale. 10*log10(power) == 20*log10(magnitude).
		if sum > 0 {
			bands[b] = (10*math.Log10(sum) + 10) / 50
		}
		bands[b] = max(0, min(1, bands[b]))

		// Temporal smoothing: fast attack, slow decay.
		if bands[b] > prev[b] {
			bands[b] = bands[b]*0.6 + prev[b]*0.4
		} else {
			bands[b] = bands[b]*0.25 + prev[b]*0.75
		}
		prev[b] = bands[b]
	}

	return bands
}
