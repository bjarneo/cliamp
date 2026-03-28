package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTerminalTitleDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TerminalTitleFormat != "[%state_icon% ][%metadata% | ]%app%" {
		t.Fatalf("TerminalTitleFormat = %q, want %q", cfg.TerminalTitleFormat, "[%state_icon% ][%metadata% | ]%app%")
	}
	if cfg.TerminalTitleIntro != "It really whips the terminal's ass." {
		t.Fatalf("TerminalTitleIntro = %q, want %q", cfg.TerminalTitleIntro, "It really whips the terminal's ass.")
	}
}

func TestLoadTerminalTitleConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte("terminal_title_format = \"[%state%] %app%\"\nterminal_title_intro = \"hello\"\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TerminalTitleFormat != "[%state%] %app%" {
		t.Fatalf("TerminalTitleFormat = %q, want %q", cfg.TerminalTitleFormat, "[%state%] %app%")
	}
	if cfg.TerminalTitleIntro != "hello" {
		t.Fatalf("TerminalTitleIntro = %q, want %q", cfg.TerminalTitleIntro, "hello")
	}
}

func TestLoadTerminalTitleEmptyIntro(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("terminal_title_intro = \"\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TerminalTitleIntro != "" {
		t.Fatalf("TerminalTitleIntro = %q, want empty", cfg.TerminalTitleIntro)
	}
}
