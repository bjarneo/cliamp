package luaplugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestSandboxBlocksUnsafeGlobalsAndModules(t *testing.T) {
	states := []struct {
		name string
		make func() *lua.LState
	}{
		{"newSandboxedState", newSandboxedState},
		{"sandbox over full state", func() *lua.LState {
			L := lua.NewState()
			sandbox(L)
			return L
		}},
	}
	blockedGlobals := []string{
		"dofile", "loadfile", "load", "loadstring",
		"require", "module", "_printregs", "collectgarbage",
		"package", "debug", "io", "channel",
	}
	blockedOS := []string{
		"execute", "remove", "rename", "exit",
		"setlocale", "tmpname", "setenv",
	}
	allowedOS := []string{"clock", "date", "difftime", "getenv", "time"}

	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			L := st.make()
			defer L.Close()

			for _, name := range blockedGlobals {
				if L.GetGlobal(name) != lua.LNil {
					t.Errorf("global %s must be nil", name)
				}
			}

			osTbl, ok := L.GetGlobal("os").(*lua.LTable)
			if !ok {
				t.Fatal("os global must be a table")
			}
			for _, name := range blockedOS {
				if osTbl.RawGetString(name) != lua.LNil {
					t.Errorf("os.%s must be nil", name)
				}
			}
			for _, name := range allowedOS {
				if osTbl.RawGetString(name) == lua.LNil {
					t.Errorf("os.%s must be present", name)
				}
			}
			// Verify os contains no unexpected extra functions.
			osCount := 0
			osTbl.ForEach(func(k, _ lua.LValue) {
				osCount++
			})
			if osCount != len(allowedOS) {
				t.Errorf("os table has %d entries, want %d", osCount, len(allowedOS))
			}
		})
	}
}

func TestSandboxAllowsSafeStdlib(t *testing.T) {
	L := newSandboxedState()
	defer L.Close()

	tests := []struct {
		name string
		code string
		want string
	}{
		{"math", `_G.r = tostring(math.floor(3.9))`, "3"},
		{"table", `local t = {3, 1, 2}; table.sort(t); _G.r = table.concat(t, ",")`, "1,2,3"},
		{"string", `_G.r = string.upper("abc")`, "ABC"},
		{"coroutine", `local c = coroutine.wrap(function() coroutine.yield("ok") end); _G.r = c()`, "ok"},
		{"os.difftime", `_G.r = tostring(os.difftime(100, 40))`, "60"},
		{"os.time", `_G.r = type(os.time())`, "number"},
		{"os.clock", `_G.r = type(os.clock())`, "number"},
		{"os.date", `_G.r = type(os.date("%Y"))`, "string"},
		{"os.getenv", `_G.r = type(os.getenv("PATH"))`, "string"},
		{"utf8.char", `_G.r = utf8.char(72, 101, 108, 108, 111)`, "Hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := L.DoString(tt.code); err != nil {
				t.Fatal(err)
			}
			if got := L.GetGlobal("r").String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSandboxStringOutputCaps(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr string
		wantLen int
	}{
		{"rep within cap", `_G.out = string.rep("a", 1024)`, "", 1024},
		{"rep at exact cap", fmt.Sprintf(`_G.out = string.rep("a", %d)`, maxStringOutputBytes), "", maxStringOutputBytes},
		{"rep over cap", fmt.Sprintf(`_G.out = string.rep("a", %d)`, maxStringOutputBytes+1), "exceeds", 0},
		{"rep method over cap", fmt.Sprintf(`_G.out = ("ab"):rep(%d)`, maxStringOutputBytes/2+1), "exceeds", 0},
		{"rep infinity", `_G.out = string.rep("a", 1/0)`, "exceeds", 0},
		{"rep negative returns empty", `_G.out = string.rep("a", -5)`, "", 0},
		{"rep empty string large count", `_G.out = string.rep("", 1000000000)`, "", 0},
		{"format normal", `_G.out = string.format("%s %d %.2f %%", "ok", 42, 3.14)`, "", len("ok 42 3.14 %")},
		{"format huge width", `_G.out = string.format("%999999999s", "a")`, "exceeds", 0},
		{"format huge numeric width", `_G.out = string.format("%100000000d", 1)`, "exceeds", 0},
		{"format huge precision", `_G.out = string.format("%.999999999f", 1.0)`, "exceeds", 0},
		{"format method huge width", `_G.out = ("%999999999s"):format("a")`, "exceeds", 0},
		{"format repeated explicit index", `local s = string.rep("x", 300000); _G.out = string.format(string.rep("%[1]s", 20), s)`, "exceeds", 0},
		{"format cumulative width over cap", `_G.out = string.format("%1500000s%1500000s%1500000s", "a", "b", "c")`, "exceeds", 0},
		{"format precision truncation under cap", `local s = string.rep("a", 2000000); _G.out = string.format("%.5s", s)`, "", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := newSandboxedState()
			defer L.Close()
			err := L.DoString(tt.code)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DoString error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DoString error = %v", err)
			}
			if got := len(L.GetGlobal("out").String()); got != tt.wantLen {
				t.Errorf("len(out) = %d, want %d", got, tt.wantLen)
			}
		})
	}
}

