package kittyimg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// pngDims builds the smallest PNG that image.DecodeConfig accepts for the
// given pixel size: signature, IHDR and IEND. No pixel data needs decoding
// for the cap check, so a huge declared size costs nothing to build.
func pngDims(width, height uint32) []byte {
	out := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	ihdr := make([]byte, 0, 21)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 13)
	ihdr = append(ihdr, 'I', 'H', 'D', 'R')
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8], data[9], data[10], data[11], data[12] = 8, 6, 0, 0, 0 // 8-bit RGBA
	ihdr = append(ihdr, data...)
	ihdr = binary.BigEndian.AppendUint32(ihdr, crc32.ChecksumIEEE(append([]byte("IHDR"), data...)))
	out = append(out, ihdr...)
	return append(out, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82)
}

func TestDecodeImageRejectsHugePixels(t *testing.T) {
	raw := pngDims(512, 1<<20) // 536,870,912 pixels, far over the cap
	_, err := decodeImage(raw)
	if err == nil {
		t.Fatal("a 512x1048576 image must be rejected before decoding")
	}
	if !strings.Contains(err.Error(), "pixel cap") {
		t.Fatalf("err = %v, want the pixel-cap message", err)
	}
}

func TestDecodeImageAcceptsNormalPixels(t *testing.T) {
	raw := pngDims(64, 16)
	_, err := decodeImage(raw)
	// Without pixel data the full decode fails, but the error must not be
	// the pixel cap: the size check passed.
	if err != nil && strings.Contains(err.Error(), "pixel cap") {
		t.Fatalf("a 64x16 image must pass the cap check: %v", err)
	}
}

type syncBufCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBufCloser) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBufCloser) Close() error { return nil }

func (b *syncBufCloser) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func resetKittyTestState(t *testing.T, sink io.WriteCloser, openErr error) {
	t.Helper()
	oldOpen := openTTY
	oldMaxPlaced := maxPlaced
	oldDisabled := ttyDisabled.Load()

	writeMu.Lock()
	ttyOpened = false
	ttyFile = nil
	ttyErr = nil
	openTTY = func() (io.WriteCloser, error) {
		return sink, openErr
	}
	writeMu.Unlock()
	ttyDisabled.Store(false)

	mu.Lock()
	placed = map[int]Placement{}
	placedOrder = nil
	mu.Unlock()

	t.Cleanup(func() {
		writeMu.Lock()
		openTTY = oldOpen
		ttyOpened = false
		ttyFile = nil
		ttyErr = nil
		writeMu.Unlock()
		ttyDisabled.Store(oldDisabled)

		mu.Lock()
		maxPlaced = oldMaxPlaced
		placed = map[int]Placement{}
		placedOrder = nil
		mu.Unlock()
	})
}

func smallPNGDataURL(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestLoadRejectsOversizedBase64BeforeDecode(t *testing.T) {
	// Build a base64 payload whose DecodedLen exceeds maxImageBytes without
	// valid base64 content so it fails on the pre-decode cap check.
	oversizedLen := ((maxImageBytes + 3) / 3) * 4
	source := "data:image/png;base64," + strings.Repeat("!", oversizedLen)
	_, err := load(source)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("load(oversized base64) err = %v, want size cap error before decode", err)
	}
}

func TestClearValidatesIDAndSerializesWithTransmit(t *testing.T) {
	sink := &syncBufCloser{}
	resetKittyTestState(t, sink, nil)

	for _, badID := range []int{0, -1, -999, 0x1000000} {
		Clear(badID)
	}
	if sink.String() != "" {
		t.Fatalf("Clear wrote for invalid ids: %q", sink.String())
	}

	// Run Clear and transmitContext concurrently under -race to verify writeMu serialization.
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		id := i
		wg.Go(func() {
			_ = transmitContext(context.Background(), id, img, 2, 2)
		})
		wg.Go(func() {
			Clear(id)
		})
	}
	wg.Wait()
	if !strings.Contains(sink.String(), "\x1b_Ga=d,d=I,i=1,q=2\x1b\\") {
		t.Errorf("Clear escape sequence missing from TTY output")
	}
}

func TestPlacedMapEvictsOldestEntries(t *testing.T) {
	sink := &syncBufCloser{}
	resetKittyTestState(t, sink, nil)
	SetTTYEnabled(false)

	mu.Lock()
	maxPlaced = 3
	mu.Unlock()

	src := smallPNGDataURL(t)
	var ids []int
	for range 5 {
		p, err := Show(src, 2, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}

	if _, ok := PlacementByID(ids[0]); ok {
		t.Errorf("oldest id %d was not evicted", ids[0])
	}
	if _, ok := PlacementByID(ids[1]); ok {
		t.Errorf("second oldest id %d was not evicted", ids[1])
	}
	for _, id := range ids[2:] {
		if _, ok := PlacementByID(id); !ok {
			t.Errorf("recent id %d missing from placed", id)
		}
	}
}

func TestShowContextCancellationAndTTYDisabled(t *testing.T) {
	sink := &syncBufCloser{}
	resetKittyTestState(t, sink, nil)

	src := smallPNGDataURL(t)

	// Cancelled context is rejected upfront without writing to TTY.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ShowContext(cancelledCtx, src, 2, 2, 0); err == nil {
		t.Fatal("ShowContext with cancelled context succeeded, want error")
	}
	if sink.String() != "" {
		t.Fatalf("cancelled ShowContext wrote to TTY: %q", sink.String())
	}

	// Headless / disabled TTY produces no TTY writes for ShowContext or Clear.
	SetTTYEnabled(false)
	p, err := ShowContext(context.Background(), src, 2, 2, 0)
	if err != nil {
		t.Fatalf("ShowContext when TTY disabled: %v", err)
	}
	Clear(p.ID)
	time.Sleep(20 * time.Millisecond)
	if sink.String() != "" {
		t.Fatalf("TTY writes occurred while disabled: %q", sink.String())
	}
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	writes  int
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	return len(p), nil
}

func (w *blockingWriter) Close() error { return nil }

func TestShowContextBoundsPendingWorkers(t *testing.T) {
	bw := &blockingWriter{
		started: make(chan struct{}, maxShowPending),
		release: make(chan struct{}),
	}
	resetKittyTestState(t, bw, nil)

	src := smallPNGDataURL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		close(bw.release)
		// Drain pending slots before finishing test so global channels are clean.
		deadline := time.Now().Add(2 * time.Second)
		for len(showPending) > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}()

	var accepted, rejected int
	for range maxShowPending + 5 {
		if _, err := ShowContext(ctx, src, 2, 2, 0); err != nil {
			rejected++
		} else {
			accepted++
		}
	}
	if accepted != maxShowPending {
		t.Errorf("accepted = %d, want %d", accepted, maxShowPending)
	}
	if rejected != 5 {
		t.Errorf("rejected = %d, want 5", rejected)
	}
}
