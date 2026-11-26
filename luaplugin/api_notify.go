package luaplugin

import (
	"context"
	"os"
	"os/exec"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// notifyTimeout bounds one run of notify-send. The time limit of the calling
// callback can end it sooner.
const notifyTimeout = 2 * time.Second

// minimalNotifyEnv builds a minimal environment for notify-send that preserves
// session bus and display variables without leaking the full process environment.
func minimalNotifyEnv() []string {
	env := minimalExecEnv()
	for _, key := range []string{
		"DBUS_SESSION_BUS_ADDRESS",
		"DISPLAY",
		"WAYLAND_DISPLAY",
		"XDG_RUNTIME_DIR",
	} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// registerNotifyAPI adds cliamp.notify(title, body) which sends a desktop
// notification via notify-send.
func registerNotifyAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	L.SetField(cliamp, "notify", L.NewFunction(func(L *lua.LState) int {
		title := L.CheckString(1)
		body := L.OptString(2, "")

		args := []string{"--", title}
		if body != "" {
			args = append(args, body)
		}

		path, err := exec.LookPath("notify-send")
		if err != nil {
			p.logger.log(p.installName, "warn", "notify-send not found: %v", err)
			return 0
		}

		ctx, cancel := context.WithTimeout(callContext(L), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = minimalNotifyEnv()
		if err := cmd.Run(); err != nil {
			p.logger.log(p.installName, "error", "notify-send failed: %v", err)
		}
		return 0
	}))
}
