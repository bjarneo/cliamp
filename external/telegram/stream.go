package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// streamSource is a file that is still being written. Read blocks at the
// current end of file until the downloader appends more data or finishes, so
// the audio decoder can start before the whole document is on disk.
type streamSource struct {
	mu   sync.Mutex
	cond *sync.Cond
	file *os.File
	size int64
	pos  int64
	done bool
	err  error
}

func newStreamSource(file *os.File) *streamSource {
	source := &streamSource{file: file}
	source.cond = sync.NewCond(&source.mu)
	return source
}

// Read implements io.Reader. It waits while the read position is at the end of
// the data written so far and the download has not finished.
func (s *streamSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	for s.pos >= s.size && !s.done && s.err == nil {
		s.cond.Wait()
	}
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return 0, err
	}
	if s.pos >= s.size {
		s.mu.Unlock()
		return 0, io.EOF
	}
	if available := s.size - s.pos; int64(len(p)) > available {
		p = p[:available]
	}
	n, err := s.file.ReadAt(p, s.pos)
	s.pos += int64(n)
	s.mu.Unlock()
	return n, err
}

// Seek implements io.Seeker within the data downloaded so far.
func (s *streamSource) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = s.pos + offset
	case io.SeekEnd:
		next = s.size + offset
	default:
		return 0, errors.New("telegram: invalid seek origin")
	}
	if next < 0 {
		return 0, errors.New("telegram: negative seek position")
	}
	s.pos = next
	return next, nil
}

// Close implements io.Closer.
func (s *streamSource) Close() error { return s.file.Close() }

// streamWriter writes the download at an explicit offset and wakes the reader.
// It owns a dedicated write descriptor so closing the playback stream (when the
// listener skips a track) does not abort the download in flight.
type streamWriter struct {
	source *streamSource
	file   *os.File
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.source.mu.Lock()
	defer w.source.mu.Unlock()
	n, err := w.file.WriteAt(p, w.source.size)
	w.source.size += int64(n)
	w.source.cond.Broadcast()
	return n, err
}

func (w streamWriter) finish(err error) {
	w.source.mu.Lock()
	w.source.done = true
	w.source.err = err
	w.source.cond.Broadcast()
	w.source.mu.Unlock()
}

// openStream returns a decoder that starts as soon as cfg.BufferMB of the
// document are on disk. The rest keeps downloading in the background.
func (p *Provider) openStream(chat string, msgID int) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	doc, err := p.fetchDocument(ctx, msgID)
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	extension := extensionFor(doc)
	dir := filepath.Join(p.cacheDir, safeChat(chat))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, beep.Format{}, 0, err
	}
	finalPath := filepath.Join(dir, fmt.Sprintf("%d%s", msgID, extension))

	// Cache hit: a complete file can be decoded at once.
	if info, statErr := os.Stat(finalPath); statErr == nil && info.Size() == doc.Size {
		if p.coverPath(chat, msgID) == "" {
			p.extractCoverFromFile(chat, msgID, finalPath)
		}
		file, openErr := os.Open(finalPath)
		if openErr != nil {
			return nil, beep.Format{}, 0, openErr
		}
		streamer, format, decodeErr := decode(file, extension)
		if decodeErr != nil {
			file.Close()
			return nil, beep.Format{}, 0, decodeErr
		}
		return streamer, format, time.Duration(docDuration(doc)) * time.Second, nil
	}

	partialPath := finalPath + ".part"
	writerFile, err := os.OpenFile(partialPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	readerFile, err := os.Open(partialPath)
	if err != nil {
		writerFile.Close()
		return nil, beep.Format{}, 0, err
	}
	source := newStreamSource(readerFile)
	go p.downloadStream(chat, msgID, doc, source, writerFile, partialPath, finalPath)

	buffer := int64(p.cfg.BufferMB) * 1024 * 1024
	if p.cfg.BufferMB <= 0 {
		buffer = 5 * 1024 * 1024
	}
	if err := source.waitFor(buffer, 3*time.Minute); err != nil {
		readerFile.Close()
		return nil, beep.Format{}, 0, err
	}
	// The metadata blocks are already on disk: pull the cover now, while the
	// audio keeps downloading. No extra request to Telegram.
	p.extractCoverFromFile(chat, msgID, partialPath)

	streamer, format, err := decode(source, extension)
	if err != nil {
		readerFile.Close()
		return nil, beep.Format{}, 0, err
	}
	return streamer, format, time.Duration(docDuration(doc)) * time.Second, nil
}

// waitFor blocks until size bytes are available or the download finishes.
func (s *streamSource) waitFor(size int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		written, done, err := s.size, s.done, s.err
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if written >= size || done {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("telegram: timed out waiting for the audio buffer")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// downloadStream writes the document into the growing file and renames it when
// the transfer completes. It owns its context: the caller returns as soon as
// the buffer is ready and the download must outlive it.
func (p *Provider) downloadStream(chat string, msgID int, doc *tg.Document, source *streamSource, writerFile *os.File, partialPath, finalPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer writerFile.Close()
	writer := streamWriter{source: source, file: writerFile}
	location := doc.AsInputDocumentFileLocation("")
	_, err := downloader.NewDownloader().Download(p.api, location).Stream(ctx, writer)
	writer.finish(err)
	if err != nil {
		applog.Warn("telegram: download of message %d failed: %v", doc.ID, err)
		os.Remove(partialPath)
		return
	}
	// On Unix the open descriptor survives the rename, so a decoder already
	// reading the partial file keeps working.
	if err := os.Rename(partialPath, finalPath); err != nil {
		os.Remove(partialPath)
	}
}

func (p *Provider) fetchDocument(ctx context.Context, msgID int) (*tg.Document, error) {
	got, err := p.api.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: msgID},
	})
	if err != nil {
		return nil, fmt.Errorf("telegram: message %d: %w", msgID, err)
	}
	messages, ok := got.(*tg.MessagesMessages)
	if !ok || len(messages.Messages) == 0 {
		return nil, fmt.Errorf("telegram: message %d not found", msgID)
	}
	message, ok := messages.Messages[0].(*tg.Message)
	if !ok {
		return nil, fmt.Errorf("telegram: message %d is not a message", msgID)
	}
	media, ok := message.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, fmt.Errorf("telegram: message %d has no document", msgID)
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return nil, fmt.Errorf("telegram: message %d has no document", msgID)
	}
	return doc, nil
}

func decode(reader io.Reader, extension string) (beep.StreamSeekCloser, beep.Format, error) {
	switch strings.ToLower(extension) {
	case ".mp3":
		readCloser, ok := reader.(io.ReadCloser)
		if !ok {
			return nil, beep.Format{}, errors.New("telegram: mp3 stream is not a ReadCloser")
		}
		return mp3.Decode(readCloser)
	case ".ogg", ".oga", ".opus":
		readCloser, ok := reader.(io.ReadCloser)
		if !ok {
			return nil, beep.Format{}, errors.New("telegram: ogg stream is not a ReadCloser")
		}
		return vorbis.Decode(readCloser)
	default:
		return flac.Decode(reader)
	}
}
