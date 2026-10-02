package luaplugin

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestSandboxBlocksDofile(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("dofile") != lua.LNil {
		t.Fatal("dofile should be nil after sandbox")
	}
}

func TestSandboxBlocksLoadfile(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("loadfile") != lua.LNil {
		t.Fatal("loadfile should be nil after sandbox")
	}
}

func TestSandboxRemovesIOModule(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("io") != lua.LNil {
		t.Fatal("io module should be nil after sandbox")
	}
}

func TestSandboxRestrictsOS(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	os := L.GetGlobal("os").(*lua.LTable)

	blocked := []string{"execute", "remove", "rename", "exit", "setlocale", "tmpname"}
	for _, name := range blocked {
		if os.RawGetString(name) != lua.LNil {
			t.Errorf("os.%s should be nil after sandbox", name)
		}
	}

	// Safe functions should remain.
	allowed := []string{"time", "date", "clock"}
	for _, name := range allowed {
		if os.RawGetString(name) == lua.LNil {
			t.Errorf("os.%s should still be available after sandbox", name)
		}
	}
}

func TestSandboxProvidesUTF8Char(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	err := L.DoString(`_G.result = utf8.char(72, 101, 108, 108, 111)`)
	if err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("result").String(); got != "Hello" {
		t.Fatalf("utf8.char(72,101,108,108,111) = %q, want %q", got, "Hello")
	}
}

// captureStdout returns what fn writes to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	w.Close()
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// print must not reach stdout. Before approval, ReadMetadata runs the
// untrusted file while the trust prompt is on the terminal. At runtime,
// stdout is the TUI. At runtime, print writes to plugins.log instead.
func TestPrintDoesNotReachStdout(t *testing.T) {
	const code = `
		print("PRINTED", 1, nil)
		plugin.register({name = "print-test", type = "hook"})
	`
	tests := []struct {
		name    string
		run     func(t *testing.T, logPath string)
		wantLog string
	}{
		{"metadata check", func(t *testing.T, _ string) {
			if _, err := ReadMetadata(code); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"runtime", func(t *testing.T, logPath string) {
			m := newTestManager()
			m.logger = newPluginLogger(logPath)
			defer m.Close()
			loadTestPlugin(t, m, "print-test", code)
		}, "[print-test] info: PRINTED\t1\tnil\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), pluginLogName)
			if out := captureStdout(t, func() { tt.run(t, logPath) }); out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
			log, _ := os.ReadFile(logPath)
			if tt.wantLog == "" && len(log) != 0 {
				t.Errorf("plugins.log = %q, want nothing", log)
			}
			if tt.wantLog != "" && !strings.HasSuffix(string(log), tt.wantLog) {
				t.Errorf("plugins.log = %q, want a line that ends with %q", log, tt.wantLog)
			}
		})
	}
}
