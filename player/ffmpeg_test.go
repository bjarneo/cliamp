package player

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

type readResult struct {
	data  []byte
	err   error
	reads int
}

type pipeErrorStreamer interface {
	Stream([][2]float64) (int, bool)
	Err() error
}

func testPipeErrConcurrentWithStream(t *testing.T, streamer pipeErrorStreamer, want error) {
	t.Helper()
	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				select {
				case <-done:
					return
				default:
					_ = streamer.Err()
				}
			}
		}()
	}
	close(start)
	if n, ok := streamer.Stream(make([][2]float64, 1)); n != 0 || ok {
		t.Fatalf("Stream() = (%d, %v), want (0, false)", n, ok)
	}
	close(done)
	wg.Wait()
	if !errors.Is(streamer.Err(), want) {
		t.Fatalf("Err() = %v, want %v", streamer.Err(), want)
	}
}

func (r *readResult) Read(p []byte) (int, error) {
	r.reads++
	if r.data == nil {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = nil
	return n, r.err
}

func encodeS16(frames ...[2]int16) []byte {
	pcm := make([]byte, len(frames)*pcmFrameSize16)
	for i, frame := range frames {
		off := i * pcmFrameSize16
		binary.LittleEndian.PutUint16(pcm[off:off+2], uint16(frame[0]))
		binary.LittleEndian.PutUint16(pcm[off+2:off+4], uint16(frame[1]))
	}
	return pcm
}

func encodeF32(frames ...[2]float32) []byte {
	pcm := make([]byte, len(frames)*pcmFrameSize32)
	for i, frame := range frames {
		off := i * pcmFrameSize32
		binary.LittleEndian.PutUint32(pcm[off:off+4], math.Float32bits(frame[0]))
		binary.LittleEndian.PutUint32(pcm[off+4:off+8], math.Float32bits(frame[1]))
	}
	return pcm
}

func TestStreamFromReaderDecodesBlock(t *testing.T) {
	tests := []struct {
		name string
		pcm  []byte
		f32  bool
		want [][2]float64
	}{
		{
			name: "s16",
			pcm:  encodeS16([2]int16{16384, -16384}, [2]int16{32767, -32768}, [2]int16{0, 8192}, [2]int16{-8192, 0}),
			want: [][2]float64{{0.5, -0.5}, {32767.0 / 32768, -1}, {0, 0.25}, {-0.25, 0}},
		},
		{
			name: "f32",
			pcm:  encodeF32([2]float32{0.5, -0.5}, [2]float32{1, -1}, [2]float32{0.25, 0.75}),
			f32:  true,
			want: [][2]float64{{0.5, -0.5}, {1, -1}, {0.25, 0.75}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &readResult{data: tt.pcm}
			reader := bufio.NewReaderSize(src, 16)
			samples := make([][2]float64, len(tt.want))
			var pcmBuf []byte
			state := newPipeStreamState(0)

			n, ok := streamFromReader(reader, samples, &pcmBuf, tt.f32, state)
			if n != len(tt.want) || !ok {
				t.Fatalf("streamFromReader() = (%d, %v), want (%d, true)", n, ok, len(tt.want))
			}
			if state.err.load() != nil {
				t.Fatalf("streamFromReader() error = %v", state.err.load())
			}
			if src.reads != 1 {
				t.Fatalf("source reads = %d, want 1 block read", src.reads)
			}
			for i := range tt.want {
				if samples[i] != tt.want[i] {
					t.Errorf("sample %d = %v, want %v", i, samples[i], tt.want[i])
				}
			}
		})
	}
}

func TestStreamFromReaderShortReads(t *testing.T) {
	readErr := errors.New("read failure")
	tests := []struct {
		name    string
		pcm     []byte
		readErr error
		wantN   int
		wantOK  bool
		wantErr error
	}{
		{name: "EOF before data"},
		{name: "EOF after complete and partial frames", pcm: append(encodeS16([2]int16{1, 2}), 3, 4), wantN: 1, wantOK: true},
		{name: "error after complete frames", pcm: encodeS16([2]int16{1, 2}, [2]int16{3, 4}), readErr: readErr, wantN: 2, wantOK: true, wantErr: readErr},
		{name: "error after complete and partial frames", pcm: append(encodeS16([2]int16{1, 2}), 3, 4), readErr: readErr, wantN: 1, wantOK: true, wantErr: readErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &readResult{data: tt.pcm, err: tt.readErr}
			reader := bufio.NewReaderSize(src, 16)
			samples := make([][2]float64, 4)
			var pcmBuf []byte
			state := newPipeStreamState(0)

			n, ok := streamFromReader(reader, samples, &pcmBuf, false, state)
			if n != tt.wantN || ok != tt.wantOK {
				t.Fatalf("streamFromReader() = (%d, %v), want (%d, %v)", n, ok, tt.wantN, tt.wantOK)
			}
			if !errors.Is(state.err.load(), tt.wantErr) {
				t.Fatalf("streamFromReader() error = %v, want %v", state.err.load(), tt.wantErr)
			}
		})
	}
}

