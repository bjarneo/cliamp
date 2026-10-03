package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
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
		{"no port", "nas", slices.Concat(options, []string{"nas"})},
		{"user", "me@nas", slices.Concat(options, []string{"me@nas"})},
		{"port", "nas:2222", slices.Concat(options, []string{"-p", "2222", "nas"})},
		{"user and port", "me@nas:2222", slices.Concat(options, []string{"-p", "2222", "me@nas"})},
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
