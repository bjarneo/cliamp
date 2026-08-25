package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// readStallTimeout is how long Read/Seek wait for new data before returning
// an error. The deadline resets whenever the download goroutine delivers new
// bytes, so slow-but-progressing downloads are not affected. Only a true
// stall (no new data for this duration) triggers the timeout.
const readStallTimeout = 5 * time.Second

var (
	errNavBufferClosed      = errors.New("nav buffer: closed")
	errNavBufferReadStalled = errors.New("nav buffer: read stalled waiting for data")
)

const navBufferChunkSize = 32 * 1024

// navBuffer is an io.ReadSeekCloser backed by a background HTTP download.
// Bytes are written to a temporary file as they arrive. Read and Seek block
// if the requested position has not yet been downloaded.
//
// Lock ordering: navBuffer.mu is a leaf lock. It must never be acquired
// while holding speaker.Lock() or player.mu.
type navBuffer struct {
	mu           sync.Mutex
	readMu       sync.Mutex // serializes cursor operations without blocking downloads
	wait         chan struct{}
	downloadDone chan struct{}
	file         *os.File
	path         string
	downloaded   int64 // bytes available in file
	total        int64 // Content-Length from HTTP response (-1 if unknown)
	pos          int64 // current read cursor, guarded by readMu
	done         bool  // true when download goroutine has finished
	err          error // first error from download goroutine
	closed       bool
	cancel       context.CancelFunc
	contentType  string       // HTTP Content-Type header value
	bytesIn      atomic.Int64 // mirrors downloaded; safe for unsynchronised UI reads
	stallTimeout time.Duration
	closeOnce    sync.Once
	closeErr     error
}

// newNavBuffer opens an HTTP GET request for rawURL, starts downloading in a
// background goroutine, and returns immediately. The caller may begin reading
// from the buffer before the download completes.
//
// Returns (buffer, contentLength, error). contentLength is -1 when the server
// does not send a Content-Length header (e.g. chunked transfer encoding).
func newNavBuffer(rawURL string) (*navBuffer, int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := navBufferGet(ctx, rawURL)
	if err != nil {
		cancel()
		return nil, 0, err
	}

	b, err := newNavBufferFor(resp, cancel)
	if err != nil {
		return nil, 0, err
	}

	go b.download(resp.Body)

	return b, resp.ContentLength, nil
}

// newNavBufferSegments is like newNavBuffer for an ordered list of segment
// URLs whose concatenated bytes form one progressive stream (e.g. an fMP4
// init segment followed by unencrypted media segments). The total length is
// unknown up front, so the returned contentLength is always -1.
func newNavBufferSegments(urls []string) (*navBuffer, int64, error) {
	if len(urls) == 0 {
		return nil, 0, fmt.Errorf("nav buffer: no segment urls")
	}
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := navBufferGet(ctx, urls[0])
	if err != nil {
		cancel()
		return nil, 0, err
	}

	b, err := newNavBufferFor(resp, cancel)
	if err != nil {
		return nil, 0, err
	}
	b.total = -1

	go b.downloadSegments(ctx, resp.Body, urls[1:])

	return b, -1, nil
}

// navBufferGet performs the HTTP GET for a navBuffer download source.
func navBufferGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("nav buffer request: %w", err)
	}
	req.Header.Set("User-Agent", "cliamp/1.0 (https://github.com/bjarneo/cliamp)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nav buffer connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("nav buffer: http status %s", resp.Status)
	}
	return resp, nil
}

// newNavBufferFor builds the file-backed buffer for an opened response.
// On error the response body is closed and cancel invoked.
func newNavBufferFor(resp *http.Response, cancel context.CancelFunc) (*navBuffer, error) {
	file, err := os.CreateTemp("", "cliamp-nav-*")
	if err != nil {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("nav buffer tempfile: %w", err)
	}

	return &navBuffer{
		total:        resp.ContentLength, // -1 if unknown
		cancel:       cancel,
		contentType:  resp.Header.Get("Content-Type"),
		wait:         make(chan struct{}),
		downloadDone: make(chan struct{}),
		file:         file,
		path:         file.Name(),
		stallTimeout: readStallTimeout,
	}, nil
}

// download reads the HTTP response body in bounded chunks and appends them to
// the temporary file. It signals blocked Read/Seek calls after each write.
func (b *navBuffer) download(body io.ReadCloser) {
	defer close(b.downloadDone)
	if _, ok := b.copyBody(body, 0); ok {
		b.finish(nil)
	}
}

