package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestParseURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		chat    string
		msgID   int
		wantErr bool
	}{
		{name: "bot chat", uri: "telegram:deezload2bot:8046", chat: "deezload2bot", msgID: 8046},
		{name: "negative id", uri: "telegram:-1001234567890:42", chat: "-1001234567890", msgID: 42},
		{name: "missing id", uri: "telegram:deezload2bot", wantErr: true},
		{name: "bad id", uri: "telegram:chat:abc", wantErr: true},
		{name: "wrong scheme", uri: "spotify:track:1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat, msgID, err := parseURI(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseURI(%q) expected error", tt.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseURI(%q) unexpected error: %v", tt.uri, err)
			}
			if chat != tt.chat || msgID != tt.msgID {
				t.Fatalf("parseURI(%q) = (%q, %d), want (%q, %d)", tt.uri, chat, msgID, tt.chat, tt.msgID)
			}
		})
	}
}

func TestAudioInfo(t *testing.T) {
	tests := []struct {
		name      string
		doc       *tg.Document
		wantOK    bool
		wantTitle string
		wantArt   string
	}{
		{
			name: "audio attribute",
			doc: &tg.Document{
				MimeType: "audio/flac",
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeAudio{Title: "Walk", Performer: "Foo Fighters", Duration: 260},
				},
			},
			wantOK:    true,
			wantTitle: "Walk",
			wantArt:   "Foo Fighters",
		},
		{
			name: "filename fallback",
			doc: &tg.Document{
				MimeType: "audio/mpeg",
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: "03 - Learn to Fly.mp3"},
				},
			},
			wantOK:    true,
			wantTitle: "03 - Learn to Fly",
		},
		{
			name: "not audio",
			doc: &tg.Document{
				MimeType: "application/pdf",
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: "notes.pdf"},
				},
			},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, ok := audioInfo(tt.doc)
			if ok != tt.wantOK {
				t.Fatalf("audioInfo() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if meta.title != tt.wantTitle {
				t.Fatalf("audioInfo() title = %q, want %q", meta.title, tt.wantTitle)
			}
			if meta.performer != tt.wantArt {
				t.Fatalf("audioInfo() performer = %q, want %q", meta.performer, tt.wantArt)
			}
		})
	}
}

func TestExtensionFor(t *testing.T) {
	tests := []struct {
		name string
		doc  *tg.Document
		want string
	}{
		{
			name: "filename wins",
			doc: &tg.Document{
				MimeType: "audio/mpeg",
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: "song.FLAC"},
				},
			},
			want: ".flac",
		},
		{
			name: "mime mp3",
			doc:  &tg.Document{MimeType: "audio/mpeg"},
			want: ".mp3",
		},
		{
			name: "mime ogg",
			doc:  &tg.Document{MimeType: "audio/ogg"},
			want: ".ogg",
		},
		{
			name: "fallback flac",
			doc:  &tg.Document{MimeType: "audio/unknown"},
			want: ".flac",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extensionFor(tt.doc); got != tt.want {
				t.Fatalf("extensionFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSafeChat(t *testing.T) {
	tests := map[string]string{
		"deezload2bot":   "deezload2bot",
		"@deezload2bot":  "deezload2bot",
		"-1001234567890": "-1001234567890",
		"a/b:c\\d":       "a_b_c_d",
	}
	for in, want := range tests {
		if got := safeChat(in); got != want {
			t.Fatalf("safeChat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPeerIDRoundTrip(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{id: "u:123456:789012"},
		{id: "ch:-1001234567890:987654"},
		{id: "c:42"},
		{id: "x:1", wantErr: true},
		{id: "u:abc:1", wantErr: true},
		{id: "u:1", wantErr: true},
		{id: "c:notanumber", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			peer, err := parsePeerID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePeerID(%q) expected error", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePeerID(%q) unexpected error: %v", tt.id, err)
			}
			if got := peerID(peer); got != tt.id {
				t.Fatalf("round-trip = %q, want %q", got, tt.id)
			}
		})
	}
}
