package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeYTDL puts a yt-dlp shell script with body on PATH for the test.
func fakeYTDL(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDownloadYTDLContext(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    string
		wantErr string
	}{
		{name: "downloaded file", script: `echo '{"_filename":"/music/Artist - Song.m4a"}'`, want: "/music/Artist - Song.m4a"},
		{name: "no file", script: `echo '{}'`, wantErr: "no file downloaded"},
		{name: "yt-dlp error", script: "echo 'ERROR: video unavailable' >&2\nexit 1", wantErr: "ERROR: video unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeYTDL(t, tt.script)
			got, err := DownloadYTDLContext(context.Background(), "https://example.com/v1", t.TempDir())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DownloadYTDLContext() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DownloadYTDLContext() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

// TestContextVariantsStopAtCancel checks that a cancel stops a slow yt-dlp
// run at once.
func TestContextVariantsStopAtCancel(t *testing.T) {
	tests := []struct {
		name string
		run  func(ctx context.Context, dir string) error
	}{
		{
			name: "RemoteContext",
			run: func(ctx context.Context, _ string) error {
				_, err := RemoteContext(ctx, []string{"ytsearch:slow query"})
				return err
			},
		},
		{
			name: "DownloadYTDLContext",
			run: func(ctx context.Context, dir string) error {
				_, err := DownloadYTDLContext(ctx, "https://example.com/v1", dir)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeYTDL(t, "exec sleep 30")
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(100*time.Millisecond, cancel)
			start := time.Now()
			if err := tt.run(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("returned after %v, want it to stop at the cancel", elapsed)
			}
		})
	}
}