func TestStreamFromReaderStopsAfterFirstError(t *testing.T) {
	firstErr := errors.New("first read failure")
	src := &readResult{data: encodeS16([2]int16{1, 2}), err: firstErr}
	reader := bufio.NewReaderSize(src, 16)
	samples := make([][2]float64, 2)
	var pcmBuf []byte
	state := newPipeStreamState(0)

	if n, ok := streamFromReader(reader, samples, &pcmBuf, false, state); n != 1 || !ok {
		t.Fatalf("first streamFromReader() = (%d, %v), want (1, true)", n, ok)
	}
	if state.err.load() != firstErr {
		t.Fatalf("first error identity = %v, want %v", state.err.load(), firstErr)
	}
	if n, ok := streamFromReader(reader, samples, &pcmBuf, false, state); n != 0 || ok {
		t.Fatalf("second streamFromReader() = (%d, %v), want (0, false)", n, ok)
	}
	if src.reads != 1 {
		t.Fatalf("source reads after terminal error = %d, want 1", src.reads)
	}
	if state.err.load() != firstErr {
		t.Fatalf("terminal error identity = %v, want %v", state.err.load(), firstErr)
	}
}

func TestStreamFromReaderReusesBuffer(t *testing.T) {
	pcm := encodeS16([2]int16{1, 2}, [2]int16{3, 4})
	var src bytes.Reader
	reader := bufio.NewReader(&src)
	samples := make([][2]float64, 2)
	pcmBuf := make([]byte, len(pcm))
	state := newPipeStreamState(0)

	allocs := testing.AllocsPerRun(100, func() {
		src.Reset(pcm)
		reader.Reset(&src)
		n, ok := streamFromReader(reader, samples, &pcmBuf, false, state)
		if n != len(samples) || !ok || state.err.load() != nil {
			t.Fatalf("streamFromReader() = (%d, %v, %v)", n, ok, state.err.load())
		}
	})
	if allocs != 0 {
		t.Fatalf("streamFromReader() allocations = %v, want 0", allocs)
	}
}

func TestFFmpegPipeEmptyDestinationDoesNotWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixture")
	}
	cmd := exec.Command("sleep", "30")
	proc := newFFmpegProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &ffmpegPipe{proc: proc, state: newPipeStreamState(0)}
	defer f.stop()

	done := make(chan struct{})
	go func() {
		n, ok := f.Stream(nil)
		if n != 0 || !ok {
			t.Errorf("Stream(nil) = (%d, %v), want (0, true)", n, ok)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stream(nil) waited for the active FFmpeg process")
	}
	if proc.cmd.ProcessState != nil {
		t.Fatal("Stream(nil) reaped the active FFmpeg process")
	}
}

func TestFFmpegPipeErrConcurrentWithStream(t *testing.T) {
	readErr := errors.New("pcm read failed")
	f := &ffmpegPipe{
		reader: bufio.NewReader(&readResult{data: []byte{1}, err: readErr}),
		state:  newPipeStreamState(0),
	}
	testPipeErrConcurrentWithStream(t, f, readErr)
}

// TestFFmpegPipeLiveEOF verifies that an unexpected EOF on an infinite radio
// stream is surfaced as an error (so auto-reconnect fires), while a finite
// stream treats EOF as a clean end-of-track.
func TestFFmpegPipeLiveEOF(t *testing.T) {
	tests := []struct {
		name    string
		live    bool
		wantErr bool
	}{
		{name: "live stream EOF is an error", live: true, wantErr: true},
		{name: "finite stream EOF is clean", live: false, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &ffmpegPipe{
				reader: bufio.NewReader(bytes.NewReader(nil)), // immediate EOF
				state:  newPipeStreamState(0),
				live:   tt.live,
			}
			samples := make([][2]float64, 8)
			n, ok := f.Stream(samples)
			if n != 0 || ok {
				t.Fatalf("Stream at EOF: got n=%d ok=%v, want 0, false", n, ok)
			}
			if gotErr := f.Err() != nil; gotErr != tt.wantErr {
				t.Fatalf("Err()=%v, wantErr=%v", f.Err(), tt.wantErr)
			}
			if tt.wantErr && !errors.Is(f.Err(), io.ErrUnexpectedEOF) {
				t.Fatalf("Err()=%v, want io.ErrUnexpectedEOF", f.Err())
			}
		})
	}
}

