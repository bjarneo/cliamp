package telegram

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bjarneo/cliamp/player"
)

func TestStreamSourceServesGrowingFile(t *testing.T) {
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "track.flac"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	source := newStreamSource(file)

	go func() {
		writer := streamWriter{source: source, file: file}
		_, _ = writer.Write([]byte("hello "))
		time.Sleep(50 * time.Millisecond)
		_, _ = writer.Write([]byte("world"))
		writer.finish(nil)
	}()

	data, err := io.ReadAll(source)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got, want := string(data), "hello world"; got != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

func TestStreamSourceSeekWithinDownloadedData(t *testing.T) {
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "track.flac"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	source := newStreamSource(file)
	writer := streamWriter{source: source, file: file}
	if _, err := writer.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	writer.finish(nil)

	if _, err := source.Seek(4, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	buffer := make([]byte, 3)
	if _, err := io.ReadFull(source, buffer); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if got, want := string(buffer), "456"; got != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

func TestStreamSourceReportsDownloadError(t *testing.T) {
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "track.flac"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	source := newStreamSource(file)
	writer := streamWriter{source: source, file: file}
	_, _ = writer.Write([]byte("part"))
	writer.finish(io.ErrUnexpectedEOF)

	if _, err := io.ReadAll(source); err == nil {
		t.Fatal("expected the download error to surface")
	}
}

// A native decoder waits in Stream for the download while the player holds
// the speaker lock. Interrupt must end that wait, and every later Read must
// fail, also after the download goes on.
func TestStreamSourceInterruptEndsWaitingRead(t *testing.T) {
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "track.flac"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })

	synctest.Test(t, func(t *testing.T) {
		source := newStreamSource(file)
		var streamer player.Interrupter = interruptibleStreamer{source: source}
		readDone := make(chan error, 1)
		go func() {
			_, err := source.Read(make([]byte, 4))
			readDone <- err
		}()
		synctest.Wait()
		select {
		case err := <-readDone:
			t.Fatalf("Read() returned %v before the download had data", err)
		default:
		}

		streamer.Interrupt()
		synctest.Wait()
		select {
		case err := <-readDone:
			if !errors.Is(err, errStreamInterrupted) {
				t.Fatalf("Read() after Interrupt error = %v, want %v", err, errStreamInterrupted)
			}
		default:
			t.Fatal("Read() still waits for the download after Interrupt")
		}

		writer := streamWriter{source: source, file: file}
		if _, err := writer.Write([]byte("more")); err != nil {
			t.Fatal(err)
		}
		writer.finish(nil)
		if _, err := source.Read(make([]byte, 4)); !errors.Is(err, errStreamInterrupted) {
			t.Fatalf("Read() after the download error = %v, want %v", err, errStreamInterrupted)
		}
	})
}
