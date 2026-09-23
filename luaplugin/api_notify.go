package luaplugin

import (
	"os/exec"

	lua "github.com/yuin/gopher-lua"
)

// registerNotifyAPI adds cliamp.notify(title, body) which sends a desktop
// notification via notify-send. Safe alternative to os.execute for this
// specific use case.
func registerNotifyAPI(L *lua.LState, cliamp *lua.LTable, logger *pluginLogger, pluginName string) {
	L.SetField(cliamp, "notify", L.NewFunction(func(L *lua.LState) int {
		title := L.CheckString(1)
		body := L.OptString(2, "")

		// "--" ends option parsing so a title starting with "-" is treated
		// as the positional summary, not as a notify-send option.
		args := []string{"--", title}
		if body != "" {
			args = append(args, body)
		}

		if _, err := exec.LookPath("notify-send"); err != nil {
			logger.log(pluginName, "warn", "notify-send not found: %v", err)
			return 0
		}

		cmd := exec.Command("notify-send", args...)
		if err := cmd.Run(); err != nil {
			logger.log(pluginName, "error", "notify-send failed: %v", err)
		}
		return 0
	}))
}
