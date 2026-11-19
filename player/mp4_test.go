package player

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gopxl/beep/v2"
)

// mp4Header returns a box header with a 32-bit size.
func mp4Header(typ string, size uint32) []byte {
	box := make([]byte, 8)
	binary.BigEndian.PutUint32(box, size)
	copy(box[4:], typ)
	return box
}

// mp4Box returns a box with a 32-bit size and payload zero bytes.
func mp4Box(typ string, payload int) []byte {
	return append(mp4Header(typ, uint32(8+payload)), make([]byte, payload)...)
}

// mp4LargeBox returns a box with a 64-bit size and payload zero bytes. size
// is the value in the header.
func mp4LargeBox(typ string, size uint64, payload int) []byte {
	box := append(mp4Header(typ, 1), make([]byte, 8+payload)...)
	binary.BigEndian.PutUint64(box[8:], size)
	return box
}

func joinBytes(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestMP4MdatFirst(t *testing.T) {
	ftyp := mp4Box("ftyp", 20)

	tests := []struct {
		name   string
		head   []byte
		want   bool
		wantOK bool // the head holds enough bytes for an answer
	}{
		{name: "empty"},
		{name: "shorter than a box header", head: ftyp[:7]},
		{name: "mp3 with an ID3 tag", head: []byte("ID3\x04\x00\x00\x00\x00"), wantOK: true},
		{name: "flac", head: []byte("fLaC\x00\x00\x00\x22"), wantOK: true},
		{name: "mdat without ftyp", head: mp4Header("mdat", 1000), wantOK: true},
		{name: "moov first", head: joinBytes(ftyp, mp4Box("moov", 64)), wantOK: true},
		{name: "moov after free", head: joinBytes(ftyp, mp4Box("free", 0), mp4Box("moov", 64)), wantOK: true},
		{name: "mdat after ftyp", head: joinBytes(ftyp, mp4Header("mdat", 1000)), want: true, wantOK: true},
		{name: "mdat after free", head: joinBytes(ftyp, mp4Box("free", 0), mp4Header("mdat", 1000)), want: true, wantOK: true},
		{name: "64-bit mdat", head: joinBytes(ftyp, mp4LargeBox("mdat", 1<<33, 0)), want: true, wantOK: true},
		{name: "64-bit mdat without its size", head: joinBytes(ftyp, mp4LargeBox("mdat", 1<<33, 0)[:8]), want: true, wantOK: true},
		{name: "mdat after a 64-bit free", head: joinBytes(ftyp, mp4LargeBox("free", 24, 8), mp4Header("mdat", 1000)), want: true, wantOK: true},
		{name: "free runs to the end of the file", head: joinBytes(ftyp, mp4Header("free", 0), mp4Header("mdat", 1000)), wantOK: true},
		{name: "head ends after ftyp", head: ftyp},
		{name: "head ends inside the mdat header", head: joinBytes(ftyp, mp4Header("mdat", 1000)[:6])},
		{name: "head ends inside a 64-bit size", head: joinBytes(ftyp, mp4LargeBox("free", 24, 8)[:12])},
		{name: "head ends inside a box before mdat", head: joinBytes(ftyp, mp4Header("free", 64), make([]byte, 16))},
		{name: "free runs past the head", head: joinBytes(ftyp, mp4LargeBox("free", math.MaxUint64, 0), mp4Header("mdat", 1000))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mp4MdatFirst(tt.head)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("mp4MdatFirst() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// readMdatFirst stops as soon as it knows the answer, so playback does not
// wait for more data than the box headers need.
func TestReadMdatFirst(t *testing.T) {
	ftyp := mp4Box("ftyp", 20)

	tests := []struct {
		name     string
		data     []byte
		want     bool
		readsAll bool // the reader reaches the end of data
	}{
		{name: "mdat first", data: joinBytes(ftyp, mp4Box("free", 0), mp4Header("mdat", 1000)), want: true},
		{name: "moov first", data: joinBytes(ftyp, mp4Box("moov", 64))},
		{name: "not mp4", data: []byte("fLaC\x00\x00\x00\x22")},
		{name: "ends before the answer", data: ftyp, readsAll: true},
		{name: "no answer within the size limit", data: joinBytes(ftyp, mp4LargeBox("free", math.MaxUint64, mp4HeadSize))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readPast := false
			past := readerFunc(func([]byte) (int, error) {
				readPast = true
				return 0, io.EOF
			})
			// One byte for each read, as data arrives from a slow server.
			r := io.MultiReader(iotest.OneByteReader(bytes.NewReader(tt.data)), past)
			if got := readMdatFirst(r); got != tt.want {
				t.Errorf("readMdatFirst() = %v, want %v", got, tt.want)
			}
			if readPast != tt.readsAll {
				t.Errorf("read past the data: %v, want %v", readPast, tt.readsAll)
			}
		})
	}
}

// encodeM4A writes 10 seconds of a sine tone as AAC in an MP4 file. ffmpeg
// puts the moov box after the audio unless faststart moves it first. The file
// is larger than the ffmpeg input buffer, so a pipe cannot hold all of it.
func encodeM4A(t *testing.T, faststart bool) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tone.m4a")
	args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-c:a", "aac", "-b:a", "64k"}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg cannot encode the test file: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if mdatFirst, _ := mp4MdatFirst(data); mdatFirst == faststart {
		t.Fatalf("encoded file has mdat first: %v, want %v", mdatFirst, !faststart)
	}
	return data
}

// bufferedM4A plays an encoded M4A file through the buffered pipeline, as an
// Audiobookshelf item-file URL plays.
func bufferedM4A(t *testing.T, faststart bool) *trackPipeline {
	t.Helper()
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is required for the buffered pipeline")
	}
	data := encodeM4A(t, faststart)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	p.RegisterBufferedURLMatcher(func(string) bool { return true })
	tp, err := p.buildPipeline(serveBody(t, string(data)), 0, nil)
	if err != nil {
		t.Fatalf("buildPipeline() error = %v", err)
	}
	t.Cleanup(tp.close)
	if !tp.seekable {
		t.Error("the pipeline cannot seek")
	}
	return tp
}

// ffmpeg puts the moov box after the audio by default, and it cannot read
// that box from a pipe. Such a file plays from the finished download.
func TestBufferedMP4MoovAfterMdatPlaysFromFile(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is required to measure the file")
	}
	const sr = beep.SampleRate(44100)
	tp := bufferedM4A(t, false)
	decoder, ok := tp.decoder.(*localFFmpegStreamer)
	if !ok {
		t.Fatalf("decoder = %T, want *localFFmpegStreamer", tp.decoder)
	}

	if peak := peakAbs(tp.stream, int(sr)/2); peak < 0.01 {
		t.Errorf("peak = %v in the first half second, want a tone", peak)
	}
	if got := sr.D(decoder.Len()); got < 9*time.Second || got > 11*time.Second {
		t.Errorf("Len() = %v, want about 10s", got)
	}
	if err := decoder.Seek(sr.N(5 * time.Second)); err != nil {
		t.Fatalf("Seek(5s) error = %v", err)
	}
	if got := decoder.Position(); got != sr.N(5*time.Second) {
		t.Errorf("Position() after seek = %d, want %d", got, sr.N(5*time.Second))
	}
	if peak := peakAbs(tp.stream, int(sr)/2); peak < 0.01 {
		t.Errorf("peak = %v after the seek, want a tone", peak)
	}

	tp.close()
	if _, err := os.Stat(decoder.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary file after close: %v, want it removed", err)
	}
}

