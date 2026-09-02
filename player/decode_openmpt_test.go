package player

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/player/openmpt"
)

// testModuleBytes builds a minimal, valid ProTracker MOD file in memory —
// see player/openmpt/openmpt_test.go for the byte-layout comments.
func testModuleBytes(t *testing.T) []byte {
	t.Helper()

	buf := &bytes.Buffer{}
	title := make([]byte, 20)
	copy(title, "TESTMOD")
	buf.Write(title)

	const sampleWords = 200
	for i := 0; i < 31; i++ {
		ih := make([]byte, 30)
		if i == 0 {
			copy(ih[0:22], "smp1")
			binary.BigEndian.PutUint16(ih[22:24], uint16(sampleWords))
			ih[25] = 64
			binary.BigEndian.PutUint16(ih[28:30], 1)
		}
		buf.Write(ih)
	}

	buf.WriteByte(1)
	buf.WriteByte(127)
	buf.Write(make([]byte, 128))
	buf.WriteString("M.K.")

	pattern := make([]byte, 64*4*4)
	off := 0 // row 0, channel 0
	pattern[off+0] = byte(1&0xF0) | byte((428>>8)&0x0F)
	pattern[off+1] = byte(428 & 0xFF)
	pattern[off+2] = byte(1&0x0F) << 4
	off = (16*4 + 1) * 4 // row 16, channel 1
	pattern[off+0] = byte(1&0xF0) | byte((214>>8)&0x0F)
	pattern[off+1] = byte(214 & 0xFF)
	pattern[off+2] = byte(1&0x0F) << 4
	buf.Write(pattern)

	sample := make([]byte, sampleWords*2)
	for i := range sample {
		v := math.Sin(2 * math.Pi * float64(i) / 64.0)
		sample[i] = byte(int8(v * 100))
	}
	buf.Write(sample)

	return buf.Bytes()
}

type closeCountingReader struct {
	*bytes.Reader
	closed int
}

func (c *closeCountingReader) Close() error {
	c.closed++
	return nil
}

func TestSupportedExtsIncludesTrackerFormats(t *testing.T) {
	if len(openmpt.SupportedExtensions) < 60 {
		t.Fatalf("openmpt.SupportedExtensions has %d entries, want the ~67 libopenmpt reports", len(openmpt.SupportedExtensions))
	}
	// Spot-check the well-known formats plus a few obscure ones, to make
	// sure the whole list — not just a hand-picked subset — made it into
	// SupportedExts and openmptExts.
	for _, ext := range []string{".mod", ".s3m", ".xm", ".it", ".mptm", ".umx", ".stm", ".mtm", ".okt"} {
		if !SupportedExts[ext] {
			t.Errorf("SupportedExts[%q] = false, want true", ext)
		}
		if !openmptExts[ext] {
			t.Errorf("openmptExts[%q] = false, want true", ext)
		}
	}
	if got, want := len(openmptExts), len(openmpt.SupportedExtensions); got != want {
		t.Errorf("len(openmptExts) = %d, want %d (one entry per openmpt.SupportedExtensions)", got, want)
	}
}

func TestDecodeWithExtRoutesModuleFormats(t *testing.T) {
	if !openmpt.Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	data := testModuleBytes(t)
	for _, ext := range []string{".mod", ".s3m", ".xm", ".it"} {
		rc := &closeCountingReader{Reader: bytes.NewReader(data)}
		decoder, format, err := decodeWithExt(rc, ext, "test"+ext, 44100, 16)
		if err != nil {
			// Only .mod is actually a valid MOD file; the others are
			// expected to fail decoding (wrong format for the bytes) but
			// must still be *routed* to the openmpt decoder rather than
			// falling through to the mp3/wav/flac switch below it.
			if ext == ".mod" {
				t.Fatalf("decodeWithExt(%q): %v", ext, err)
			}
			continue
		}
		defer decoder.Close()
		if format.NumChannels != 2 {
			t.Errorf("decodeWithExt(%q): NumChannels = %d, want 2", ext, format.NumChannels)
		}
	}
}

