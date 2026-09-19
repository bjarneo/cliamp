package luaplugin

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/plugintrust"
)

// loadPomodoro installs the bundled pomodoro plugin into an isolated config
// directory and returns a live Manager for it.
func loadPomodoro(t *testing.T, cfg map[string]string) *Manager {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	dir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("..", "plugins", "pomodoro.lua"))
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	dest := filepath.Join(dir, "pomodoro.lua")
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	if _, err := plugintrust.Approve(dir, "pomodoro", dest); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var all map[string]map[string]string
	if cfg != nil {
		all = map[string]map[string]string{"pomodoro": cfg}
	}
	mgr, err := New(all, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(mgr.Close)
	return mgr
}

func TestPomodoroCommands(t *testing.T) {
	mgr := loadPomodoro(t, nil)

	if out, _ := mgr.EmitCommand("pomodoro", "status", nil); !strings.Contains(out, "off") {
		t.Errorf("status before start = %q, want off", out)
	}
	if out, err := mgr.EmitCommand("pomodoro", "start", nil); err != nil || !strings.Contains(out, "Focus") {
		t.Fatalf("start = %q, %v; want a focus phase", out, err)
	}
	if out, _ := mgr.EmitCommand("pomodoro", "stop", nil); !strings.Contains(out, "stopped") {
		t.Errorf("stop = %q", out)
	}
	if out, _ := mgr.EmitCommand("pomodoro", "status", nil); !strings.Contains(out, "off") {
		t.Errorf("status after stop = %q, want off", out)
	}
}

// The point of the plugin: the break has to actually pause the music.
func TestPomodoroPausesPlaybackOnBreak(t *testing.T) {
	mgr := loadPomodoro(t, map[string]string{"work_minutes": "0.02", "break_minutes": "5"}) // 1.2s of work

	var mu sync.Mutex
	pauses := 0
	mgr.SetStateProvider(StateProvider{PlayerState: func() string { return "playing" }})
	mgr.SetControlProvider(ControlProvider{TogglePause: func() {
		mu.Lock()
		defer mu.Unlock()
		pauses++
	}})

	if _, err := mgr.EmitCommand("pomodoro", "start", nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := pauses
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if pauses == 0 {
		t.Fatal("work phase ended but playback was never paused for the break")
	}
	if out, _ := mgr.EmitCommand("pomodoro", "status", nil); !strings.Contains(out, "Break") {
		t.Errorf("status = %q, want a break phase", out)
	}
}

var pomodoroMinutesLeft = regexp.MustCompile(`(\d+)m \d+s left`)

// waitMinutesLeft polls status until want holds, since key handlers may land
// after EmitKey returns.
func waitMinutesLeft(t *testing.T, mgr *Manager, want int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, _ := mgr.EmitCommand("pomodoro", "status", nil)
		m := pomodoroMinutesLeft.FindStringSubmatch(out)
		if m == nil && time.Now().After(deadline) {
			t.Fatalf("status = %q, want a running phase", out)
		}
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			if n == want || time.Now().After(deadline) {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// H toggles the session; ) and ( move the end of the running phase.
func TestPomodoroKeys(t *testing.T) {
	mgr := loadPomodoro(t, map[string]string{"work_minutes": "25", "adjust_minutes": "5"})

	if !mgr.EmitKey("H") {
		t.Fatal(`EmitKey("H") did not reach the pomodoro`)
	}
	if got := waitMinutesLeft(t, mgr, 24); got != 24 {
		t.Fatalf("after H: %d min left, want 24", got)
	}
	mgr.EmitKey(")")
	if got := waitMinutesLeft(t, mgr, 29); got != 29 {
		t.Fatalf("after ): %d min left, want 29", got)
	}
	mgr.EmitKey("(")
	mgr.EmitKey("(")
	if got := waitMinutesLeft(t, mgr, 19); got != 19 {
		t.Fatalf("after ( twice: %d min left, want 19", got)
	}
	for range 10 {
		mgr.EmitKey("(")
	}
	if got := waitMinutesLeft(t, mgr, 1); got != 1 {
		t.Fatalf("after many (: %d min left, want the 1 min floor", got)
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// The clock replaces the visualizer, so it must fill the panel's exact height
// and stay inside its width once escape codes are discounted.
func TestPomodoroRendersCountdown(t *testing.T) {
	mgr := loadPomodoro(t, nil)

	if names := mgr.Visualizers(); len(names) != 1 || names[0] != "pomodoro" {
		t.Fatalf("Visualizers() = %v, want [pomodoro]", names)
	}

	var bands [10]float64
	const rows, cols = 16, 100
	fits := func(out string) {
		t.Helper()
		lines := strings.Split(out, "\n")
		if len(lines) != rows {
			t.Errorf("render has %d lines, want %d", len(lines), rows)
		}
		for i, line := range lines {
			if n := len([]rune(ansiEscape.ReplaceAllString(line, ""))); n > cols {
				t.Errorf("line %d is %d cells wide, want <= %d", i, n, cols)
			}
		}
	}

	idle := mgr.RenderVis("pomodoro", bands, rows, cols, 0)
	fits(idle)
	if !strings.Contains(idle, "▀") {
		t.Error("expected block digits in the idle render")
	}
	if strings.ContainsRune(idle, 0) {
		t.Error("render contains a NUL byte")
	}

	if _, err := mgr.EmitCommand("pomodoro", "start", nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	running := mgr.RenderVis("pomodoro", bands, rows, cols, 1)
	fits(running)
	if running == idle {
		t.Error("render did not change once the session started")
	}
}
