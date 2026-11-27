package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsInvalidURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "about scheme", url: "about:blank"},
		{name: "file scheme", url: "file:///etc/passwd"},
		{name: "javascript scheme", url: "javascript:alert(1)"},
		{name: "empty host", url: "https:///path"},
		{name: "empty string", url: ""},
		{name: "bare path", url: "/usr/bin/sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Open(tt.url); err == nil {
				t.Errorf("Open(%q) = nil, want error", tt.url)
			}
		})
	}
}

// TestOpenWithUnreachablePATH confirms Open returns a "not found" error when
// xdg-open / open / rundll32 cannot be located. We set PATH to an empty
// temp dir so the call can't actually spawn a browser.
func TestOpenWithUnreachablePATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no executables here
	err := Open("https://example.com")
	if err == nil {
		t.Error("Open should error when the dispatcher binary is not on PATH")
	}
}

func TestOpenLaunchesHTTPURL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake xdg-open test runs on Linux")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "opened.txt")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > \"" + out + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if err := Open("https://example.com/auth?code=1"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(out)
		if err == nil && strings.TrimSpace(string(data)) == "https://example.com/auth?code=1" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("xdg-open did not record URL: data=%q err=%v", string(data), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestOpenUnsupportedPlatform verifies the default case. We can only reach
// it on non-Linux/Darwin/Windows hosts; elsewhere we skip rather than try
// to duplicate the switch body.
func TestOpenUnsupportedPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		t.Skipf("host is %s, cannot exercise unsupported-platform branch without build tags", runtime.GOOS)
	}
	err := Open("https://example.com")
	if err == nil {
		t.Error("Open on unsupported platform should return error")
	}
}
