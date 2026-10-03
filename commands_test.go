package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	cli "github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/theme"
)

func TestInverseBoolFlags(t *testing.T) {
	tests := []struct {
		flag string
		get  func(config.Overrides) *bool
		want bool
	}{
		{"--shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, true},
		{"--no-shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, false},
		{"--mono", func(ov config.Overrides) *bool { return ov.Mono }, true},
		{"--no-mono", func(ov config.Overrides) *bool { return ov.Mono }, false},
		{"--auto-play", func(ov config.Overrides) *bool { return ov.Play }, true},
		{"--no-auto-play", func(ov config.Overrides) *bool { return ov.Play }, false},
		{"--simplified", func(ov config.Overrides) *bool { return ov.Simplified }, true},
		{"--no-simplified", func(ov config.Overrides) *bool { return ov.Simplified }, false},
		{"--help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, false},
		{"--no-help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, true},
		{"--expanded", func(ov config.Overrides) *bool { return ov.Expanded }, true},
		{"--no-expanded", func(ov config.Overrides) *bool { return ov.Expanded }, false},
		{"--expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, true},
		{"--no-expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, false},
		{"--low-power", func(ov config.Overrides) *bool { return ov.LowPower }, true},
		{"--no-low-power", func(ov config.Overrides) *bool { return ov.LowPower }, false},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			app := buildApp()
			var got config.Overrides
			app.Action = func(_ context.Context, c *cli.Command) error {
				var err error
				got, err = overridesFromFlags(c)
				return err
			}

			if err := app.Run(context.Background(), []string{"cliamp", tt.flag}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			value := tt.get(got)
			if value == nil || *value != tt.want {
				t.Errorf("value = %v, want %t", value, tt.want)
			}
		})
	}
}

// The help of each --[no-] flag shows the default that docs/cli.md names.
// --expand-playlist shows the default of the resolve package.
func TestInverseBoolFlagDefaults(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("docs", "cli.md"))
	if err != nil {
		t.Fatalf("read docs/cli.md: %v", err)
	}
	code := map[string]bool{"expand-playlist": resolve.ExpandYTPlaylist}
	for _, f := range buildApp().Flags {
		bf, ok := f.(*cli.BoolWithInverseFlag)
		if !ok {
			continue
		}
		t.Run(bf.Name, func(t *testing.T) {
			name := regexp.QuoteMeta(bf.Name)
			row := regexp.MustCompile("(?m)^\\| `--" + name + "` / `--no-" + name + "` \\| bool \\| (true|false) \\|")
			m := row.FindStringSubmatch(string(data))
			if m == nil {
				t.Fatalf("docs/cli.md has no row for --%s", bf.Name)
			}
			if got := strconv.FormatBool(bf.Value); got != m[1] {
				t.Errorf("--%s help shows default %s, docs/cli.md says %s", bf.Name, got, m[1])
			}
			if want, ok := code[bf.Name]; ok && bf.Value != want {
				t.Errorf("--%s help shows default %t, the code uses %t", bf.Name, bf.Value, want)
			}
		})
	}
}

// Every key and alias in providerKeys parses through --provider in any case,
// and the flag help names each one. An unknown value fails with a message
// that names every key. A key missing from the table would make
// --provider and the provider config key fail for a provider that works.
func TestProviderFlag(t *testing.T) {
	parse := func(t *testing.T, value string) (config.Overrides, error) {
		t.Helper()
		app := buildApp()
		var got config.Overrides
		var flagErr error
		app.Action = func(_ context.Context, c *cli.Command) error {
			got, flagErr = overridesFromFlags(c)
			return nil
		}
		if err := app.Run(t.Context(), []string{"cliamp", "--provider", value}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return got, flagErr
	}

	type flagCase struct{ value, want string }
	var cases []flagCase
	for _, pk := range providerKeys {
		cases = append(cases, flagCase{pk.key, pk.key}, flagCase{strings.ToUpper(pk.key), pk.key})
		if pk.alias != "" {
			cases = append(cases, flagCase{pk.alias, pk.key})
		}
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parse(t, tc.value)
			if err != nil {
				t.Fatalf("--provider %s rejected: %v", tc.value, err)
			}
			if got.Provider == nil || *got.Provider != tc.want {
				t.Fatalf("provider = %v, want %s", got.Provider, tc.want)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		_, err := parse(t, "winamp")
		if err == nil || !strings.HasSuffix(err.Error(), `(got "winamp")`) {
			t.Fatalf("error = %v, want a --provider error", err)
		}
		for _, pk := range providerKeys {
			if !strings.Contains(err.Error(), pk.key) {
				t.Errorf("error %q does not name %s", err, pk.key)
			}
		}
	})

	t.Run("help", func(t *testing.T) {
		var usage string
		for _, f := range buildApp().Flags {
			if sf, ok := f.(*cli.StringFlag); ok && sf.Name == "provider" {
				usage = sf.Usage
			}
		}
		for _, pk := range providerKeys {
			for _, value := range []string{pk.key, pk.alias} {
				if value != "" && !slices.Contains(strings.Split(strings.TrimPrefix(usage, "default provider: "), ", "), value) {
					t.Errorf("--provider help %q does not name %s", usage, value)
				}
			}
		}
	})
}

func TestRadioCommandFlags(t *testing.T) {
	app := buildApp()
	radioCmd := app.Command("radio")
	if radioCmd == nil {
		t.Fatal("radio command not registered")
	}
	for _, name := range []string{"stats", "globe", "json"} {
		if !slices.ContainsFunc(radioCmd.Flags, func(f cli.Flag) bool { return slices.Contains(f.Names(), name) }) {
			t.Errorf("radio command lacks --%s", name)
		}
	}
	// --globe --json is contradictory and must fail before any network call.
	err := app.Run(context.Background(), []string{"cliamp", "radio", "--globe", "--json"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("--globe --json error = %v", err)
	}
}

// The bookmark commands stay as aliases of the favorite commands so old
// scripts keep working.
func TestPlaylistBookmarkAliases(t *testing.T) {
	playlistCmd := buildApp().Command("playlist")
	if playlistCmd == nil {
		t.Fatal("playlist command not registered")
	}
	for _, tt := range []struct{ alias, name string }{
		{alias: "bookmark", name: "favorite"},
		{alias: "bookmarks", name: "favorites"},
	} {
		t.Run(tt.alias, func(t *testing.T) {
			got := playlistCmd.Command(tt.alias)
			if got == nil || got.Name != tt.name {
				t.Fatalf("playlist %s = %v, want the %s command", tt.alias, got, tt.name)
			}
		})
	}
}

// The ipc package returns a bare sentinel; the CLI wording is added here.
func TestUserIPCErrorRendersNotRunning(t *testing.T) {
	rendered := userIPCError(fmt.Errorf("dial: %w", ipc.ErrNotRunning))
	want := fmt.Sprintf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	if rendered.Error() != want {
		t.Errorf("rendered = %q, want %q", rendered.Error(), want)
	}

	other := errors.New("connect: permission denied")
	if got := userIPCError(other); got != other {
		t.Errorf("unrelated error rewritten to %v", got)
	}
}

// captureOutput runs fn with stdout and stderr sent to pipes and returns
// what fn wrote to each.
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	read := func(f **os.File) func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		orig := *f
		*f = w
		done := make(chan string)
		go func() {
			data, _ := io.ReadAll(r)
			done <- string(data)
		}()
		return func() string {
			*f = orig
			_ = w.Close()
			return <-done
		}
	}
	stopOut := read(&os.Stdout)
	stopErr := read(&os.Stderr)
	fn()
	return stopOut(), stopErr()
}

// cliamp theme list works with no running cliamp. It lists the terminal
// colors first, as the IPC theme list does, and names each theme file that
// it skips on stderr.
func TestThemeListCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", "broken.toml"), []byte(`accent = "blue"`), 0o644); err != nil {
		t.Fatal(err)
	}

	var runErr error
	stdout, stderr := captureOutput(t, func() {
		runErr = buildApp().Run(t.Context(), []string{"cliamp", "theme", "list"})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "  "+theme.DefaultName {
		t.Fatalf("theme list = %q, want %q first", lines, theme.DefaultName)
	}
	if !strings.Contains(stderr, "skip broken.toml") {
		t.Errorf("stderr = %q, want the skipped broken.toml", stderr)
	}
}

// cliamp shuffle and cliamp mono toggle by default and pass on, off or
// toggle in lower case.
func TestSwitchCommands(t *testing.T) {
	var got []string
	var mu sync.Mutex
	startTestIPC(t, ipc.RuntimeSnapshot{}, func(jobs *ipc.JobStore, id string, request ipc.V2Request) {
		var params ipc.Request
		_ = json.Unmarshal(request.Params, &params)
		mu.Lock()
		got = append(got, request.Operation+" "+params.Name)
		mu.Unlock()
		_, _ = jobs.Start(id)
		_ = jobs.Succeed(id, json.RawMessage(`{"ok":true,"shuffle":true,"mono":false}`))
	})

	for _, args := range [][]string{{"shuffle"}, {"shuffle", "OFF"}, {"mono"}, {"mono", "on"}} {
		stdout, _ := captureOutput(t, func() {
			if err := buildApp().Run(t.Context(), append([]string{"cliamp"}, args...)); err != nil {
				t.Errorf("cliamp %v: %v", args, err)
			}
		})
		want := "Shuffle: on\n"
		if args[0] == "mono" {
			want = "Mono: off\n"
		}
		if stdout != want {
			t.Errorf("cliamp %v printed %q, want %q", args, stdout, want)
		}
	}
	want := []string{"shuffle toggle", "shuffle off", "mono toggle", "mono on"}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, want) {
		t.Fatalf("operations = %q, want %q", got, want)
	}
}

// TestVersionFlag checks that --version works with and without the
// version that -ldflags sets. go install and go build set none.
func TestVersionFlag(t *testing.T) {
	tests := []struct {
		name    string
		ldflags string
		arg     string
	}{
		{"release long", "v1.2.3", "--version"},
		{"release short", "v1.2.3", "-v"},
		{"plain build long", "", "--version"},
		{"plain build short", "", "-v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := version
			version = tt.ldflags
			t.Cleanup(func() { version = orig })

			want := buildVersion()
			if want == "" {
				t.Fatal("buildVersion() is empty")
			}
			if tt.ldflags != "" && want != tt.ldflags {
				t.Fatalf("buildVersion() = %q, want %q", want, tt.ldflags)
			}
			var out strings.Builder
			app := buildApp()
			app.Writer = &out
			if err := app.Run(t.Context(), []string{"cliamp", tt.arg}); err != nil {
				t.Fatalf("Run(%s) error = %v", tt.arg, err)
			}
			if got := out.String(); got != "cliamp version "+want+"\n" {
				t.Errorf("Run(%s) printed %q, want version %q", tt.arg, got, want)
			}
		})
	}
}

// cliamp queue sends a local file with an absolute path, because the
// running cliamp has its own working directory. A URL or another URI goes
// as it is. A missing file or a directory is an error and sends nothing.
func TestQueueCommandPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "album"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	sent := make(chan string, 16)
	startTestIPC(t, ipc.RuntimeSnapshot{}, func(jobs *ipc.JobStore, id string, request ipc.V2Request) {
		var params ipc.Request
		_ = json.Unmarshal(request.Params, &params)
		sent <- params.Path
		_, _ = jobs.Start(id)
		_ = jobs.Succeed(id, json.RawMessage(`{"ok":true}`))
	})

	for _, tt := range []struct {
		name    string
		arg     string
		want    string
		wantErr string
	}{
		{name: "relative file", arg: "./song.mp3", want: filepath.Join(dir, "song.mp3")},
		{name: "bare file name", arg: "song.mp3", want: filepath.Join(dir, "song.mp3")},
		{name: "absolute file", arg: filepath.Join(dir, "song.mp3"), want: filepath.Join(dir, "song.mp3")},
		{name: "URL", arg: "https://example.com/a.mp3", want: "https://example.com/a.mp3"},
		{name: "search", arg: "ytsearch:aphex twin", want: "ytsearch:aphex twin"},
		{name: "SSH", arg: "ssh://host/music/a.flac", want: "ssh://host/music/a.flac"},
		{name: "provider URI", arg: "spotify:track:abc", want: "spotify:track:abc"},
		{name: "missing file", arg: "nope.mp3", wantErr: "nope.mp3"},
		{name: "directory", arg: "album", wantErr: "is a directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := buildApp().Run(t.Context(), []string{"cliamp", "queue", tt.arg})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("cliamp queue %s error = %v, want %q", tt.arg, err, tt.wantErr)
				}
				select {
				case got := <-sent:
					t.Fatalf("cliamp queue %s sent %q, want nothing", tt.arg, got)
				default:
				}
				return
			}
			if err != nil {
				t.Fatalf("cliamp queue %s: %v", tt.arg, err)
			}
			if got := <-sent; got != tt.want {
				t.Fatalf("cliamp queue %s sent %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

// cliamp status --json always prints position, volume and index, because
// 0 is a real value of each. Other fields with no value stay out.
func TestStatusJSONKeepsZeroValues(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot ipc.RuntimeSnapshot
		want     map[string]any
	}{
		{
			name:     "zero values",
			snapshot: ipc.RuntimeSnapshot{State: "playing", Total: 3},
			want:     map[string]any{"ok": true, "state": "playing", "total": 3.0, "position": 0.0, "volume": 0.0, "index": 0.0},
		},
		{
			name:     "set values",
			snapshot: ipc.RuntimeSnapshot{State: "paused", Position: 12.5, Volume: -6, Index: 2, Total: 3},
			want:     map[string]any{"ok": true, "state": "paused", "total": 3.0, "position": 12.5, "volume": -6.0, "index": 2.0},
		},
		{
			name:     "empty playlist",
			snapshot: ipc.RuntimeSnapshot{State: "stopped", Index: -1},
			want:     map[string]any{"ok": true, "state": "stopped", "position": 0.0, "volume": 0.0, "index": -1.0},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			startTestIPC(t, tt.snapshot, func(*ipc.JobStore, string, ipc.V2Request) {})
			var runErr error
			stdout, _ := captureOutput(t, func() {
				runErr = buildApp().Run(t.Context(), []string{"cliamp", "status", "--json"})
			})
			if runErr != nil {
				t.Fatal(runErr)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("status --json printed %q: %v", stdout, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("status --json = %v, want %v", got, tt.want)
			}
		})
	}
}
