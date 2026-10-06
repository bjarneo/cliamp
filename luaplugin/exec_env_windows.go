//go:build windows

package luaplugin

import (
	"os"
	"os/exec"
)

func minimalExecEnv() []string {
	home := homeEnv()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USERPROFILE=" + home,
	}
	// Pass through the Windows variables subprocesses commonly need; skip
	// any that are unset.
	for _, key := range []string{"APPDATA", "LOCALAPPDATA", "ComSpec", "PATHEXT", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// killProcessGroup does nothing on Windows. The cancel of cmd kills only the
// binary, and the exec manager closes the pipes after execPipeGrace.
func killProcessGroup(*exec.Cmd) {}