// TestProbeNativeRateUnreadable verifies probeNativeRate fails closed (0, not
// a panic or hang) for a path ffprobe can't read — the same outcome the
// buffered-URL pipeline relies on to fall back to the device's current rate.
func TestProbeNativeRateUnreadable(t *testing.T) {
	if got := probeNativeRate("/nonexistent/path/does-not-exist.flac"); got != 0 {
		t.Errorf("probeNativeRate(nonexistent) = %d, want 0", got)
	}
}

// TestProbeNativeRateAsyncDeliversOnce verifies the async wrapper used by the
// buffered-URL pipeline sends exactly one value and doesn't hang, so the
// pipeline's select-with-timeout around it is safe to rely on.
func TestProbeNativeRateAsyncDeliversOnce(t *testing.T) {
	ch := probeNativeRateAsync("/nonexistent/path/does-not-exist.flac")
	select {
	case got := <-ch:
		if got != 0 {
			t.Errorf("probeNativeRateAsync result = %d, want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probeNativeRateAsync did not deliver a result in time")
	}
}

func TestKnownDurationMakesHTTPFFmpegFinite(t *testing.T) {
	decoder := &ffmpegPipeStreamer{ffmpegPipe: ffmpegPipe{live: true}}
	tp := &trackPipeline{decoder: decoder}
	tp.setKnownDuration(time.Minute)
	if decoder.live {
		t.Fatal("ffmpeg decoder remains live after a finite duration was supplied")
	}
}

func TestBuildPipelineNativeFallbackStreamsFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
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

	path := filepath.Join(dir, "invalid-native.wav")
	if err := os.WriteFile(path, []byte("not a native wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Player{bitDepth: 16}
	p.rate.Store(100)
	tp, err := p.buildPipeline(path)
	if err != nil {
		t.Fatalf("buildPipeline() error = %v", err)
	}
	decoder, ok := tp.decoder.(*localFFmpegStreamer)
	if !ok {
		tp.close()
		t.Fatalf("decoder type = %T, want *localFFmpegStreamer", tp.decoder)
	}
	defer tp.close()

	if decoder.Len() != 250 {
		t.Errorf("Len() = %d, want 250", decoder.Len())
	}
	samples := make([][2]float64, 1)
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("Stream() = (%d, %v), want (1, true)", n, ok)
	}
	if samples[0] != [2]float64{0.5, -0.5} {
		t.Errorf("sample = %v, want [0.5 -0.5]", samples[0])
	}
	if decoder.Position() != 1 {
		t.Errorf("Position() = %d, want 1", decoder.Position())
	}
	if err := decoder.Seek(50); err != nil {
		t.Fatalf("Seek(50) error = %v", err)
	}
	if decoder.Position() != 50 {
		t.Errorf("Position() after seek = %d, want 50", decoder.Position())
	}
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("Stream() after seek = (%d, %v), want (1, true)", n, ok)
	}
	if decoder.Position() != 51 {
		t.Errorf("Position() after resumed stream = %d, want 51", decoder.Position())
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-ss 0.500") {
		t.Errorf("ffmpeg args do not contain seek offset: %q", args)
	}

	tp.close()
	if decoder.proc == nil {
		t.Error("decoder lost FFmpeg process state")
	} else if decoder.proc.cmd.ProcessState == nil {
		t.Error("Close() did not reap FFmpeg command")
	}
	tp.decoder = nil
}

func TestDecodeFFmpegLocalReportsProcessError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf 'invalid fixture input\n' >&2
exit 7
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '0\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, _, err := decodeFFmpegLocal(filepath.Join(dir, "bad.wav"), beep.SampleRate(44100), 16)
	if err == nil {
		t.Fatal("decodeFFmpegLocal() error = nil, want process error")
	}
	if !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "invalid fixture input") {
		t.Fatalf("decodeFFmpegLocal() error = %q, want exit status and stderr", err)
	}
}

