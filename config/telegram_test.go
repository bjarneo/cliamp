package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadTelegramChats(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	header := `
[telegram]
api_id = 123456
api_hash = "0123456789abcdef0123456789abcdef"
phone = "+5491100000000"
`

	// A list of chats to show instead of every dialog.
	write(header + `chats = ["deezload2bot", "@mygroup"]
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"deezload2bot", "@mygroup"}; !slices.Equal(cfg.Telegram.Chats, want) {
		t.Fatalf("chats = %q, want %q", cfg.Telegram.Chats, want)
	}

	// The single chat key from the first version still works.
	write(header + `chat = "deezload2bot"
`)
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"deezload2bot"}; !slices.Equal(cfg.Telegram.Chats, want) {
		t.Fatalf("chat = %q, want %q", cfg.Telegram.Chats, want)
	}

	// Empty chats mean every dialog.
	write(header)
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Telegram.Chats) != 0 {
		t.Fatalf("chats = %q, want none", cfg.Telegram.Chats)
	}
}
