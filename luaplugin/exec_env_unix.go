//go:build !windows

package luaplugin

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func minimalExecEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"PATH=" + path,
		"HOME=" + homeEnv(),
		"LANG=C.UTF-8",
	}
}

// killProcessGroup starts cmd in its own process group and makes the cancel
// of cmd kill that whole group. Thus a cancel or a timeout also stops the
// child processes of the binary, such as the ffmpeg that yt-dlp starts. They
// inherit the output pipes and keep them open.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
