//go:build !windows

package luaplugin

import "os"

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