func TestLocalFFmpegStreamReportsLateProcessError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '\000\100\000\300'
printf 'decode stopped early\n' >&2
exit 9
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '1\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	decoder, _, err := decodeFFmpegLocal(filepath.Join(dir, "partial.wav"), beep.SampleRate(100), 16)
	if err != nil {
		t.Fatalf("decodeFFmpegLocal() error = %v", err)
	}
	defer decoder.Close()
	samples := make([][2]float64, 1)
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("first Stream() = (%d, %v), want (1, true)", n, ok)
	}
	if n, ok := decoder.Stream(samples); n != 0 || ok {
		t.Fatalf("second Stream() = (%d, %v), want (0, false)", n, ok)
	}
	if err := decoder.Err(); err == nil || !strings.Contains(err.Error(), "exit status 9") || !strings.Contains(err.Error(), "decode stopped early") {
		t.Fatalf("Err() = %v, want exit status and stderr", err)
	}
}

func TestLocalFFmpegKnownDurationFallback(t *testing.T) {
	decoder := &localFFmpegStreamer{ffmpegPipe: ffmpegPipe{}, sr: beep.SampleRate(100)}
	tp := &trackPipeline{decoder: decoder}
	tp.setKnownDuration(2500 * time.Millisecond)
	if decoder.Len() != 250 {
		t.Fatalf("Len() = %d, want 250", decoder.Len())
	}
}

func TestLocalFFmpegSeekFailureKeepsCurrentProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	countPath := filepath.Join(dir, "ffmpeg-count")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
n=0
if [ -f "$FFMPEG_COUNT" ]; then n=$(cat "$FFMPEG_COUNT"); fi
n=$((n + 1))
printf '%s' "$n" > "$FFMPEG_COUNT"
if [ "$n" -eq 2 ]; then
  printf 'replacement rejected\n' >&2
  exit 12
fi
dd if=/dev/zero bs=4 count=32 2>/dev/null
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '10\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_COUNT", countPath)

	decoder, _, err := decodeFFmpegLocal(filepath.Join(dir, "track.m4a"), beep.SampleRate(100), 16)
	if err != nil {
		t.Fatalf("decodeFFmpegLocal() error = %v", err)
	}
	defer decoder.Close()
	oldProc := decoder.proc

	err = decoder.Seek(100)
	if err == nil || !strings.Contains(err.Error(), "replacement rejected") {
		t.Fatalf("Seek() error = %v, want replacement failure", err)
	}
	if decoder.proc != oldProc {
		t.Fatal("failed Seek replaced the usable FFmpeg process")
	}
	samples := make([][2]float64, 1)
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("Stream() after failed Seek = (%d, %v), want (1, true)", n, ok)
	}
}

func TestNavFFmpegSeekFailureKeepsCurrentProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	countPath := filepath.Join(dir, "ffmpeg-count")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
n=0
if [ -f "$FFMPEG_COUNT" ]; then n=$(cat "$FFMPEG_COUNT"); fi
n=$((n + 1))
printf '%s' "$n" > "$FFMPEG_COUNT"
if [ "$n" -eq 2 ]; then
  printf 'nav replacement rejected\n' >&2
  exit 13
fi
printf '\000\100\000\300'
exec sleep 30
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_COUNT", countPath)

	nb := newCompletedTestNavBuffer(t, []byte("HEADpayload"))
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16, 1000)
	if err != nil {
		t.Fatalf("decodeNavFFmpeg() error = %v", err)
	}
	defer decoder.Close()
	waitForFileValue(t, countPath, "1")
	oldProc := decoder.proc

	err = decoder.Seek(100)
	if err == nil || !strings.Contains(err.Error(), "nav replacement rejected") {
		t.Fatalf("Seek() error = %v, want replacement failure", err)
	}
	if decoder.proc != oldProc {
		t.Fatal("failed Seek replaced the usable nav FFmpeg process")
	}
	samples := make([][2]float64, 1)
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("Stream() after failed Seek = (%d, %v), want (1, true)", n, ok)
	}
}

func TestNavFFmpegSeekReplaysHeaderWithTimeOffset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args")
	inputsPath := filepath.Join(dir, "ffmpeg-inputs")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '%s\n' "$*" >> "$FFMPEG_ARGS"
