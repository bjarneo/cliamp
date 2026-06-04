//go:build !windows

package luaplugin

import "os"

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
