package telegram

import (
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
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
			name: "mime opus",
			doc:  &tg.Document{MimeType: "audio/opus"},
			want: ".opus",
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

func TestWaitReflectsState(t *testing.T) {
	p := &Provider{authReq: make(chan authRequest)}

	p.setReady()
	if err := p.wait(); err != nil {
		t.Fatalf("ready wait: %v", err)
	}

	p.setNeedsAuth()
	if err := p.wait(); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("needs auth wait = %v, want ErrNeedsAuth", err)
	}

	p.setFailed(errors.New("boom"))
	err := p.wait()
	if err == nil || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("failed wait = %v, want the connect error", err)
	}
}

func TestAuthenticateRejectsWhenUnusable(t *testing.T) {
	p := &Provider{authReq: make(chan authRequest)}
	p.setFailed(errors.New("boom"))
	if err := p.Authenticate("12345"); err == nil {
		t.Fatal("Authenticate should fail before sending when the client is gone")
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

func TestDialogsAsPlaylistsUsesTheDialogList(t *testing.T) {
	user := &tg.User{ID: 10, AccessHash: 99, FirstName: "Ada"}
	orphan := &tg.User{ID: 30, AccessHash: 55, FirstName: "OnlyInTopMessage"}
	channel := &tg.Channel{ID: 20, AccessHash: 77, Title: "Music"}

	got := dialogsAsPlaylists(&tg.MessagesDialogsSlice{
		Dialogs: []tg.DialogClass{
			&tg.Dialog{Peer: &tg.PeerUser{UserID: 10}},
			&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 20}},
		},
		Users: []tg.UserClass{user, orphan},
		Chats: []tg.ChatClass{channel},
	})

	if len(got) != 2 {
		t.Fatalf("dialogsAsPlaylists() returned %d playlists, want 2: %+v", len(got), got)
	}
	if got[0].ID != "u:10:99" || got[0].Section != "Chats" {
		t.Fatalf("user dialog = %+v, want u:10:99 in Chats", got[0])
	}
	if got[1].ID != "ch:20:77" || got[1].Section != "Channels" || got[1].Name != "Music" {
		t.Fatalf("channel dialog = %+v, want ch:20:77 Music in Channels", got[1])
	}
}

func TestTopMessageDateMatchesThePeer(t *testing.T) {
	// Message IDs repeat across peers, so the offset date has to come from the
	// message of the dialog's own peer.
	messages := []tg.MessageClass{
		&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 10}, Date: 111},
		&tg.Message{ID: 7, PeerID: &tg.PeerChannel{ChannelID: 20}, Date: 222},
		&tg.MessageService{ID: 9, PeerID: &tg.PeerChannel{ChannelID: 20}, Date: 333},
	}
	if got := topMessageDate(messages, &tg.PeerChannel{ChannelID: 20}, 7); got != 222 {
		t.Fatalf("topMessageDate() = %d, want 222", got)
	}
	// A dialog whose top message is a service event (a join, a pin) still has
	// to supply the offset date; reading only regular messages left it at zero.
	if got := topMessageDate(messages, &tg.PeerChannel{ChannelID: 20}, 9); got != 333 {
		t.Fatalf("topMessageDate() = %d for a service message, want 333", got)
	}
	if got := topMessageDate(messages, &tg.PeerChat{ChatID: 3}, 7); got != 0 {
		t.Fatalf("topMessageDate() = %d, want 0 when no message matches the peer", got)
	}
}
