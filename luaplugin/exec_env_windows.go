//go:build windows

package luaplugin

import "os"

func minimalExecEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeEnv(),
		"USERPROFILE=" + homeEnv(),
	}
	if v := os.Getenv("APPDATA"); v != "" {
		env = append(env, "APPDATA="+v)
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		env = append(env, "LOCALAPPDATA="+v)
	}
	if v := os.Getenv("ComSpec"); v != "" {
		env = append(env, "ComSpec="+v)
	}
	if v := os.Getenv("PATHEXT"); v != "" {
		env = append(env, "PATHEXT="+v)
	}
	if v := os.Getenv("SystemRoot"); v != "" {
		env = append(env, "SystemRoot="+v)
	}
	if v := os.Getenv("WINDIR"); v != "" {
		env = append(env, "WINDIR="+v)
	}
	if v := os.Getenv("TEMP"); v != "" {
		env = append(env, "TEMP="+v)
	}
	if v := os.Getenv("TMP"); v != "" {
		env = append(env, "TMP="+v)
	}
	return env
}
