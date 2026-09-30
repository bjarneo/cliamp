package spotify

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/gopxl/beep/v2"
)

type closeErrorSource struct {
	closeCalls int
	err        error
	onClose    func()
}

func (*closeErrorSource) Read([]float32) (int, error) { return 0, nil }
func (*closeErrorSource) SetPositionMs(int64) error   { return nil }
func (*closeErrorSource) PositionMs() int64           { return 0 }
func (s *closeErrorSource) Close() error {
	s.closeCalls++
	if s.onClose != nil {
		s.onClose()
	}
	return s.err
}

type closeSource struct {
	closeCalls int
}

func (*closeSource) Read([]float32) (int, error) { return 0, nil }
func (*closeSource) SetPositionMs(int64) error   { return nil }
func (*closeSource) PositionMs() int64           { return 0 }
func (s *closeSource) Close()                    { s.closeCalls++ }

type noCloseSource struct{}

func (*noCloseSource) Read([]float32) (int, error) { return 0, nil }
func (*noCloseSource) SetPositionMs(int64) error   { return nil }
func (*noCloseSource) PositionMs() int64           { return 0 }

func TestSpotifyStreamerCloseReleasesReferencesWithoutClosingDecoder(t *testing.T) {
	source := &closeErrorSource{err: errors.New("close source")}
	stream := &librespotPlayer.Stream{Source: source}
	s := newSpotifyStreamer(stream, nil)
	s.buf = make([]float32, 16)

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if source.closeCalls != 0 {
		t.Fatalf("source Close calls = %d, want 0", source.closeCalls)
	}
	if s.source != nil || s.buf != nil {
		t.Fatalf("Close() retained resources: source=%v buf=%v", s.source, s.buf)
	}
}

func TestSpotifyStreamerCloseWithoutCloseMethod(t *testing.T) {
	source := &closeSource{}
	stream := &librespotPlayer.Stream{Source: source}
	s := newSpotifyStreamer(stream, nil)
	s.buf = make([]float32, 16)

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if source.closeCalls != 0 {
		t.Fatalf("source Close calls = %d, want 0", source.closeCalls)
	}
	if s.source != nil || s.buf != nil {
		t.Fatalf("Close() retained resources: source=%v buf=%v", s.source, s.buf)
	}
}

func TestSpotifyStreamerCloseWithNonClosableSource(t *testing.T) {
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &noCloseSource{}}, nil)
	s.buf = make([]float32, 16)

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if s.source != nil || s.buf != nil {
		t.Fatalf("Close() retained resources: source=%v buf=%v", s.source, s.buf)
	}
}

func TestSpotifyStreamerCloseConcurrent(t *testing.T) {
	source := &closeErrorSource{err: errors.New("close source")}
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: source}, nil)

	const callers = 16
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Close()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	}
	if source.closeCalls != 0 {
		t.Fatalf("source Close calls = %d, want 0", source.closeCalls)
	}
}

func TestSpotifyStreamerCloseCancelsTransport(t *testing.T) {
	requestStarted := make(chan struct{})
	requestDone := make(chan error, 1)
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	streamCtx, cancel := context.WithCancel(context.Background())
	client := newSpotifyStreamHTTPClient(streamCtx, transport)

	source := &closeErrorSource{}
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: source}, cancel)

	go func() {
		req, err := http.NewRequest(http.MethodGet, "https://audio.example/chunk", nil)
		if err != nil {
			requestDone <- err
			return
		}
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("transport request did not start")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !errors.Is(streamCtx.Err(), context.Canceled) {
		t.Fatal("stream context was not canceled")
	}

	select {
	case err := <-requestDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("transport error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("transport request was not canceled")
	}
}

type blockingSource struct {
	readStarted chan struct{}
	releaseRead chan struct{}
	closeCalls  int
	position    int64
}

func (s *blockingSource) Read(p []float32) (int, error) {
	close(s.readStarted)
	<-s.releaseRead
	for i := range p {
		p[i] = float32(i)
	}
	s.position++
	return len(p), nil
}

func (s *blockingSource) SetPositionMs(position int64) error {
	s.position = position
	return nil
}

func (s *blockingSource) PositionMs() int64 { return s.position }

func (s *blockingSource) Close() error {
	s.closeCalls++
	return nil
}

func TestSpotifyStreamerCloseConcurrentOperations(t *testing.T) {
	source := &blockingSource{
		readStarted: make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	canceled := make(chan struct{})
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: source}, func() { close(canceled) })

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		s.Stream(make([][2]float64, 8))
	}()
	<-source.readStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	<-canceled

	positionDone := make(chan int, 1)
	seekDone := make(chan error, 1)
	go func() { positionDone <- s.Position() }()
	go func() { seekDone <- s.Seek(spotifySampleRate) }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before decoder read completed: %v", err)
	default:
	}
	close(source.releaseRead)
	<-streamDone
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if position := <-positionDone; position != 0 {
		t.Errorf("Position() after close request = %d, want 0", position)
	}
	if err := <-seekDone; !errors.Is(err, net.ErrClosed) {
		t.Errorf("Seek() after close request error = %v, want net.ErrClosed", err)
	}

	if n, ok := s.Stream(make([][2]float64, 1)); n != 0 || ok {
		t.Errorf("Stream() after Close() = (%d, %v), want (0, false)", n, ok)
	}
	if err := s.Err(); err != nil {
		t.Errorf("Err() after Close() = %v, want nil", err)
	}
	if position := s.Position(); position != 0 {
		t.Errorf("Position() after Close() = %d, want 0", position)
	}
	if err := s.Seek(0); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Seek() after Close() error = %v, want net.ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
	if source.closeCalls != 0 {
		t.Errorf("source Close calls = %d, want 0", source.closeCalls)
	}
}

