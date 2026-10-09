//go:build !windows

package luaplugin

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// minimalExecEnv returns the restricted environment for plugin subprocesses:
// PATH, HOME and LANG, plus any explicit cliamp config overrides so a
// `cliamp remote call` child resolves the same config dir as the daemon.
func minimalExecEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	env := []string{
		"PATH=" + path,
		"HOME=" + homeEnv(),
		"LANG=C.UTF-8",
	}
	// Same as Windows: an explicit config override must reach `cliamp
	// remote call` children so they find the daemon socket.
	for _, key := range []string{"CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
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