// downloadSegments appends the first segment's body, then fetches and appends
// each remaining segment URL in order, so the file holds the segments'
// concatenated bytes. Records the first error and stops on close.
func (b *navBuffer) downloadSegments(ctx context.Context, first io.ReadCloser, rest []string) {
	defer close(b.downloadDone)
	offset, ok := b.copyBody(first, 0)
	if !ok {
		return
	}
	for _, u := range rest {
		resp, err := navBufferGet(ctx, u)
		if err != nil {
			b.finish(err)
			return
		}
		if offset, ok = b.copyBody(resp.Body, offset); !ok {
			return
		}
	}
	b.finish(nil)
}

// copyBody appends one response body to the temporary file starting at
// offset. Returns the new offset and whether the caller should continue;
// on failure the error has already been recorded via finish.
func (b *navBuffer) copyBody(body io.ReadCloser, offset int64) (int64, bool) {
	defer body.Close()
	chunk := make([]byte, navBufferChunkSize)
	for {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return offset, false
		}

		n, err := body.Read(chunk)
		if n > 0 {
			written := 0
			for written < n {
				b.mu.Lock()
				file := b.file
				closed := b.closed
				b.mu.Unlock()
				if closed || file == nil {
					return offset, false
				}

				wn, writeErr := file.WriteAt(chunk[written:n], offset)
				if wn > 0 {
					written += wn
					offset += int64(wn)
					b.publish(offset)
				}
				if writeErr != nil {
					b.finish(fmt.Errorf("nav buffer tempfile write: %w", writeErr))
					return offset, false
				}
				if wn == 0 {
					b.finish(fmt.Errorf("nav buffer tempfile write: %w", io.ErrShortWrite))
					return offset, false
				}
			}
		}
		if err == io.EOF {
			return offset, true
		}
		if err != nil {
			b.finish(err)
			return offset, false
		}
	}
}

func (b *navBuffer) publish(downloaded int64) {
	b.mu.Lock()
	if !b.closed && downloaded > b.downloaded {
		b.downloaded = downloaded
		b.bytesIn.Store(downloaded)
		b.signalLocked()
	}
	b.mu.Unlock()
}

func (b *navBuffer) finish(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if err != nil && b.err == nil {
		b.err = err
	}
	b.done = true
	b.signalLocked()
}

func (b *navBuffer) signalLocked() {
	close(b.wait)
	b.wait = make(chan struct{})
}

// waitFor blocks until target bytes are available or, when complete is true,
// until the download finishes. The stall timer resets only on byte progress.
func (b *navBuffer) waitFor(target int64, complete bool, stallErr error, cancel <-chan struct{}) error {
	stallTimeout := b.stallTimeout
	if stallTimeout <= 0 {
		stallTimeout = readStallTimeout
	}
	timer := time.NewTimer(stallTimeout)
	defer stopTimer(timer)

	b.mu.Lock()
	lastDownloaded := b.downloaded
	b.mu.Unlock()
	expired := false

	for {
		if isCanceled(cancel) {
			return errNavBufferClosed
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return errNavBufferClosed
		}
		if !complete && b.downloaded >= target {
			b.mu.Unlock()
			return nil
		}
		if b.err != nil {
			err := b.err
			b.mu.Unlock()
			return err
		}
		if b.done {
			b.mu.Unlock()
			return nil
		}

		downloaded := b.downloaded
		wait := b.wait
		b.mu.Unlock()

		if downloaded > lastDownloaded {
			lastDownloaded = downloaded
			resetTimer(timer, stallTimeout)
			expired = false
		}
		if expired {
			return stallErr
		}

		select {
		case <-wait:
		case <-timer.C:
			expired = true
		case <-cancel:
			return errNavBufferClosed
		}
	}
}

