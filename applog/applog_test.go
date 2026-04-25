package applog

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// reset clears package-level state so tests don't leak into each other.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	entries = nil
	if currentFile != nil {
		_ = currentFile.Close()
		currentFile = nil
	}
	logger.Store(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestUserWarnAndDrain(t *testing.T) {
	reset(t)

	UserWarn("hello %s", "world")
	UserWarn("n=%d", 42)

	got := Drain()
	if len(got) != 2 {
		t.Fatalf("Drain() returned %d entries, want 2", len(got))
	}
	if got[0].Text != "hello world" {
		t.Errorf("entries[0].Text = %q, want %q", got[0].Text, "hello world")
	}
	if got[1].Text != "n=42" {
		t.Errorf("entries[1].Text = %q, want %q", got[1].Text, "n=42")
	}
	if got[0].At.IsZero() {
		t.Error("At should be set to the log time")
	}
}

func TestDrainClearsBuffer(t *testing.T) {
	reset(t)

	UserWarn("first")
	_ = Drain()

	got := Drain()
	if got != nil {
		t.Errorf("second Drain() = %v, want nil", got)
	}
}

func TestDrainEmpty(t *testing.T) {
	reset(t)

	got := Drain()
	if got != nil {
		t.Errorf("Drain() on empty buffer = %v, want nil", got)
	}
}

func TestFooterRingBufferCap(t *testing.T) {
	reset(t)

	for i := 0; i < 6; i++ {
		UserWarn("msg-%d", i)
	}
	got := Drain()
	if len(got) != maxEntries {
		t.Fatalf("len(entries) = %d, want %d", len(got), maxEntries)
	}
	for i, e := range got {
		want := "msg-" + string(rune('2'+i))
		if e.Text != want {
			t.Errorf("entries[%d].Text = %q, want %q", i, e.Text, want)
		}
	}
}

func TestFooterConcurrent(t *testing.T) {
	reset(t)

	const goroutines = 20
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				UserWarn("g-log")
			}
		}()
	}
	wg.Wait()

	got := Drain()
	if len(got) > maxEntries {
		t.Errorf("len(entries) = %d, exceeds cap %d", len(got), maxEntries)
	}
	for _, e := range got {
		if !strings.HasPrefix(e.Text, "g-log") {
			t.Errorf("unexpected entry text %q", e.Text)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    Level
		wantErr bool
	}{
		{"", LevelInfo, false},
		{"info", LevelInfo, false},
		{"INFO", LevelInfo, false},
		{" debug ", LevelDebug, false},
		{"warn", LevelWarn, false},
		{"warning", LevelWarn, false},
		{"error", LevelError, false},
		{"trace", LevelInfo, true},
		{"verbose", LevelInfo, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("level = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusFootersOnly(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "cliamp.log")
	closeFn, err := Init(path, LevelDebug)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	Status("nothing-on-disk")

	if got := Drain(); len(got) != 1 || got[0].Text != "nothing-on-disk" {
		t.Errorf("footer = %v, want one entry 'nothing-on-disk'", got)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "nothing-on-disk") {
		t.Errorf("Status leaked to log file: %s", data)
	}
}

func TestDiagnosticLogsSkipFooter(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "cliamp.log")
	closeFn, err := Init(path, LevelDebug)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	Debug("dbg-msg")
	Info("info-msg")
	Warn("warn-msg")
	Error("err-msg")

	if got := Drain(); got != nil {
		t.Errorf("footer = %v, want nil (diagnostic logs must not push to footer)", got)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{"dbg-msg", "info-msg", "warn-msg", "err-msg"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log file missing %q\nfile contents:\n%s", want, data)
		}
	}
}

func TestUserLogsHitBothSinks(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "cliamp.log")
	closeFn, err := Init(path, LevelDebug)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	UserWarn("careful: %s", "oops")
	UserError("boom: %d", 7)

	got := Drain()
	if len(got) != 2 {
		t.Fatalf("footer entries = %d, want 2", len(got))
	}
	if got[0].Text != "careful: oops" || got[1].Text != "boom: 7" {
		t.Errorf("unexpected footer texts: %+v", got)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{"careful: oops", "boom: 7", "level=WARN", "level=ERROR"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log file missing %q\nfile contents:\n%s", want, data)
		}
	}
}

func TestLevelFilteringSuppressesBelowThreshold(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "cliamp.log")
	closeFn, err := Init(path, LevelWarn)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	Debug("hidden-debug")
	Info("hidden-info")
	Warn("visible-warn")

	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "hidden-debug") || strings.Contains(string(data), "hidden-info") {
		t.Errorf("entries below threshold leaked:\n%s", data)
	}
	if !strings.Contains(string(data), "visible-warn") {
		t.Errorf("warn entry missing:\n%s", data)
	}
}

func TestInitTwiceClosesPreviousFile(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")

	close1, err := Init(first, LevelInfo)
	if err != nil {
		t.Fatalf("Init first: %v", err)
	}
	Info("to-first")

	close2, err := Init(second, LevelInfo)
	if err != nil {
		t.Fatalf("Init second: %v", err)
	}
	Info("to-second")

	// close1 should be a no-op now (the second Init closed the file already).
	_ = close1()

	if err := close2(); err != nil {
		t.Fatalf("close2: %v", err)
	}

	firstData, _ := os.ReadFile(first)
	if !strings.Contains(string(firstData), "to-first") {
		t.Errorf("first log missing entry:\n%s", firstData)
	}
	secondData, _ := os.ReadFile(second)
	if !strings.Contains(string(secondData), "to-second") {
		t.Errorf("second log missing entry:\n%s", secondData)
	}
	if strings.Contains(string(firstData), "to-second") {
		t.Errorf("first log received post-rotation entry:\n%s", firstData)
	}
}

func TestInitMissingDirCreatesIt(t *testing.T) {
	reset(t)
	nested := filepath.Join(t.TempDir(), "a", "b", "c", "cliamp.log")
	closeFn, err := Init(nested, LevelInfo)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}
