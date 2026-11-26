package ui

import "strings"

// waveLeftMask maps clamped cell dot rows to left-column Braille bits.
var waveLeftMask = [4][4]byte{
	{0x01, 0x03, 0x07, 0x47},
	{0x00, 0x02, 0x06, 0x46},
	{0x00, 0x00, 0x04, 0x44},
	{0x00, 0x00, 0x00, 0x40},
}

// waveRightMask maps clamped cell dot rows to right-column Braille bits.
var waveRightMask = [4][4]byte{
	{0x08, 0x18, 0x38, 0xb8},
	{0x00, 0x10, 0x30, 0xb0},
	{0x00, 0x00, 0x20, 0xa0},
	{0x00, 0x00, 0x00, 0x80},
}

// traceYs returns the per-frame y position buffer that the wave and heartbeat
// traces share, sized to n dot columns. Callers overwrite every entry.
func (v *Visualizer) traceYs(n int) []int {
	if cap(v.traceYBuf) >= n {
		v.traceYBuf = v.traceYBuf[:n]
	} else {
		v.traceYBuf = make([]int, n)
	}
	return v.traceYBuf
}

// renderWave draws a Braille-character oscilloscope waveform from raw audio samples.
// Each Braille character covers a 2×4 dot grid, giving smooth sub-cell resolution.
func (v *Visualizer) renderWave() string {
	height := v.Rows
	charCols := v.columns()
	dotRows := height * 4
	dotCols := charCols * 2

	samples := v.waveBuf
	n := len(samples)

	// Store the vertical yMin and yMax span for each dot column in one buffer.
	spans := v.traceYs(dotCols * 2)
	prevY := 0
	for x := range dotCols {
		var sample float64
		if n > 0 {
			idx := x * n / dotCols
			if idx >= n {
				idx = n - 1
			}
			sample = samples[idx]
		}
		// Map sample [-1, 1] to dot row [0, dotRows-1]; center is dotRows/2.
		y := int((1.0 - sample) * float64(dotRows-1) / 2.0)
		y = max(0, min(dotRows-1, y))
		if x == 0 {
			prevY = y
		}
		spans[x*2] = min(y, prevY)
		spans[x*2+1] = max(y, prevY)
		prevY = y
	}

	var sb strings.Builder
	sb.Grow(height * (charCols*3 + 24))
	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		style := specANSI[specTag(float64(height-1-row)/float64(height))]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}
		dotRowStart := row * 4
		dotRowEnd := dotRowStart + 3

		for ch := range charCols {
			var mask byte
			base := ch * 4
			if yMin, yMax := spans[base], spans[base+1]; yMax >= dotRowStart && yMin <= dotRowEnd {
				mask |= waveLeftMask[max(0, yMin-dotRowStart)][min(3, yMax-dotRowStart)]
			}
			if yMin, yMax := spans[base+2], spans[base+3]; yMax >= dotRowStart && yMin <= dotRowEnd {
				mask |= waveRightMask[max(0, yMin-dotRowStart)][min(3, yMax-dotRowStart)]
			}

			sb.WriteString(brailleUTF8[mask])
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}

	return sb.String()
}
