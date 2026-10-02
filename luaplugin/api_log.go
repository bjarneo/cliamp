package luaplugin

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// pluginLogName is the plugin log file in the config dir.
const pluginLogName = "plugins.log"

// pluginLogger writes plugin log messages to ~/.config/cliamp/plugins.log.
type pluginLogger struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func newPluginLogger(path string) *pluginLogger {
	return &pluginLogger{path: path}
}

func (l *pluginLogger) log(plugin, level, format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		l.f = f
	}

	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(l.f, "%s [%s] %s: %s\n", ts, plugin, level, msg)
}

func (l *pluginLogger) close() {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	l.f.Close()
	l.f = nil
	l.mu.Unlock()
}

// registerLogAPI adds cliamp.log.{info,warn,error,debug} to the cliamp table.
// It also makes print write its arguments to plugins.log at the info level,
// joined by tabs as the base print joins them.
func registerLogAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	tbl := L.NewTable()
	for _, level := range []string{"info", "warn", "error", "debug"} {
		L.SetField(tbl, level, L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			p.logger.log(p.installName, level, "%s", msg)
			return 0
		}))
	}
	L.SetField(cliamp, "log", tbl)

	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		parts := make([]string, L.GetTop())
		for i := range parts {
			parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
		}
		p.logger.log(p.installName, "info", "%s", strings.Join(parts, "\t"))
		return 0
	}))
}