// A file with the moov box first keeps playing from the download in progress.
func TestBufferedMP4MoovFirstStreams(t *testing.T) {
	tp := bufferedM4A(t, true)
	if _, ok := tp.decoder.(*navFFmpegStreamer); !ok {
		t.Fatalf("decoder = %T, want *navFFmpegStreamer", tp.decoder)
	}
	if peak := peakAbs(tp.stream, 22050); peak < 0.01 {
		t.Errorf("peak = %v in the first half second, want a tone", peak)
	}
}

// A newer request ends the wait for the download, and the download stops.
func TestBufferedMP4WaitEndsWhenSuperseded(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is required for the buffered pipeline")
	}
	head := bytes.Join([][]byte{mp4Box("ftyp", 20), mp4Box("free", 0), mp4Header("mdat", 1<<20)}, nil)
	canceled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2000000")
		_, _ = w.Write(head)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	t.Cleanup(srv.Close)

	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	p.RegisterBufferedURLMatcher(func(string) bool { return true })
	// A newer play request has replaced request 1.
	p.playGen.Store(2)

	start := time.Now()
	tp, err := p.buildPipeline(srv.URL, 0, staleFor(&p.playGen, 1))
	if err == nil {
		tp.close()
		t.Fatal("buildPipeline() error = nil, want the superseded error")
	}
	if !errors.Is(err, errSourceSuperseded) {
		t.Fatalf("buildPipeline() error = %v, want %v", err, errSourceSuperseded)
	}
	if elapsed := time.Since(start); elapsed > readStallTimeout {
		t.Errorf("the wait ended after %v, want it to end before the stall timeout", elapsed)
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the download went on after the wait ended")
	}
}

func TestNavBufferWaitComplete(t *testing.T) {
	tests := []struct {
		name    string
		length  string // Content-Length, empty for none
		body    string
		stall   bool  // the server holds the response open after body
		wantErr bool  // waitComplete fails
		wantIs  error // when set, the error matches it
	}{
		{name: "complete", length: "10", body: "abcdefghij"},
		{name: "complete without a length", body: "abcdefghij"},
		{name: "stalled", body: "abcd", stall: true, wantErr: true, wantIs: errNavBufferReadStalled},
		{name: "ended early", length: "20", body: "abcdefghij", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.length != "" {
					w.Header().Set("Content-Length", tt.length)
				}
				_, _ = w.Write([]byte(tt.body))
				w.(http.Flusher).Flush()
				if tt.stall {
					<-r.Context().Done()
				}
			}))
			t.Cleanup(srv.Close)

			b, _, err := newNavBuffer(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			b.stallTimeout = 50 * time.Millisecond

			path, err := b.waitComplete(nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("waitComplete() error = %v, want an error: %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("waitComplete() error = %v, want %v", err, tt.wantIs)
			}
			if tt.wantErr {
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the downloaded file: %v", err)
			}
			if string(data) != tt.body {
				t.Errorf("downloaded file = %q, want %q", data, tt.body)
			}
		})
	}
}
