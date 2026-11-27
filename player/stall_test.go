package player

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var errStalledConn = errors.New("connection closed")

// blockingReader models a live HTTP body. When fast is set, Read returns it
// immediately; otherwise Read blocks until unblock is closed (simulating the
// request context cancel tearing down the connection).
type blockingReader struct {
	fast    []byte
	unblock chan struct{}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.fast != nil {
		return copy(p, b.fast), nil
	}
	<-b.unblock
	return 0, errStalledConn
}

func (b *blockingReader) Close() error { return nil }

func TestStallReaderTimesOutOnStall(t *testing.T) {
	br := &blockingReader{unblock: make(chan struct{})}

	// cancel unblocks the stalled read, mimicking net/http closing the
	// connection when the request context is cancelled.
	var closed atomic.Bool
	sr := &stallReader{
		rc: br,
		cancel: func() {
			if closed.CompareAndSwap(false, true) {
				close(br.unblock)
			}
		},
		timeout: 20 * time.Millisecond,
	}

	start := time.Now()
	n, err := sr.Read(make([]byte, 16))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error from stalled read, got n=%d err=nil", n)
	}
	if elapsed > time.Second {
		t.Fatalf("stall reader did not time out promptly: %v", elapsed)
	}
}

func TestStallReaderPassesThroughFastRead(t *testing.T) {
	br := &blockingReader{fast: []byte("hello")}
	var cancelled atomic.Bool
	sr := &stallReader{
		rc:      br,
		cancel:  func() { cancelled.Store(true) },
		timeout: 50 * time.Millisecond,
	}

	buf := make([]byte, 16)
	n, err := sr.Read(buf)
	if err != nil || n != len("hello") {
		t.Fatalf("fast read: got n=%d err=%v, want n=5 err=nil", n, err)
	}

	// Give any stray (but stopped) timer a chance to misfire.
	time.Sleep(80 * time.Millisecond)
	if cancelled.Load() {
		t.Fatal("cancel fired on a healthy fast read")
	}
}

func TestStallReaderCloseCancels(t *testing.T) {
	var cancels atomic.Int32
	sr := &stallReader{
		rc:      &blockingReader{fast: []byte("x")},
		cancel:  func() { cancels.Add(1) },
		timeout: time.Hour,
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cancels.Load() == 0 {
		t.Fatal("Close did not cancel the request")
	}
}

func TestStallReaderReusesTimer(t *testing.T) {
	br := &blockingReader{fast: []byte("hello")}
	var cancelled atomic.Bool
	sr := &stallReader{
		rc:      br,
		cancel:  func() { cancelled.Store(true) },
		timeout: time.Second,
	}
	defer sr.Close()

	buf := make([]byte, 16)
	if n, err := sr.Read(buf); err != nil || n != 5 {
		t.Fatalf("warmup Read() = (%d, %v), want (5, nil)", n, err)
	}
	firstTimer := sr.timer
	if firstTimer == nil {
		t.Fatal("expected stallReader to allocate one reusable timer")
	}

	allocs := testing.AllocsPerRun(100, func() {
		if n, err := sr.Read(buf); err != nil || n != 5 {
			t.Fatalf("Read() = (%d, %v), want (5, nil)", n, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("steady-state stallReader.Read allocations = %v, want 0", allocs)
	}
	if sr.timer != firstTimer {
		t.Fatal("stallReader replaced its reusable timer")
	}
	if cancelled.Load() {
		t.Fatal("cancel fired during healthy fast reads")
	}
}
