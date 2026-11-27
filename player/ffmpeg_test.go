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
		{
			name: "f32 non-finite sanitized to zero",
			pcm: encodeF32(
				[2]float32{float32(math.NaN()), 0.5},
				[2]float32{-0.25, float32(math.NaN())},
				[2]float32{float32(math.Inf(1)), float32(math.Inf(-1))},
			),
			f32:  true,
			want: [][2]float64{{0, 0.5}, {-0.25, 0}, {0, 0}},
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
	f := &ffmpegPipe{proc: proc}
	f.setState(newPipeStreamState(0))
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
	}
	f.setState(newPipeStreamState(0))
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
				live:   tt.live,
			}
			f.setState(newPipeStreamState(0))
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
	p := &Player{sr: beep.SampleRate(100), bitDepth: 16}
	tp, err := p.buildPipeline(path, 0, nil)
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
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16)
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
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16)
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
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16)
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

func TestPCMOutputArgs(t *testing.T) {
	tests := []struct {
		name     string
		sr       beep.SampleRate
		bitDepth int
		want     string
	}{
		{name: "16 bit", sr: 44100, bitDepth: 16, want: "-f s16le -acodec pcm_s16le -ar 44100 -ac 2 -loglevel error pipe:1"},
		{name: "32 bit", sr: 48000, bitDepth: 32, want: "-f f32le -acodec pcm_f32le -ar 48000 -ac 2 -loglevel error pipe:1"},
		{name: "unknown depth falls back to 16 bit", sr: 96000, bitDepth: 24, want: "-f s16le -acodec pcm_s16le -ar 96000 -ac 2 -loglevel error pipe:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(pcmOutputArgs(tt.sr, tt.bitDepth), " "); got != tt.want {
				t.Fatalf("pcmOutputArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

// requireFFmpeg must look up PATH on each call. Tests put a fake ffmpeg on
// PATH, and a user can install ffmpeg while cliamp runs.
func TestRequireFFmpegLooksUpPATHOnEachCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	for _, installed := range []bool{false, true, false} {
		ffmpeg := filepath.Join(dir, "ffmpeg")
		if installed {
			writeExecutable(t, ffmpeg, "#!/bin/sh\n")
		} else if err := os.Remove(ffmpeg); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		err := requireFFmpeg()
		if installed && err != nil {
			t.Fatalf("requireFFmpeg() = %v with ffmpeg on PATH", err)
		}
		if !installed && (err == nil || !strings.Contains(err.Error(), "ffmpeg is required: ")) {
			t.Fatalf("requireFFmpeg() = %v without ffmpeg on PATH, want an install hint", err)
		}
		if got := ffmpegAvailable(); got != installed {
			t.Fatalf("ffmpegAvailable() = %v, want %v", got, installed)
		}
	}
}

// Every ffmpeg decoder reports the same install hint when ffmpeg is missing.
func TestFFmpegDecodersRequireFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\n")
	t.Setenv("PATH", dir)
	want := requireFFmpeg()
	if want == nil {
		t.Fatal("requireFFmpeg() = nil without ffmpeg on PATH")
	}

	tests := []struct {
		name   string
		decode func() error
	}{
		{name: "url stream", decode: func() error {
			_, _, err := decodeFFmpegStream("https://example.com/live.m3u8", 44100, 16)
			return err
		}},
		{name: "stdin stream", decode: func() error {
			_, _, err := decodeFFmpegPipeStream(io.NopCloser(bytes.NewReader(nil)), 44100, 16, true)
			return err
		}},
		{name: "local file", decode: func() error {
			_, _, err := decodeFFmpegLocal(filepath.Join(dir, "track.m4a"), 44100, 16)
			return err
		}},
		{name: "nav buffer", decode: func() error {
			_, _, err := decodeNavFFmpeg(newCompletedTestNavBuffer(t, nil), 44100, 16)
			return err
		}},
		{name: "yt-dlp pipe", decode: func() error {
			_, _, err := decodeYTDLPipe("https://www.youtube.com/watch?v=x", 44100, 16, 0)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.decode(); err == nil || err.Error() != want.Error() {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

func TestProbeFrames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	tests := []struct {
		name    string
		script  string
		timeout time.Duration
		want    int
	}{
		{name: "duration", script: "printf '2.5\\n'", timeout: ffprobeTimeout, want: 250},
		{name: "no duration", script: "printf 'N/A\\n'", timeout: ffprobeTimeout, want: 0},
		{name: "probe error", script: "exit 1", timeout: ffprobeTimeout, want: 0},
		// The child sleep keeps stdout open after the shell is killed.
		{name: "hung probe", script: "sleep 30\nprintf '2.5\\n'", timeout: 100 * time.Millisecond, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeExecutable(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\n"+tt.script+"\n")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			done := make(chan int, 1)
			go func() {
				done <- probeFramesWithin(filepath.Join(dir, "track.m4a"), beep.SampleRate(100), tt.timeout)
			}()
			select {
			case got := <-done:
				if got != tt.want {
					t.Fatalf("probeFramesWithin() = %d, want %d", got, tt.want)
				}
			case <-time.After(tt.timeout + 5*time.Second):
				t.Fatal("probeFramesWithin() did not return after its timeout")
			}
		})
	}
}

// TestPeekWithTimeout checks the three ends of an initial audio wait: audio
// arrives, the pipe closes, or the timeout passes.
func TestPeekWithTimeout(t *testing.T) {
	processEnded := errors.New("process ended")
	tests := []struct {
		name       string
		feed       func(w *io.PipeWriter)
		timeout    time.Duration
		wantErr    error
		wantText   string
		wantStop   bool
		wantClosed error
	}{
		{
			name:    "audio arrives",
			feed:    func(w *io.PipeWriter) { _, _ = w.Write([]byte{1, 2, 3, 4}) },
			timeout: 5 * time.Second,
		},
		{
			name:       "pipe closes",
			feed:       func(w *io.PipeWriter) { _ = w.Close() },
			timeout:    5 * time.Second,
			wantErr:    processEnded,
			wantClosed: io.EOF,
		},
		{
			name:     "timeout",
			feed:     func(*io.PipeWriter) {},
			timeout:  20 * time.Millisecond,
			wantText: "timed out waiting for audio data (20ms)",
			wantStop: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w := io.Pipe()
			defer w.Close()
			go tt.feed(w)
			stopped := false
			var closedWith error
			stop := func() {
				stopped = true
				_ = r.Close()
			}
			closed := func(err error) error {
				closedWith = err
				return processEnded
			}

			err := peekWithTimeout(bufio.NewReader(r), 4, tt.timeout, stop, closed)
			switch {
			case tt.wantText != "":
				if err == nil || err.Error() != tt.wantText {
					t.Fatalf("error = %v, want %q", err, tt.wantText)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if stopped != tt.wantStop {
				t.Fatalf("stop called = %v, want %v", stopped, tt.wantStop)
			}
			if !errors.Is(closedWith, tt.wantClosed) {
				t.Fatalf("closed called with %v, want %v", closedWith, tt.wantClosed)
			}
		})
	}
}

// TestPipeReport checks the Err, Len and Position that every pipe streamer
// takes from pipeReport, also before its state exists.
func TestPipeReport(t *testing.T) {
	failed := errors.New("pipe failed")
	state := newPipeStreamState(40)
	state.err.publish(failed)
	localPipe := &localFFmpegStreamer{}
	localPipe.setState(state)
	localPipe.setTotal(100)
	ytdlPipe := &ytdlPipeStreamer{}
	ytdlPipe.setState(state)
	tests := []struct {
		name    string
		decoder beep.StreamSeekCloser
		wantErr error
		wantLen int
		wantPos int
	}{
		{name: "ffmpeg pipe before start", decoder: &ffmpegPipeStreamer{}},
		{name: "ffmpeg pipe", decoder: localPipe, wantErr: failed, wantLen: 100, wantPos: 40},
		{name: "yt-dlp pipe before start", decoder: &ytdlPipeStreamer{}},
		{name: "yt-dlp pipe", decoder: ytdlPipe, wantErr: failed, wantPos: 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.decoder.Err(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Err() = %v, want %v", err, tt.wantErr)
			}
			if got := tt.decoder.Len(); got != tt.wantLen {
				t.Errorf("Len() = %d, want %d", got, tt.wantLen)
			}
			if got := tt.decoder.Position(); got != tt.wantPos {
				t.Errorf("Position() = %d, want %d", got, tt.wantPos)
			}
		})
	}
}

func TestDecodePCMFrameRejectsNonFiniteF32(t *testing.T) {
	tests := []struct {
		name string
		in   [2]float32
		want [2]float64
	}{
		{name: "finite values", in: [2]float32{0.5, -0.25}, want: [2]float64{0.5, -0.25}},
		{name: "NaN left", in: [2]float32{float32(math.NaN()), 0.5}, want: [2]float64{0, 0.5}},
		{name: "NaN right", in: [2]float32{0.5, float32(math.NaN())}, want: [2]float64{0.5, 0}},
		{name: "positive and negative Inf", in: [2]float32{float32(math.Inf(1)), float32(math.Inf(-1))}, want: [2]float64{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodePCMFrame(encodeF32(tt.in), true)
			if got != tt.want {
				t.Fatalf("decodePCMFrame() = %v, want %v", got, tt.want)
			}
		})
	}
}

type blockingCloser struct {
	mu      sync.Mutex
	closed  int
	release chan struct{}
}

func (b *blockingCloser) Close() error {
	b.mu.Lock()
	b.closed++
	b.mu.Unlock()
	if b.release != nil {
		<-b.release
	}
	return nil
}

func TestFFmpegPipeStreamDoesNotBlockOnInputOrProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixture")
	}
	cmd := exec.Command("sleep", "30")
	proc := newFFmpegProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc.startMonitor()

	bc := &blockingCloser{release: make(chan struct{})}
	f := &ffmpegPipe{
		proc:   proc,
		reader: bufio.NewReader(bytes.NewReader(nil)),
		input:  bc,
	}
	f.setState(newPipeStreamState(0))

	start := time.Now()
	n, ok := f.Stream(make([][2]float64, 4))
	elapsed := time.Since(start)
	if n != 0 || ok {
		t.Fatalf("Stream() = (%d, %v), want (0, false)", n, ok)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Stream() blocked for %v on active process", elapsed)
	}
	bc.mu.Lock()
	closedInStream := bc.closed
	bc.mu.Unlock()
	if closedInStream != 0 {
		t.Fatalf("Stream() called input.Close %d times, want 0", closedInStream)
	}

	close(bc.release)
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	bc.mu.Lock()
	closedAfterClose := bc.closed
	bc.mu.Unlock()
	if closedAfterClose == 0 {
		t.Fatal("Close() did not close input")
	}
	if proc.cmd.ProcessState == nil {
		t.Fatal("Close() did not reap FFmpeg process")
	}
}

func TestFFmpegPipePartialFinalReadReportsErrorAndTotal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	tests := []struct {
		name      string
		script    string
		wantN     int
		wantTotal int
		wantErr   string
	}{
		{
			name:      "partial final read captures mid-stream ffmpeg error",
			script:    "printf '\\000\\100\\000\\300'\nprintf 'mid-stream failure\\n' >&2\nexit 8\n",
			wantN:     1,
			wantTotal: 1,
			wantErr:   "mid-stream failure",
		},
		{
			name:      "partial final read updates unknown local total",
			script:    "printf '\\000\\100\\000\\300\\000\\100\\000\\300'\n",
			wantN:     2,
			wantTotal: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\n"+tt.script)
			writeExecutable(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\nprintf '0\\n'\n")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			decoder, _, err := decodeFFmpegLocal(filepath.Join(dir, "partial.m4a"), beep.SampleRate(100), 16)
			if err != nil {
				t.Fatalf("decodeFFmpegLocal() error = %v", err)
			}
			defer decoder.Close()

			// Request more frames than ffmpeg writes so EOF hits on a partial read.
			n, ok := decoder.Stream(make([][2]float64, 8))
			if n != tt.wantN || !ok {
				t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, tt.wantN)
			}
			if got := decoder.Len(); got != tt.wantTotal {
				t.Fatalf("Len() = %d after partial final read, want %d", got, tt.wantTotal)
			}
			if tt.wantErr != "" {
				if err := decoder.Err(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Err() = %v, want containing %q", err, tt.wantErr)
				}
			} else if err := decoder.Err(); err != nil {
				t.Fatalf("Err() = %v, want nil", err)
			}
		})
	}
}

func TestLocalFFmpegSafeFilePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	tests := []struct {
		name     string
		filename string
	}{
		{name: "leading dash", filename: "-leading-dash.m4a"},
		{name: "colon in name", filename: "http:colon-name.m4a"},
		{name: "dash and colon", filename: "-a:b.m4a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ffmpegArgsPath := filepath.Join(dir, "ffmpeg.args")
			ffprobeArgsPath := filepath.Join(dir, "ffprobe.args")
			writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '%s\n' "$@" > "$TEST_FFMPEG_ARGS"
printf '\000\100\000\300'
`)
			writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '%s\n' "$@" > "$TEST_FFPROBE_ARGS"
printf '1.0\n'
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_FFMPEG_ARGS", ffmpegArgsPath)
			t.Setenv("TEST_FFPROBE_ARGS", ffprobeArgsPath)

			target := filepath.Join(dir, tt.filename)
			decoder, _, err := decodeFFmpegLocal(target, beep.SampleRate(100), 16)
			if err != nil {
				t.Fatalf("decodeFFmpegLocal() error = %v", err)
			}
			_ = decoder.Close()

			wantInput := "file:" + target
			for label, argsFile := range map[string]string{"ffmpeg": ffmpegArgsPath, "ffprobe": ffprobeArgsPath} {
				raw, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatalf("read %s args: %v", label, err)
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				foundWhitelist := false
				foundInput := false
				for i := range lines {
					if lines[i] == "-protocol_whitelist" && i+1 < len(lines) && lines[i+1] == "file" {
						foundWhitelist = true
					}
					if lines[i] == "-i" && i+1 < len(lines) && lines[i+1] == wantInput {
						foundInput = true
					}
				}
				if !foundWhitelist {
					t.Errorf("%s args missing -protocol_whitelist file: %q", label, lines)
				}
				if !foundInput {
					t.Errorf("%s args missing -i %q: %q", label, wantInput, lines)
				}
			}
		})
	}
}

func TestNavFFmpegCloseCancelsProbeAndKillsBeforeInputDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	probeStarted := filepath.Join(dir, "probe-started")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
# Leave stdout and stdin unread to exercise full-pipe shutdown ordering.
exec sleep 30
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
: > "$TEST_PROBE_STARTED"
exec sleep 30
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_PROBE_STARTED", probeStarted)

	// Large payload fills the OS stdin pipe buffer while ffmpeg sleeps.
	payload := bytes.Repeat([]byte("A"), 512*1024)
	nb := newCompletedTestNavBuffer(t, payload)
	decoder, _, err := decodeNavFFmpeg(nb, beep.SampleRate(100), 16)
	if err != nil {
		t.Fatalf("decodeNavFFmpeg() error = %v", err)
	}
	if !waitForPath(probeStarted) {
		_ = decoder.Close()
		t.Fatal("background ffprobe did not start")
	}

	start := time.Now()
	if err := decoder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close() took %v with active probe and full stdin pipe", elapsed)
	}
	select {
	case <-decoder.probeDone:
	default:
		t.Fatal("Close() returned before probeDownloadedLength exited")
	}
	if decoder.proc.cmd.ProcessState == nil {
		t.Fatal("Close() returned before FFmpeg was reaped")
	}
}

type failAfterReadCloser struct {
	data []byte
	err  error
	read bool
}

func (r *failAfterReadCloser) Read(p []byte) (int, error) {
	if !r.read && len(r.data) > 0 {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}

func (r *failAfterReadCloser) Close() error { return nil }

func TestFFmpegPipePublishesUpstreamInputReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	wantErr := errors.New("upstream network drop")
	dir := t.TempDir()
	// Drain stdin and exit 0 cleanly, simulating ffmpeg treating truncated pipe:0 as EOF.
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
cat > /dev/null
printf '\000\100\000\300'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &failAfterReadCloser{data: []byte("audio"), err: wantErr}
	decoder, _, err := decodeFFmpegPipeStream(src, beep.SampleRate(100), 16, false)
	if err != nil {
		t.Fatalf("decodeFFmpegPipeStream() error = %v", err)
	}
	defer decoder.Close()

	buf := make([][2]float64, 8)
	for {
		_, ok := decoder.Stream(buf)
		if !ok {
			break
		}
	}
	if err := decoder.Err(); !errors.Is(err, wantErr) {
		t.Fatalf("Err() = %v, want %v", err, wantErr)
	}
}

func TestPipeInputInterruptDoesNotRecordCloseError(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	_, dstW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	in := startPipeInput(pr, dstW)
	in.interrupt()
	<-in.done
	if err := in.err(); err != nil {
		t.Fatalf("err() after interrupt = %v, want nil", err)
	}
}

func TestClampFFmpegSeekPosition(t *testing.T) {
	tests := []struct {
		name  string
		pos   int
		total int
		sr    beep.SampleRate
		want  int
	}{
		{name: "negative clamps to zero", pos: -5, total: 44100, sr: 44100, want: 0},
		{name: "unknown total passes through", pos: 12345, total: 0, sr: 44100, want: 12345},
		{name: "in range passes through", pos: 44100, total: 441000, sr: 44100, want: 44100},
		{name: "near end clamps 1s before EOF", pos: 440999, total: 441000, sr: 44100, want: 396900},
		{name: "past end clamps 1s before EOF", pos: 500000, total: 441000, sr: 44100, want: 396900},
		{name: "short clip keeps valid mid seek", pos: 8820, total: 22050, sr: 44100, want: 8820},
		{name: "short clip clamps near EOF", pos: 22049, total: 22050, sr: 44100, want: 17640},
		{name: "tiny clip clamps to zero", pos: 2000, total: 2205, sr: 44100, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampFFmpegSeekPosition(tt.pos, tt.total, tt.sr); got != tt.want {
				t.Fatalf("clampFFmpegSeekPosition(%d, %d, %d) = %d, want %d", tt.pos, tt.total, tt.sr, got, tt.want)
			}
		})
	}
}