func TestSandboxStringPatternCaps(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr string
	}{
		{"find normal", `assert(string.find("hello world", "world") == 7)`, ""},
		{"match normal", `assert(string.match("abc123", "%d+") == "123")`, ""},
		{"gsub normal", `assert(string.gsub("foo bar", "foo", "baz") == "baz bar")`, ""},
		{"gmatch normal", `
			local out = {}
			for w in string.gmatch("a b c", "%a+") do table.insert(out, w) end
			assert(table.concat(out, ",") == "a,b,c")
		`, ""},
		{"gfind normal", `
			local out = {}
			for w in string.gfind("x y", "%a+") do table.insert(out, w) end
			assert(table.concat(out, ",") == "x,y")
		`, ""},
		{"find subject too large", fmt.Sprintf(`string.find(string.rep("a", %d), "a")`, maxPatternInputBytes+1), "input exceeds"},
		{"match subject too large", fmt.Sprintf(`string.match(string.rep("a", %d), "a")`, maxPatternInputBytes+1), "input exceeds"},
		{"gsub subject too large", fmt.Sprintf(`string.gsub(string.rep("a", %d), "a", "b")`, maxPatternInputBytes+1), "input exceeds"},
		{"gmatch subject too large", fmt.Sprintf(`string.gmatch(string.rep("a", %d), "a")`, maxPatternInputBytes+1), "input exceeds"},
		{"gfind subject too large", fmt.Sprintf(`string.gfind(string.rep("a", %d), "a")`, maxPatternInputBytes+1), "input exceeds"},
		{"find pattern too large", fmt.Sprintf(`string.find("abc", string.rep("a", %d))`, maxPatternBytes+1), "pattern exceeds"},
		{"match pattern too large", fmt.Sprintf(`string.match("abc", string.rep("a", %d))`, maxPatternBytes+1), "pattern exceeds"},
		{"gsub pattern too large", fmt.Sprintf(`string.gsub("abc", string.rep("a", %d), "x")`, maxPatternBytes+1), "pattern exceeds"},
		{"gmatch pattern too large", fmt.Sprintf(`string.gmatch("abc", string.rep("a", %d))`, maxPatternBytes+1), "pattern exceeds"},
		{"method match pattern too large", fmt.Sprintf(`("abc"):match(string.rep("a", %d))`, maxPatternBytes+1), "pattern exceeds"},
		{"gsub output over cap", `string.gsub(string.rep("a", 50), "a", string.rep("b", 100000))`, "output exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := newSandboxedState()
			defer L.Close()
			err := L.DoString(tt.code)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DoString error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DoString error = %v", err)
			}
		})
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
