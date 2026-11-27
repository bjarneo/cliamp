package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/gotd/td/telegram/auth"
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

func TestAuthenticateForwardsCode(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr error
	}{
		{name: "valid numeric code", code: "12345"},
		{name: "different code with rejection", code: "98765", wantErr: errors.New("invalid code")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &Provider{
				ctx:     ctx,
				cancel:  cancel,
				authReq: make(chan authRequest),
			}
			p.setNeedsAuth()

			gotCode := make(chan string, 1)
			go func() {
				req := <-p.authReq
				gotCode <- req.code
				req.result <- tt.wantErr
			}()

			err := p.Authenticate(tt.code)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate(%q) error = %v, want %v", tt.code, err, tt.wantErr)
			}
			if got := <-gotCode; got != tt.code {
				t.Fatalf("received code = %q, want %q", got, tt.code)
			}
		})
	}
}

func TestWaitConcurrentStateTransition(t *testing.T) {
	p := &Provider{authReq: make(chan authRequest)}
	wantErr := errors.New("connect failed")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			p.setFailed(wantErr)
			p.setNeedsAuth()
			p.setReady()
		}
		p.setFailed(wantErr)
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			_ = p.wait()
		}
	}()
	wg.Wait()

	if err := p.wait(); !errors.Is(err, wantErr) {
		t.Fatalf("wait() = %v, want %v", err, wantErr)
	}
}

func TestCloseCancelsContextAndWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Provider{
		ctx:     ctx,
		cancel:  cancel,
		authReq: make(chan authRequest),
	}
	var closer provider.Closer = p

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- p.wait()
	}()

	closer.Close()
	closer.Close()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("Close did not cancel provider context")
	}
	if err := <-waitErr; err == nil {
		t.Fatal("wait() should return an error after Close")
	}
}

func TestNewFromConfigDefersStartUntilFirstAccess(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "nested", "session")
	cfg := config.TelegramConfig{
		AppID:       123456,
		AppHash:     "0123456789abcdef0123456789abcdef",
		SessionFile: filepath.Join(sessionDir, "telegram.session"),
	}

	// NewFromConfig must not create the session directory or connect during startup.
	unused := NewFromConfig(cfg)
	if unused == nil {
		t.Fatal("NewFromConfig() returned nil for valid config")
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("NewFromConfig() created session dir eagerly: %v", err)
	}
	unused.Close()
	unused.Close()

	tests := []struct {
		name   string
		invoke func(p *Provider)
	}{
		{
			name:   "wait",
			invoke: func(p *Provider) { _ = p.wait() },
		},
		{
			name:   "Playlists",
			invoke: func(p *Provider) { _, _ = p.Playlists() },
		},
		{
			name:   "Dialogs",
			invoke: func(p *Provider) { _, _ = p.Dialogs() },
		},
		{
			name:   "TracksPage",
			invoke: func(p *Provider) { _, _, _ = p.TracksPage("c:1", 0) },
		},
		{
			name:   "Authenticate",
			invoke: func(p *Provider) { _ = p.Authenticate("12345") },
		},
		{
			name: "SetChats",
			invoke: func(p *Provider) {
				_ = p.SetChats([]string{"c:1"})
			},
		},
		{
			name:   "NewStreamer",
			invoke: func(p *Provider) { _, _, _, _ = p.NewStreamer("telegram:c:1:10") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			p := NewFromConfig(cfg)
			t.Cleanup(p.Close)

			var starts atomic.Int32
			started := make(chan struct{})
			p.runFn = func() {
				if starts.Add(1) == 1 {
					close(started)
				}
				p.setFailed(errors.New("stubbed"))
			}

			if starts.Load() != 0 {
				t.Fatal("background runner started before first method call")
			}

			tt.invoke(p)
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s did not trigger lazy start", tt.name)
			}

			// Repeated access must not start the runner a second time.
			_ = p.wait()
			if got := starts.Load(); got != 1 {
				t.Fatalf("start count = %d, want 1", got)
			}
		})
	}
}

func TestCloseBeforeLazyStartPreventsRun(t *testing.T) {
	cfg := config.TelegramConfig{
		AppID:       123456,
		AppHash:     "0123456789abcdef0123456789abcdef",
		SessionFile: filepath.Join(t.TempDir(), "telegram.session"),
	}
	p := NewFromConfig(cfg)

	var starts atomic.Int32
	p.runFn = func() {
		starts.Add(1)
	}

	p.Close()
	p.Close()

	if err := p.wait(); err == nil {
		t.Fatal("wait() after Close() succeeded, want error")
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("runner started %d times after Close(), want 0", got)
	}
}

