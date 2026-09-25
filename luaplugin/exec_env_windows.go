//go:build windows

package luaplugin

import "os"

// minimalExecEnv returns the restricted environment for plugin subprocesses:
// PATH, a HOME derived from homeEnv, the daemon's own USERPROFILE (so a
// customized HOME cannot make the child resolve a different config dir and
// miss the daemon socket), plus the Windows variables subprocesses commonly
// need and any explicit cliamp config overrides so a `cliamp remote call`
// child resolves the same config dir as the daemon.
func minimalExecEnv() []string {
	home := homeEnv()
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile = home
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USERPROFILE=" + userProfile,
	}
	// Pass through the Windows variables subprocesses commonly need; skip
	// any that are unset. CLIAMP_CONFIG_DIR/XDG_* must propagate so a
	// `cliamp remote call` child resolves the same config dir (and socket)
	// as the daemon instead of falling back to HOME/.config/cliamp.
	for _, key := range []string{"APPDATA", "LOCALAPPDATA", "CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "ComSpec", "PATHEXT", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}