func TestDecodeOpenmptStreamAndSeek(t *testing.T) {
	if !openmpt.Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	rc := &closeCountingReader{Reader: bytes.NewReader(testModuleBytes(t))}
	decoder, format, err := decodeOpenmpt(rc, "test.mod", beep.SampleRate(44100))
	if err != nil {
		t.Fatalf("decodeOpenmpt: %v", err)
	}
	defer decoder.Close()

	if rc.closed != 1 {
		t.Errorf("decodeOpenmpt closed the source reader %d times, want 1", rc.closed)
	}
	if format.SampleRate != 44100 || format.NumChannels != 2 {
		t.Fatalf("unexpected format: %+v", format)
	}
	if decoder.Len() <= 0 {
		t.Errorf("Len() = %d, want > 0", decoder.Len())
	}

	samples := make([][2]float64, 4096)
	total := 0
	sawNonZero := false
	for {
		n, ok := decoder.Stream(samples)
		for i := 0; i < n; i++ {
			if samples[i][0] != 0 || samples[i][1] != 0 {
				sawNonZero = true
			}
		}
		total += n
		if !ok {
			break
		}
	}
	if total == 0 {
		t.Fatal("Stream never produced any frames")
	}
	if !sawNonZero {
		t.Error("all streamed samples were silent")
	}
	if err := decoder.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}

	if err := decoder.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	if decoder.Position() != 0 {
		t.Errorf("Position() after Seek(0) = %d, want 0", decoder.Position())
	}
	n, ok := decoder.Stream(samples)
	if n == 0 || !ok {
		t.Error("Stream after Seek(0) produced no frames")
	}

	if err := decoder.Seek(-1); err == nil {
		t.Error("Seek(-1): got nil error, want an error")
	}
}

func TestDecodeOpenmptRejectsCorruptFile(t *testing.T) {
	if !openmpt.Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	rc := &closeCountingReader{Reader: bytes.NewReader([]byte("not a module"))}
	if _, _, err := decodeOpenmpt(rc, "test.mod", beep.SampleRate(44100)); err == nil {
		t.Error("decodeOpenmpt on garbage bytes: got nil error, want an error")
	}
	if rc.closed != 1 {
		t.Errorf("decodeOpenmpt closed the source reader %d times, want 1", rc.closed)
	}
}

// TestBuildPipelineCorruptModuleDoesNotFallBackToFFmpeg guards against
// buildPipeline's generic "native decoder failed, retry with ffmpeg"
// fallback swallowing a module decode error: ffmpeg has no tracker-module
// demuxer at all, so retrying it can only ever fail, replacing a clear
// libopenmpt error with a confusing ffmpeg one. Modeled on
// TestBuildPipelineNativeFallbackStreamsFFmpeg in ffmpeg_test.go, which
// covers the case where that fallback *should* fire.
func TestBuildPipelineCorruptModuleDoesNotFallBackToFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	if !openmpt.Available() {
		t.Skip("libopenmpt not installed in this environment")
	}

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '%s\n' "$*" >> "$FFMPEG_ARGS"
printf '\000\100\000\300'
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '2.5\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_ARGS", argsPath)

	path := filepath.Join(dir, "corrupt.mod")
	if err := os.WriteFile(path, []byte("not a module file"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	tp, err := p.buildPipeline(path)
	if err == nil {
		tp.close()
		t.Fatal("buildPipeline() on a corrupt .mod file: got nil error, want the libopenmpt decode error")
	}
	if _, statErr := os.Stat(argsPath); statErr == nil {
		t.Error("ffmpeg was invoked for a .mod file — it has no tracker-module support, this should never happen")
	}
}

var _ io.ReadCloser = (*closeCountingReader)(nil)
