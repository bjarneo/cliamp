package luaplugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func TestWriteAllowlistBlocksSymlinkParentTraversal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	outsideParent := filepath.Join(base, "outside")
	outsideSub := filepath.Join(outsideParent, "sub")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideSub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "link")
	if err := os.Symlink(outsideSub, link); err != nil {
		t.Skipf("symlink: %v", err)
	}

	rules := writeRules{allow: []string{allowed}}
	escapeTarget := link + "/../escaped.txt"
	if rules.allows(escapeTarget) {
		t.Fatalf("writeRules.allows(%q) = true, want false (symlink + .. escape)", escapeTarget)
	}

	// Verify cliamp.fs.write, append, remove, mkdir reject the traversal when
	// active write rules allow only `allowed`.
	prev := activeWriteRules.Swap(&rules)
	defer activeWriteRules.Store(prev)

	L := newSandboxedState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)
	L.SetGlobal("escape", lua.LString(escapeTarget))

	for _, call := range []string{
		`cliamp.fs.write(escape, "x")`,
		`cliamp.fs.append(escape, "x")`,
		`cliamp.fs.remove(escape)`,
		`cliamp.fs.mkdir(escape)`,
	} {
		err := L.DoString(call)
		if err == nil {
			t.Errorf("%s succeeded across symlink/.. traversal, want error", call)
		}
	}
	if _, err := os.Stat(filepath.Join(outsideParent, "escaped.txt")); err == nil {
		t.Fatal("escaped.txt was created outside allowlist")
	}
}

func TestWriteRulesDenyTelegramSessionAndRadioFiles(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "cliamp")
	data := filepath.Join(home, ".local", "share", "cliamp")
	t.Setenv("HOME", home)
	t.Setenv("CLIAMP_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	rules := loadWriteRules()
	for _, path := range []string{
		filepath.Join(data, "telegram.session"),
		filepath.Join(cfg, "radio_favorites.toml"),
		filepath.Join(cfg, "radio_countries.toml"),
	} {
		if rules.allows(path) {
			t.Errorf("loadWriteRules().allows(%q) = true, want false", path)
		}
	}
}

func TestBlockedExecFlagYTDLPBypasses(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		blocked bool
	}{
		{"--alias", []string{"--alias", "--x", " --exec id", "--x", "https://example.com"}, true},
		{"--netrc-cmd", []string{"--netrc-cmd", "id", "https://example.com"}, true},
		{"--js-runtimes", []string{"--js-runtimes", "/tmp/evil", "https://example.com"}, true},
		{"--ffmpeg-location", []string{"--ffmpeg-location", "/tmp/evil", "https://example.com"}, true},
		{"--downloader", []string{"--downloader", "/tmp/evil", "https://example.com"}, true},
		{"--external-downloader", []string{"--external-downloader", "/tmp/evil", "https://example.com"}, true},
		{"optparse prefix --exec-b", []string{"--exec-b", "id", "https://example.com"}, true},
		{"optparse prefix --config-l", []string{"--config-l", "/tmp/cfg", "https://example.com"}, true},
		{"optparse prefix --plugin-d", []string{"--plugin-d", "/tmp/plugs", "https://example.com"}, true},
		{"optparse prefix --netrc-c", []string{"--netrc-c", "id", "https://example.com"}, true},
		{"optparse prefix --ali", []string{"--ali", "--x", " --exec id"}, true},
		{"safe --netrc allowed", []string{"--netrc", "https://example.com"}, false},
		{"safe --extract-audio allowed", []string{"--extract-audio", "https://example.com"}, false},
		{"safe --all-formats allowed", []string{"--all-formats", "https://example.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blockedExecFlag("yt-dlp", tt.args)
			if (got != "") != tt.blocked {
				t.Fatalf("blockedExecFlag(yt-dlp, %v) = %q, want blocked=%v", tt.args, got, tt.blocked)
			}
		})
	}
}

