package player

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// navBuffer is an io.ReadSeekCloser backed by a background HTTP download.
// Bytes are appended to data as they arrive. Read and Seek block via cond
// if the requested position has not yet been downloaded.
//
// Lock ordering: navBuffer.mu is a leaf lock. It must never be acquired
// while holding speaker.Lock() or player.mu.
type navBuffer struct {
	mu          sync.Mutex
	cond        *sync.Cond
	data        []byte // all bytes downloaded so far
	total       int64  // Content-Length from HTTP response (-1 if unknown)
	pos         int64  // current read cursor
	done        bool   // true when download goroutine has finished
	err         error  // first error from download goroutine
	cancel      context.CancelFunc
	contentType string       // HTTP Content-Type header value
	bytesIn     atomic.Int64 // mirrors len(data); safe for unsynchronised UI reads
}

// newNavBuffer opens an HTTP GET request for rawURL, starts downloading in a
// background goroutine, and returns immediately. The caller may begin reading
// from the buffer before the download completes.
//
// Returns (buffer, contentLength, error). contentLength is -1 when the server
// does not send a Content-Length header (e.g. chunked transfer encoding).
func newNavBuffer(rawURL string) (*navBuffer, int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		cancel()
		return nil, 0, fmt.Errorf("nav buffer request: %w", err)
	}
	req.Header.Set("User-Agent", "cliamp/1.0 (https://github.com/bjarneo/cliamp)")

	resp, err := httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, 0, fmt.Errorf("nav buffer connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, 0, fmt.Errorf("nav buffer: http status %s", resp.Status)
	}

	b := &navBuffer{
		total:       resp.ContentLength, // -1 if unknown
		cancel:      cancel,
		contentType: resp.Header.Get("Content-Type"),
	}
	b.cond = sync.NewCond(&b.mu)

	go b.download(resp.Body)

	return b, resp.ContentLength, nil
}

// download reads the HTTP response body in chunks and appends to data.
// Broadcasts on cond after each chunk so blocked Read/Seek calls wake up.
func (b *navBuffer) download(body io.ReadCloser) {
	defer body.Close()
	chunk := make([]byte, 32*1024)
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			b.mu.Lock()
			b.data = append(b.data, chunk[:n]...)
			b.mu.Unlock()
			b.bytesIn.Add(int64(n))
			b.cond.Broadcast()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			b.mu.Lock()
			b.err = err
			b.mu.Unlock()
			b.cond.Broadcast()
			return
		}
	}
	b.mu.Lock()
	b.done = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

// Read implements io.Reader.
// Blocks until at least one byte at pos is available or the download ends.
func (b *navBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for int64(len(b.data)) <= b.pos && !b.done {
		if b.err != nil {
			return 0, b.err
		}
		b.cond.Wait()
	}
	if b.err != nil && int64(len(b.data)) <= b.pos {
		return 0, b.err
	}
	if int64(len(b.data)) <= b.pos {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += int64(n)
	return n, nil
}

// Seek implements io.Seeker.
// If the target position is beyond what has been downloaded, blocks until
// the download reaches it. Returns io.EOF if the target exceeds total size.
func (b *navBuffer) Seek(offset int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

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
			for !b.done {
				if b.err != nil {
					return 0, b.err
				}
				b.cond.Wait()
			}
			target = int64(len(b.data)) + offset
		}
	default:
		return 0, fmt.Errorf("nav buffer: invalid whence %d", whence)
	}

	if target < 0 {
		target = 0
	}

	// Block until the buffer reaches the target position.
	for int64(len(b.data)) < target && !b.done {
		if b.err != nil {
			return 0, b.err
		}
		b.cond.Wait()
	}

	if target > int64(len(b.data)) {
		target = int64(len(b.data))
	}
	b.pos = target
	return b.pos, nil
}

// Close cancels the download goroutine and unblocks all waiters.
func (b *navBuffer) Close() error {
	b.cancel()
	b.mu.Lock()
	if !b.done {
		b.done = true
		b.err = fmt.Errorf("nav buffer: closed")
	}
	b.mu.Unlock()
	b.cond.Broadcast()
	return nil
}

// ContentType returns the HTTP Content-Type from the server response.
func (b *navBuffer) ContentType() string {
	return b.contentType
}
