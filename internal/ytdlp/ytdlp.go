// Package ytdlp holds yt-dlp helpers that need no audio code, so provider
// packages can use them without a link to the player.
package ytdlp

import (
	"os/exec"
	"runtime"
)

// InstallHint returns a platform-specific install command suggestion.
func InstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install yt-dlp"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install yt-dlp"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S yt-dlp"
		}
		return "pip install yt-dlp"
	case "windows":
		return "winget install yt-dlp"
	default:
		return "pip install yt-dlp"
	}
}