dd bs=1 count=4 2>/dev/null >> "$FFMPEG_INPUTS"
printf '\000\100\000\300'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_ARGS", argsPath)
	t.Setenv("FFMPEG_INPUTS", inputsPath)

	nb := newCompletedTestNavBuffer(t, []byte("HEADpayload"))
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16, 1000)
	if err != nil {
		t.Fatalf("decodeNavFFmpeg() error = %v", err)
	}
	defer decoder.Close()
	samples := make([][2]float64, 1)
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("initial Stream() = (%d, %v), want (1, true)", n, ok)
	}
	if err := decoder.Seek(100); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	if n, ok := decoder.Stream(samples); n != 1 || !ok {
		t.Fatalf("Stream() after Seek = (%d, %v), want (1, true)", n, ok)
	}

	inputs, err := os.ReadFile(inputsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(inputs) != "HEADHEAD" {
		t.Fatalf("FFmpeg inputs = %q, want both decodes to start with header", inputs)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 {
		t.Fatalf("FFmpeg invocations = %d, want 2: %q", len(lines), args)
	}
	inputAt := strings.Index(lines[1], "-i pipe:0")
	seekAt := strings.Index(lines[1], "-ss 1.000")
	if inputAt < 0 || seekAt < 0 || seekAt < inputAt {
		t.Fatalf("seek FFmpeg args = %q, want output-side -ss after pipe input", lines[1])
	}
}

func TestNavFFmpegBlockedInputCloseIsPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
exec sleep 30
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	nb := newStalledTestNavBuffer(t)
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16, 1000)
	if err != nil {
		t.Fatalf("decodeNavFFmpeg() error = %v", err)
	}

	start := time.Now()
	if err := decoder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close() took %v with blocked nav input", elapsed)
	}
	if decoder.proc.cmd.ProcessState == nil {
		t.Fatal("Close() returned before FFmpeg was reaped")
	}
}

func TestFFmpegProcessWaitIsSingleOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixture")
	}
	cmd := exec.Command("sh", "-c", "printf 'bad input\\n' >&2; exit 7")
	proc := newFFmpegProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	const waiters = 8
	errs := make(chan error, waiters)
	var wg sync.WaitGroup
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- proc.wait()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "bad input") {
			t.Errorf("wait() error = %v, want shared exit status and stderr", err)
		}
	}
}

func TestLimitedBufferBoundsStderr(t *testing.T) {
	var b limitedBuffer
	input := bytes.Repeat([]byte("x"), subprocessStderrLimit+4096)
	if n, err := b.Write(input); err != nil || n != len(input) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(input))
	}
	got := b.String()
	if len(got) > subprocessStderrLimit+len("\n[stderr truncated]") {
		t.Fatalf("captured stderr length = %d, want bounded", len(got))
	}
	if !strings.HasSuffix(got, "[stderr truncated]") {
		t.Fatalf("captured stderr missing truncation marker: %q", got[len(got)-32:])
	}
}

func newCompletedTestNavBuffer(t *testing.T, data []byte) *navBuffer {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "nav-buffer-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt(data, 0); err != nil {
		t.Fatal(err)
	}
	downloadDone := make(chan struct{})
	close(downloadDone)
	b := &navBuffer{
		wait:         make(chan struct{}),
		downloadDone: downloadDone,
		file:         file,
		path:         file.Name(),
		downloaded:   int64(len(data)),
		total:        int64(len(data)),
		done:         true,
		stallTimeout: readStallTimeout,
	}
	b.bytesIn.Store(int64(len(data)))
	return b
}

func newStalledTestNavBuffer(t *testing.T) *navBuffer {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "nav-buffer-*")
	if err != nil {
		t.Fatal(err)
	}
	downloadDone := make(chan struct{})
	close(downloadDone)
	return &navBuffer{
		wait:         make(chan struct{}),
		downloadDone: downloadDone,
		file:         file,
		path:         file.Name(),
		total:        -1,
		stallTimeout: readStallTimeout,
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkStreamFromReader(b *testing.B) {
	for _, tt := range []struct {
		name string
		f32  bool
	}{
		{name: "s16"},
		{name: "f32", f32: true},
	} {
		b.Run(tt.name, func(b *testing.B) {
			f32 := tt.f32
			fs := pcmFrameSize(f32)
			pcm := make([]byte, 1024*fs)
			samples := make([][2]float64, 1024)
			pcmBuf := make([]byte, len(pcm))
			var src bytes.Reader
			reader := bufio.NewReaderSize(&src, len(pcm))
			state := newPipeStreamState(0)

			b.ReportAllocs()
			b.SetBytes(int64(len(pcm)))
			for b.Loop() {
				src.Reset(pcm)
				reader.Reset(&src)
				streamFromReader(reader, samples, &pcmBuf, f32, state)
			}
		})
	}
}
