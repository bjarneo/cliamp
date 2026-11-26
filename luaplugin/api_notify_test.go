package luaplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNotifyPassesEndOfOptionsAndMinimalEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sh for fake notify-send")
	}

	tests := []struct {
		name     string
		title    string
		body     string
		wantArgs []string
	}{
		{"title only", "Now Playing", "", []string{"--", "Now Playing"}},
		{"title and body", "Song", "Artist", []string{"--", "Song", "Artist"}},
		{"flag-like title", "--icon=/etc/passwd", "-u", []string{"--", "--icon=/etc/passwd", "-u"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsOut := filepath.Join(dir, "args.txt")
			envOut := filepath.Join(dir, "env.txt")

			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nenv > %q\n", argsOut, envOut)
			bin := filepath.Join(dir, "notify-send")
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
			t.Setenv("DISPLAY", ":0")
			t.Setenv("WAYLAND_DISPLAY", "wayland-0")
			t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
			t.Setenv("LD_PRELOAD", "/tmp/evil.so")
			t.Setenv("CLIAMP_TEST_SECRET", "supersecret")

			L := newSandboxedState()
			defer L.Close()
			p := &Plugin{Name: "notify-test", installName: "notify-test"}
			cliamp := L.NewTable()
			registerNotifyAPI(L, cliamp, p)
			L.SetGlobal("cliamp", cliamp)

			var call string
			if tt.body == "" {
				call = fmt.Sprintf(`cliamp.notify(%q)`, tt.title)
			} else {
				call = fmt.Sprintf(`cliamp.notify(%q, %q)`, tt.title, tt.body)
			}
			if err := L.DoString(call); err != nil {
				t.Fatal(err)
			}

			gotArgsRaw, err := os.ReadFile(argsOut)
			if err != nil {
				t.Fatalf("read args: %v", err)
			}
			gotArgs := strings.Split(strings.TrimSuffix(string(gotArgsRaw), "\n"), "\n")
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args = %q, want %q", gotArgs, tt.wantArgs)
			}
			for i := range tt.wantArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], tt.wantArgs[i])
				}
			}

			gotEnvRaw, err := os.ReadFile(envOut)
			if err != nil {
				t.Fatalf("read env: %v", err)
			}
			gotEnv := string(gotEnvRaw)
			for _, want := range []string{
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
				"DISPLAY=:0",
				"WAYLAND_DISPLAY=wayland-0",
				"XDG_RUNTIME_DIR=/run/user/1000",
			} {
				if !strings.Contains(gotEnv, want) {
					t.Errorf("notify-send env missing %q", want)
				}
			}
			for _, forbidden := range []string{"LD_PRELOAD=", "CLIAMP_TEST_SECRET="} {
				if strings.Contains(gotEnv, forbidden) {
					t.Errorf("notify-send env leaked %q", forbidden)
				}
			}
		})
	}
}
