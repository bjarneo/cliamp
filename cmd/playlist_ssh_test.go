package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/sshurl"
)

// fakeSSH puts an ssh script first on PATH. The script writes its arguments,
// one per line, to the returned file and prints out.
func fakeSSH(t *testing.T, out string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh binary is a shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsFile) + "\nprintf '%s\\n' " + shellQuote(out) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", dir)
	return argsFile
}

// sshArgs returns the arguments that the fake ssh received. The last one is
// the remote command.
func sshArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ssh was not called: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestSSHCommandsKeepPort(t *testing.T) {
	options := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5"}
	tests := []struct {
		name string
		host string   // the --ssh value and the host part of an ssh:// path
		want []string // ssh arguments before the remote command
	}{
		{"no port", "nas", slices.Concat(options, []string{"--", "nas"})},
		{"user", "me@nas", slices.Concat(options, []string{"--", "me@nas"})},
		{"port", "nas:2222", slices.Concat(options, []string{"-p", "2222", "--", "nas"})},
		{"user and port", "me@nas:2222", slices.Concat(options, []string{"-p", "2222", "--", "me@nas"})},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/enrich", func(t *testing.T) {
			argsFile := fakeSSH(t, "123.4")
			if got := probeDuration("ssh://" + tt.host + "/music/a b.mp3"); got != 123 {
				t.Errorf("probeDuration() = %d, want 123", got)
			}
			args := sshArgs(t, argsFile)
			if got := args[:len(args)-1]; !slices.Equal(got, tt.want) {
				t.Errorf("ssh args = %q, want %q", got, tt.want)
			}
			if remote := args[len(args)-1]; !strings.Contains(remote, shellQuote("/music/a b.mp3")) {
				t.Errorf("remote command %q does not probe the track path", remote)
			}
		})
		t.Run(tt.name+"/create", func(t *testing.T) {
			argsFile := fakeSSH(t, "/music/a.mp3")
			got, err := sshFindAudio(tt.host, []string{"/music"})
			if err != nil {
				t.Fatalf("sshFindAudio() error = %v", err)
			}
			if !slices.Equal(got, []string{"/music/a.mp3"}) {
				t.Errorf("sshFindAudio() = %q, want [/music/a.mp3]", got)
			}
			args := sshArgs(t, argsFile)
			if got := args[:len(args)-1]; !slices.Equal(got, tt.want) {
				t.Errorf("ssh args = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSSHFindAudioRejectsBadHost(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{"ssh option", "-oProxyCommand=touch /tmp/x"},
		{"equals sign", "nas=1"},
		{"path in host", "nas/music"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsFile := fakeSSH(t, "/music/a.mp3")
			if _, err := sshFindAudio(tt.host, []string{"/music"}); err == nil {
				t.Fatalf("sshFindAudio(%q) error = nil, want an error", tt.host)
			}
			if _, err := os.Stat(argsFile); err == nil {
				t.Errorf("sshFindAudio(%q) ran ssh", tt.host)
			}
		})
	}
}

func TestSSHFindAudioRejectsLeadingDashPaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
	}{
		{name: "leading dash primary", paths: []string{"-exec"}},
		{name: "leading dash delete", paths: []string{"/music", "-delete"}},
		{name: "empty path", paths: []string{""}},
		{name: "find negation operator", paths: []string{"!"}},
		{name: "find group operator", paths: []string{"("}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsFile := fakeSSH(t, "/music/a.mp3")
			if _, err := sshFindAudio("nas", tt.paths); err == nil {
				t.Fatalf("sshFindAudio(%q) error = nil, want an error", tt.paths)
			}
			if _, err := os.Stat(argsFile); err == nil {
				t.Errorf("sshFindAudio(%q) ran ssh", tt.paths)
			}
		})
	}
}

func TestBuildSSHURLRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		remotePath string
		wantHost   string
		wantPort   string
	}{
		{name: "plain path", host: "nas", remotePath: "/music/song.mp3", wantHost: "nas"},
		{name: "special characters", host: "me@nas:2222", remotePath: "/music/Track #1 (100%)?.flac", wantHost: "me@nas", wantPort: "2222"},
		{name: "spaces and percent", host: "nas", remotePath: "/music/a b%20c.mp3", wantHost: "nas"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := buildSSHURL(tt.host, tt.remotePath)
			if err != nil {
				t.Fatalf("buildSSHURL() error = %v", err)
			}
			parsed, err := sshurl.Parse(raw)
			if err != nil {
				t.Fatalf("sshurl.Parse(%q) error = %v", raw, err)
			}
			if parsed.Host != tt.wantHost || parsed.Port != tt.wantPort || parsed.Path != tt.remotePath {
				t.Fatalf("parsed = %+v, want host=%q port=%q path=%q", parsed, tt.wantHost, tt.wantPort, tt.remotePath)
			}
		})
	}
}

func TestProbeDurationUsesInputFlagAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ffprobe uses a shell script")
	}

	t.Run("passes -i before path", func(t *testing.T) {
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "args")
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsFile) + "\nprintf '42.9\\n'\n"
		if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)

		got := probeDuration("-leading-dash.mp3")
		if got != 42 {
			t.Fatalf("probeDuration() = %d, want 42", got)
		}
		data, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if len(args) < 2 || args[len(args)-2] != "-i" || args[len(args)-1] != "-leading-dash.mp3" {
			t.Fatalf("ffprobe args = %q, want trailing -i -leading-dash.mp3", args)
		}
	})

	t.Run("cancels hung ffprobe on timeout", func(t *testing.T) {
		dir := t.TempDir()
		script := "#!/bin/sh\nexec sleep 10\n"
		if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)

		oldTimeout := probeTimeout
		probeTimeout = 100 * time.Millisecond
		defer func() { probeTimeout = oldTimeout }()

		start := time.Now()
		if got := probeDuration("/music/slow.mp3"); got != 0 {
			t.Fatalf("probeDuration() = %d, want 0 on timeout", got)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("probeDuration() took %v, want timeout near 100ms", elapsed)
		}
	})
}
