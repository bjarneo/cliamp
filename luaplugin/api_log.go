package luaplugin

import (
	"fmt"
	"os"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

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
func registerLogAPI(L *lua.LState, cliamp *lua.LTable, logger *pluginLogger, pluginName string) {
	tbl := L.NewTable()
	for _, level := range []string{"info", "warn", "error", "debug"} {
		L.SetField(tbl, level, L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			logger.log(pluginName, level, "%s", msg)
			return 0
		}))
	}
	L.SetField(cliamp, "log", tbl)
}
