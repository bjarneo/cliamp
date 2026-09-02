package openmpt

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// testModuleBytes builds a minimal, valid ProTracker MOD file in memory: one
// pattern, four channels, a single short sample triggered by two notes. It
// exists so tests don't depend on a real-world fixture file.
func testModuleBytes(t *testing.T) []byte {
	t.Helper()

	buf := &bytes.Buffer{}

	title := make([]byte, 20)
	copy(title, "TESTMOD")
	buf.Write(title)

	const sampleWords = 200 // 400 bytes
	for i := 0; i < 31; i++ {
		ih := make([]byte, 30)
		if i == 0 {
			copy(ih[0:22], "smp1")
			binary.BigEndian.PutUint16(ih[22:24], uint16(sampleWords))
			ih[25] = 64 // volume
			binary.BigEndian.PutUint16(ih[28:30], 1)
		}
		buf.Write(ih)
	}

	buf.WriteByte(1)   // song length
	buf.WriteByte(127) // restart byte
	buf.Write(make([]byte, 128))
	buf.WriteString("M.K.")

	pattern := make([]byte, 64*4*4)
	putCell(pattern, 0, 0, 1, 428)
	putCell(pattern, 16, 1, 1, 214)
	buf.Write(pattern)

	sample := make([]byte, sampleWords*2)
	for i := range sample {
		v := math.Sin(2 * math.Pi * float64(i) / 64.0)
		sample[i] = byte(int8(v * 100))
	}
	buf.Write(sample)

	return buf.Bytes()
}

func putCell(pattern []byte, row, channel, sampleNum, period int) {
	off := (row*4 + channel) * 4
	pattern[off+0] = byte(sampleNum&0xF0) | byte((period>>8)&0x0F)
	pattern[off+1] = byte(period & 0xFF)
	pattern[off+2] = byte((sampleNum & 0x0F) << 4)
}

func TestAvailable(t *testing.T) {
	if !Available() {
		t.Skip("libopenmpt not installed in this environment")
	}
}

func TestOpenAndRender(t *testing.T) {
	if !Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	mod, err := Open(testModuleBytes(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer mod.Close()

	if d := mod.DurationSeconds(); d <= 0 {
		t.Errorf("DurationSeconds() = %v, want > 0", d)
	}

	const sampleRate = 44100
	buf := make([]float32, sampleRate*2) // 1 second, stereo interleaved
	total := 0
	sawNonZero := false
	for i := 0; i < 20; i++ { // render up to ~20s worth, well past the module's length
		n := mod.ReadInterleavedFloatStereo(sampleRate, buf)
		if n == 0 {
			break
		}
		for j := 0; j < n*2; j++ {
			if buf[j] != 0 {
				sawNonZero = true
			}
		}
		total += n
	}
	if total == 0 {
		t.Fatal("ReadInterleavedFloatStereo never returned any frames")
	}
	if !sawNonZero {
		t.Error("rendered audio was entirely silent, expected the sample's note to be audible")
	}
}

func TestOpenRejectsGarbage(t *testing.T) {
	if !Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	_, err := Open([]byte("this is not a tracker module"))
	if err == nil {
		t.Fatal("Open() on garbage bytes: got nil error, want an error")
	}
	// libopenmpt's create_from_memory2 hands back a real, allocated error
	// message (freed via openmpt_free_string in takeCString) - make sure
	// that's what actually surfaces, not the "(error code N)" fallback
	// Open uses when the message pointer comes back NULL.
	if strings.Contains(err.Error(), "error code") {
		t.Errorf("Open() error = %q, want libopenmpt's real error message, not the numeric-code fallback", err.Error())
	}
}

func TestOpenRejectsEmpty(t *testing.T) {
	if _, err := Open(nil); err == nil {
		t.Error("Open(nil): got nil error, want an error")
	}
}

func TestSeek(t *testing.T) {
	if !Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	mod, err := Open(testModuleBytes(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer mod.Close()

	duration := mod.DurationSeconds()
	target := duration / 2
	got := mod.SetPositionSeconds(target)
	if math.Abs(got-target) > 0.5 {
		t.Errorf("SetPositionSeconds(%v) = %v, want close to %v", target, got, target)
	}
	if pos := mod.PositionSeconds(); math.Abs(pos-got) > 0.01 {
		t.Errorf("PositionSeconds() = %v, want close to %v (the seek target actually reached)", pos, got)
	}
}