type racingSource struct {
	value  int64
	closed bool
}

func (s *racingSource) Read(p []float32) (int, error) {
	if s.closed {
		return 0, net.ErrClosed
	}
	s.value++
	runtime.Gosched()
	return len(p), nil
}

func (s *racingSource) SetPositionMs(position int64) error {
	if s.closed {
		return net.ErrClosed
	}
	s.value = position
	runtime.Gosched()
	return nil
}

func (s *racingSource) PositionMs() int64 {
	runtime.Gosched()
	return s.value
}

func (s *racingSource) Close() error {
	s.closed = true
	return nil
}

func TestSpotifyStreamerRaceCloseAndOperations(t *testing.T) {
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &racingSource{}}, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				s.Stream(make([][2]float64, 4))
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				s.Position()
				_ = s.Seek(spotifySampleRate)
				_ = s.Err()
			}
		}()
	}

	close(start)
	runtime.Gosched()
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	wg.Wait()
}

type fullSource struct{}

func (*fullSource) Read(p []float32) (int, error) {
	for i := range p {
		p[i] = float32(i)
	}
	return len(p), nil
}

func (*fullSource) SetPositionMs(int64) error { return nil }
func (*fullSource) PositionMs() int64         { return 0 }

func TestSpotifyStreamerStreamAllocations(t *testing.T) {
	const sampleCount = 256
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &fullSource{}}, nil)
	s.buf = make([]float32, sampleCount*spotifyChannels)
	samples := make([][2]float64, sampleCount)

	if allocs := testing.AllocsPerRun(100, func() {
		if n, ok := s.Stream(samples); n != sampleCount || !ok {
			panic("unexpected stream result")
		}
	}); allocs != 0 {
		t.Errorf("Stream() allocations = %v, want 0", allocs)
	}
}

// pairSource is a finite librespot.AudioSource emitting `pairs` stereo pairs,
// at most `chunk` interleaved float32 values per Read, then io.EOF. Pair i
// carries (i+1, -(i+1)) so the streamed sequence is self-verifying: a dropped
// or duplicated pair — or one emitted out of order — breaks the 1..N
// progression on both channels.
//
// chunk must stay even: go-librespot sources emit interleaved stereo, and
// spotifyStreamer drops a trailing odd float32 by design.
type pairSource struct {
	pairs int
	chunk int
	pos   int // interleaved float32 index already emitted
}

func (s *pairSource) Read(p []float32) (int, error) {
	total := s.pairs * spotifyChannels
	if s.pos >= total {
		return 0, io.EOF
	}
	n := min(len(p), s.chunk, total-s.pos)
	for i := range n {
		v := float32((s.pos+i)/2 + 1)
		if (s.pos+i)%2 == 1 {
			v = -v
		}
		p[i] = v
	}
	s.pos += n
	return n, nil
}

func (*pairSource) SetPositionMs(int64) error { return nil }
func (*pairSource) PositionMs() int64         { return 0 }

// TestSpotifyStreamerGaplessSeqTransition locks the contract the player's
// gapless handoff relies on: when two Spotify streamers are sequenced with
// beep.Seq (the same fill-remaining semantics gaplessStreamer uses), the
// boundary must emit every pair of the outgoing track followed by every pair
// of the incoming one — no dropped or duplicated sample pairs at the seam,
// whatever the chunk/buffer alignment.
func TestSpotifyStreamerGaplessSeqTransition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		aPairs  int
		bPairs  int
		aChunk  int // float32 values returned per Read by streamer A
		bChunk  int // float32 values returned per Read by streamer B
		bufSize int // stereo pairs requested per Stream call
	}{
		{"seam mid-buffer", 1000, 777, 512, 512, 512},
		{"seam at buffer edge", 1024, 300, 1024, 512, 512},
		{"tiny chunked reads", 33, 21, 2, 4, 7},
		{"unaligned chunks", 300, 222, 250, 62, 129},
		{"single oversized buffer", 500, 700, 512, 512, 4096},
		{"empty outgoing", 0, 128, 512, 512, 256},
		{"empty incoming", 128, 0, 512, 512, 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newSpotifyStreamer(&librespotPlayer.Stream{
				Source: &pairSource{pairs: tc.aPairs, chunk: tc.aChunk},
			}, nil)
			b := newSpotifyStreamer(&librespotPlayer.Stream{
				Source: &pairSource{pairs: tc.bPairs, chunk: tc.bChunk},
			}, nil)

			var got [][2]float64
			buf := make([][2]float64, tc.bufSize)
			seq := beep.Seq(a, b)
			for {
				n, ok := seq.Stream(buf)
				got = append(got, buf[:n]...)
				if !ok {
					break
				}
			}

			if want := tc.aPairs + tc.bPairs; len(got) != want {
				t.Fatalf("Seq emitted %d pairs, want %d", len(got), want)
			}
			for i, fr := range got {
				track, pair := 'A', i+1
				if i >= tc.aPairs {
					track, pair = 'B', i-tc.aPairs+1
				}
				want := [2]float64{float64(pair), -float64(pair)}
				if fr != want {
					t.Fatalf("output[%d] (track %c) = %v, want %v — dropped or duplicated pair at the seam",
						i, track, fr, want)
				}
			}
		})
	}
}
