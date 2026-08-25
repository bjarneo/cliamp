package spotify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
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
