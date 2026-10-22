package telegram

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

func TestUsernameOf(t *testing.T) {
	tests := map[string]string{
		"deezload2bot":         "deezload2bot",
		"@deezload2bot":        "deezload2bot",
		" https://t.me/durov ": "durov",
		"t.me/durov":           "durov",
		"@":                    "",
	}
	for in, want := range tests {
		if got := usernameOf(in); got != want {
			t.Errorf("usernameOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPeerKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"u:123:456", "u:123", true},
		{"ch:-100:99", "ch:-100", true},
		{"c:42", "c:42", true},
		{"c:42:9", "", false},
		{"u:123", "", false},
		{"u:x:1", "", false},
		{"deezload2bot", "", false},
	}
	for _, tt := range tests {
		_, got, ok := peerKey(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("peerKey(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSetChatsPersists(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const hash = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`
[telegram]
api_id = 123456
api_hash = "`+hash+`"
phone = "+5491100000000"
buffer_mb = 4
`), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Provider{cfg: config.TelegramConfig{AppID: 123456, AppHash: hash, Phone: "+5491100000000", BufferMB: 4}}
	want := []string{"u:5749294212:3552096433505666955", "c:1234"}
	if err := p.SetChats(want); err != nil {
		t.Fatal(err)
	}
	if got := p.Chats(); !slices.Equal(got, want) {
		t.Fatalf("Chats() = %q, want %q", got, want)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Telegram.Chats, want) {
		t.Fatalf("saved chats = %q, want %q", cfg.Telegram.Chats, want)
	}
	if cfg.Telegram.BufferMB != 4 || cfg.Telegram.AppID != 123456 {
		t.Fatalf("other keys changed: %+v", cfg.Telegram)
	}

	// An empty filter removes the key, which means every dialog.
	if err := p.SetChats(nil); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Telegram.Chats) != 0 {
		t.Fatalf("chats = %q, want none", cfg.Telegram.Chats)
	}
}