func TestEnsureStartedConcurrent(t *testing.T) {
	cfg := config.TelegramConfig{
		AppID:       123456,
		AppHash:     "0123456789abcdef0123456789abcdef",
		SessionFile: filepath.Join(t.TempDir(), "telegram.session"),
	}
	p := NewFromConfig(cfg)
	t.Cleanup(p.Close)

	var starts atomic.Int32
	p.runFn = func() {
		starts.Add(1)
		p.setFailed(errors.New("stubbed"))
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.wait()
		}()
	}
	wg.Wait()

	if got := starts.Load(); got != 1 {
		t.Fatalf("concurrent start count = %d, want 1", got)
	}
}

type fakeTGAuthClient struct {
	mu           sync.Mutex
	sendCalls    int
	signInHashes []string
	signInCodes  []string
	needPassword bool
	gotPassword  string
}

func (f *fakeTGAuthClient) SendCode(_ context.Context, _ string, _ auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls++
	return &tg.AuthSentCode{PhoneCodeHash: "hash-1"}, nil
}

func (f *fakeTGAuthClient) SignIn(_ context.Context, _, code, codeHash string) (*tg.AuthAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signInCodes = append(f.signInCodes, code)
	f.signInHashes = append(f.signInHashes, codeHash)
	if code != "54321" {
		return nil, errors.New("PHONE_CODE_INVALID")
	}
	if f.needPassword {
		return nil, auth.ErrPasswordAuthNeeded
	}
	return &tg.AuthAuthorization{}, nil
}

func (f *fakeTGAuthClient) Password(_ context.Context, password string) (*tg.AuthAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotPassword = password
	return &tg.AuthAuthorization{}, nil
}

func TestAwaitSignInSendsCodeOnceAndMarksReadyBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Provider{
		cfg:     config.TelegramConfig{Phone: "+15551234567", Password: "secret"},
		ctx:     ctx,
		cancel:  cancel,
		authReq: make(chan authRequest),
	}
	fake := &fakeTGAuthClient{needPassword: true}
	var readySet atomic.Bool

	go func() {
		_ = p.awaitSignIn(ctx, fake, func() { readySet.Store(true) })
	}()

	// Wait until SendCode has run and provider transitioned to provNeedsAuth.
	if err := p.wait(); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("initial wait() = %v, want ErrNeedsAuth", err)
	}
	// Wrong code must fail without calling SendCode again.
	if err := p.Authenticate("00000"); err == nil {
		t.Fatal("Authenticate(wrong code) = nil, want error")
	}
	// Correct code must succeed with the same hash and mark ready BEFORE returning.
	if err := p.Authenticate("54321"); err != nil {
		t.Fatalf("Authenticate(54321) = %v, want nil", err)
	}
	if !readySet.Load() {
		t.Fatal("onReady did not run before Authenticate returned")
	}
	if err := p.wait(); err != nil {
		t.Fatalf("wait() right after Authenticate = %v, want nil (ready)", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.sendCalls != 1 {
		t.Fatalf("SendCode calls = %d, want 1", fake.sendCalls)
	}
	if fake.gotPassword != "secret" {
		t.Fatalf("2FA password = %q, want secret", fake.gotPassword)
	}
	if len(fake.signInHashes) != 2 || fake.signInHashes[0] != "hash-1" || fake.signInHashes[1] != "hash-1" {
		t.Fatalf("SignIn hashes = %v, want [hash-1 hash-1]", fake.signInHashes)
	}
}

func TestURISchemesIncludesTrailingColonAndSafeChatBlocksDotDot(t *testing.T) {
	p := &Provider{}
	schemes := p.URISchemes()
	if len(schemes) != 1 || schemes[0] != "telegram:" {
		t.Fatalf("URISchemes() = %v, want [telegram:]", schemes)
	}
	for _, bad := range []string{"", ".", ".."} {
		if got := safeChat(bad); got == "" || got == "." || got == ".." {
			t.Fatalf("safeChat(%q) = %q, want safe directory name", bad, got)
		}
	}
}

func TestRefreshRestartsAfterTransientFailure(t *testing.T) {
	cfg := config.TelegramConfig{
		AppID:       123456,
		AppHash:     "0123456789abcdef0123456789abcdef",
		SessionFile: filepath.Join(t.TempDir(), "telegram.session"),
	}
	p := NewFromConfig(cfg)
	t.Cleanup(p.Close)

	var starts atomic.Int32
	done := make(chan struct{}, 2)
	p.runFn = func() {
		if starts.Add(1) == 1 {
			p.setFailed(errors.New("transient dns error"))
		} else {
			p.setReady()
		}
		done <- struct{}{}
	}

	if err := p.wait(); err == nil {
		t.Fatal("first wait() = nil, want transient error")
	}
	<-done
	p.Refresh()
	if err := p.wait(); err != nil {
		t.Fatalf("wait() after Refresh() = %v, want nil", err)
	}
	<-done
	if got := starts.Load(); got != 2 {
		t.Fatalf("starts = %d, want 2", got)
	}
}