func TestBoundedStringGsubPreventsOOMAndPreservesSemantics(t *testing.T) {
	L := newSandboxedState()
	defer L.Close()

	// Previously allocated ~1 GB before checking output size.
	err := L.DoString(`return string.gsub(string.rep("a", 10000), "a", string.rep("b", 100000))`)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("huge gsub error = %v, want output exceeds limit error", err)
	}

	// Verify normal string, capture, percent-escape, table, function, and limit behavior.
	if err := L.DoString(`
		local s1, n1 = string.gsub("hello world", "(%w+)", "%1-%1", 1)
		assert(s1 == "hello-hello world" and n1 == 1, "capture+limit failed: " .. tostring(s1))

		local s2, n2 = string.gsub("a b", "%w", "%%")
		assert(s2 == "% %" and n2 == 2, "percent escape failed: " .. tostring(s2))

		local s3, n3 = string.gsub("foo bar", "%w+", {foo = "FOO"})
		assert(s3 == "FOO bar" and n3 == 2, "table repl failed: " .. tostring(s3))

		local s4, n4 = string.gsub("a1 b2", "(%a)(%d)", function(a, d) return d .. a end)
		assert(s4 == "1a 2b" and n4 == 2, "func repl failed: " .. tostring(s4))

		local s5, n5 = string.gsub("abc", "x", "y")
		assert(s5 == "abc" and n5 == 0, "no-match failed: " .. tostring(s5))
	`); err != nil {
		t.Fatalf("gsub semantics failed: %v", err)
	}
}

func TestUISelectCallsOnSelectMinusOneWhenOnCancelOmitted(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	var (
		gotDone   func(int)
		gotCancel func()
	)
	m.SetUIProvider(UIProvider{
		ShowSelect: func(_ string, _ []string, _ int, done func(int), cancel func()) {
			gotDone = done
			gotCancel = cancel
		},
	})
	p := loadTestPlugin(t, m, "sel-cancel", `
		plugin.register({name = "sel-cancel", type = "hook", permissions = {"ui"}})
		local ok, err = cliamp.ui.select({
			title = "Pick",
			options = {"a", "b"},
			on_select = function(i) _G.choice = i end,
		})
		assert(ok, tostring(err))
	`)
	if gotCancel != nil {
		t.Fatal("onCancel callback was non-nil when on_cancel option was omitted")
	}
	gotDone(-1)
	m.Close()

	if got := p.L.GetGlobal("choice"); got != lua.LNumber(-1) {
		t.Fatalf("choice = %v, want -1", got)
	}
}

func TestMessageAndExecTimeoutHandleHugeFloats(t *testing.T) {
	L := newSandboxedState()
	defer L.Close()

	var gotDur time.Duration
	cliamp := L.NewTable()
	registerMessageAPI(L, cliamp, func() *UIProvider {
		return &UIProvider{
			ShowMessage: func(_ string, dur time.Duration) { gotDur = dur },
		}
	})
	L.SetGlobal("cliamp", cliamp)

	for _, expr := range []string{
		`cliamp.message("hi", math.huge)`,
		`cliamp.message("hi", 1e12)`,
	} {
		gotDur = -1
		if err := L.DoString(expr); err != nil {
			t.Fatal(err)
		}
		if gotDur != messageMaxDuration {
			t.Fatalf("%s produced duration %v, want %v", expr, gotDur, messageMaxDuration)
		}
	}
}

func TestEmitKeyNormalizesKeyCase(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	p := loadTestPlugin(t, m, "keys", `
		local p = plugin.register({name = "keys", type = "hook", permissions = {"keymap"}})
		local ok, err = p:bind("Z", function(k) _G.pressed = k end)
		assert(ok, tostring(err))
	`)
	if !m.EmitKey("Z") {
		t.Fatal("EmitKey(\"Z\") returned false, want true")
	}
	m.Close()
	if got := p.L.GetGlobal("pressed"); got != lua.LString("z") {
		t.Fatalf("pressed = %v, want \"z\"", got)
	}
}