func isCanceled(cancel <-chan struct{}) bool {
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// Read implements io.Reader.
// Blocks until at least one byte at pos is available or the download ends.
// Returns an error if the download stalls (no new data for readStallTimeout).
func (b *navBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	b.readMu.Lock()
	defer b.readMu.Unlock()

	if err := b.waitFor(b.pos+1, false, errNavBufferReadStalled, nil); err != nil {
		return 0, err
	}
	n, err := b.readAt(p, b.pos, nil)
	b.pos += int64(n)
	return n, err
}

// Seek implements io.Seeker.
// If the target position is beyond what has been downloaded, blocks until
// the download reaches it. Returns an error if the download stalls.
func (b *navBuffer) Seek(offset int64, whence int) (int64, error) {
	b.readMu.Lock()
	defer b.readMu.Unlock()

	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = b.pos + offset
	case io.SeekEnd:
		// If Content-Length is known, use it immediately — no need to wait for
		// the full download. This is the common case with format=raw, and avoids
		// blocking the FLAC decoder which seeks to SeekEnd during header parsing
		// just to determine the file size.
		if b.total >= 0 {
			target = b.total + offset
		} else {
			// Content-Length unknown (chunked): must wait for the full download.
			if err := b.waitFor(0, true, fmt.Errorf("nav buffer: seek stalled waiting for download"), nil); err != nil {
				return 0, err
			}
			b.mu.Lock()
			target = b.downloaded + offset
			b.mu.Unlock()
		}
	default:
		return 0, fmt.Errorf("nav buffer: invalid whence %d", whence)
	}

	if target < 0 {
		target = 0
	}

	// Block until the buffer reaches the target position.
	if err := b.waitFor(target, false, fmt.Errorf("nav buffer: seek stalled waiting for data at offset %d", target), nil); err != nil {
		return 0, err
	}

	b.mu.Lock()
	downloaded := b.downloaded
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return 0, errNavBufferClosed
	}
	if target > downloaded {
		target = downloaded
	}
	b.pos = target
	return b.pos, nil
}

// navReader is a per-FFmpeg cursor over a navBuffer. Closing it interrupts a
// blocked read without closing the shared progressive download.
type navReader struct {
	buffer    *navBuffer
	pos       int64
	cancel    chan struct{}
	closeOnce sync.Once
}

func (b *navBuffer) newReader() *navReader {
	return &navReader{buffer: b, cancel: make(chan struct{})}
}

// readAt reads currently available data. The caller holds readMu so Close
// cannot close the temporary file during the operation.
func (b *navBuffer) readAt(p []byte, pos int64, cancel <-chan struct{}) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errNavBufferClosed
	}
	available := b.downloaded - pos
	file := b.file
	b.mu.Unlock()
	if available <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > available {
		p = p[:available]
	}

	n, err := file.ReadAt(p, pos)
	if err == nil {
		return n, nil
	}
	if isCanceled(cancel) {
		return n, errNavBufferClosed
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return n, errNavBufferClosed
	}
	return n, fmt.Errorf("nav buffer tempfile read: %w", err)
}

func (r *navReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > navBufferChunkSize {
		p = p[:navBufferChunkSize]
	}

	b := r.buffer
	if err := b.waitFor(r.pos+1, false, errNavBufferReadStalled, r.cancel); err != nil {
		return 0, err
	}

	// Per-process readers have independent cursors. Serialize only the file
	// operation against Close, not the wait for progressive data, so a seek
	// replacement can read while the old FFmpeg input is stalled.
	b.readMu.Lock()
	defer b.readMu.Unlock()
	n, err := b.readAt(p, r.pos, r.cancel)
	r.pos += int64(n)
	return n, err
}

func (r *navReader) Close() error {
	r.closeOnce.Do(func() { close(r.cancel) })
	return nil
}

// Close cancels the download, unblocks waiters, and removes the temporary file.
func (b *navBuffer) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.done = true
		if b.err == nil {
			b.err = errNavBufferClosed
		}
		cancel := b.cancel
		b.cancel = nil
		b.signalLocked()
		b.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		<-b.downloadDone

		// No file operation may overlap Close or Remove. This ordering is
		// required on Windows, where an open file cannot be removed.
		b.readMu.Lock()
		b.mu.Lock()
		file := b.file
		b.file = nil
		path := b.path
		b.path = ""
		b.mu.Unlock()
		var closeErr, removeErr error
		if file != nil {
			closeErr = file.Close()
		}
		if path != "" {
			removeErr = os.Remove(path)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
		}
		b.closeErr = errors.Join(closeErr, removeErr)
		b.readMu.Unlock()
	})
	return b.closeErr
}

// ContentType returns the HTTP Content-Type from the server response.
func (b *navBuffer) ContentType() string {
	return b.contentType
}
