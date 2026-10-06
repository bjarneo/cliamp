package ytdlp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallHintLinuxPackageManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the package manager lookup runs on Linux only")
	}
	for _, tt := range []struct {
		name  string
		tools []string
		want  string
	}{
		{name: "apt", tools: []string{"apt-get"}, want: "sudo apt install yt-dlp"},
		{name: "apt before pacman", tools: []string{"apt-get", "pacman"}, want: "sudo apt install yt-dlp"},
		{name: "pacman", tools: []string{"pacman"}, want: "sudo pacman -S yt-dlp"},
		{name: "none", want: "pip install yt-dlp"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, tool := range tt.tools {
				if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			if got := InstallHint(); got != tt.want {
				t.Fatalf("InstallHint() = %q, want %q", got, tt.want)
			}
		})
	}
}
