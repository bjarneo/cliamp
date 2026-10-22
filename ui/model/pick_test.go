package model

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// chatFilterProvider is a provider whose list can be narrowed to some ids.
type chatFilterProvider struct {
	dialogs []playlist.PlaylistInfo
	chats   []string
	saved   []string
}

func (p *chatFilterProvider) Name() string { return "Telegram" }

func (p *chatFilterProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if len(p.chats) == 0 {
		return p.dialogs, nil
	}
	out := make([]playlist.PlaylistInfo, 0, len(p.chats))
	for _, dialog := range p.dialogs {
		if slices.Contains(p.chats, dialog.ID) {
			out = append(out, dialog)
		}
	}
	return out, nil
}

func (p *chatFilterProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }

func (p *chatFilterProvider) Dialogs() ([]playlist.PlaylistInfo, error) { return p.dialogs, nil }

func (p *chatFilterProvider) Chats() []string { return p.chats }

func (p *chatFilterProvider) SetChats(chats []string) error {
	p.chats = append([]string(nil), chats...)
	p.saved = append([]string(nil), chats...)
	return nil
}

func TestChatPickerMarksAndApplies(t *testing.T) {
	p := &chatFilterProvider{
		dialogs: []playlist.PlaylistInfo{
			{ID: "u:1:1", Name: "A"},
			{ID: "u:2:2", Name: "B"},
			{ID: "c:3", Name: "C"},
		},
		chats: []string{"u:2:2"},
	}
	m := &Model{
		provider: p,
		playlist: playlist.New(),
		provPane: providerPane{lists: []playlist.PlaylistInfo{{ID: "u:2:2", Name: "B"}}},
	}

	cmd := m.startChatPicker()
	if !m.provPane.picking || !m.provPane.pickLoad || cmd == nil {
		t.Fatalf("picker did not open: %+v", m.provPane)
	}
	m.handleChatPickerLoaded(cmd().(providerPickLoadedMsg))
	if len(m.provPane.lists) != 3 {
		t.Fatalf("rows = %d, want every dialog", len(m.provPane.lists))
	}
	if !m.provPane.picked["u:2:2"] || m.provPane.picked["u:1:1"] {
		t.Fatalf("picked = %v, want only the current filter", m.provPane.picked)
	}

	m.provPane.cursor = 0
	m.handleChatPickerKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if !m.provPane.picked["u:1:1"] {
		t.Fatal("space did not mark the row under the cursor")
	}

	m.handleChatPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.provPane.picking {
		t.Fatal("enter should close the picker")
	}
	if want := []string{"u:1:1", "u:2:2"}; !slices.Equal(p.saved, want) {
		t.Fatalf("saved = %v, want %v", p.saved, want)
	}
	if len(m.provPane.lists) != 2 || m.provPane.lists[0].ID != "u:1:1" {
		t.Fatalf("rows after apply = %+v", m.provPane.lists)
	}
}

func TestChatPickerCancelRestoresRows(t *testing.T) {
	p := &chatFilterProvider{dialogs: []playlist.PlaylistInfo{{ID: "u:1:1", Name: "A"}}}
	rows := []playlist.PlaylistInfo{{ID: "u:1:1", Name: "A"}}
	m := &Model{provider: p, playlist: playlist.New(), provPane: providerPane{lists: rows, cursor: 0}}

	cmd := m.startChatPicker()
	m.handleChatPickerLoaded(cmd().(providerPickLoadedMsg))
	m.handleChatPickerKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.provPane.picking {
		t.Fatal("esc should close the picker")
	}
	if len(m.provPane.lists) != 1 || m.provPane.lists[0].ID != "u:1:1" {
		t.Fatalf("rows = %+v, want the original list", m.provPane.lists)
	}
	if p.saved != nil {
		t.Fatalf("cancel should not save: %v", p.saved)
	}
}

func TestChatPickerAllMeansNoFilter(t *testing.T) {
	p := &chatFilterProvider{
		dialogs: []playlist.PlaylistInfo{{ID: "u:1:1", Name: "A"}, {ID: "u:2:2", Name: "B"}},
	}
	m := &Model{provider: p, playlist: playlist.New(), provPane: providerPane{}}

	cmd := m.startChatPicker()
	m.handleChatPickerLoaded(cmd().(providerPickLoadedMsg))
	m.handleChatPickerKey(tea.KeyPressMsg{Text: "a"})
	m.handleChatPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(p.saved) != 0 {
		t.Fatalf("saved = %v, want no filter for every chat", p.saved)
	}
	if len(m.provPane.lists) != 2 {
		t.Fatalf("rows = %d, want every chat", len(m.provPane.lists))
	}
}

// codeAuthStub signs in with a code the pane types.
type codeAuthStub struct {
	prompt  string
	saved   []string
	dialogs []playlist.PlaylistInfo
}

func (p *codeAuthStub) Name() string { return "Telegram" }
func (p *codeAuthStub) Playlists() ([]playlist.PlaylistInfo, error) {
	return p.dialogs, nil
}
func (p *codeAuthStub) Tracks(string) ([]playlist.Track, error) { return nil, nil }
func (p *codeAuthStub) Prompt() string                          { return p.prompt }
func (p *codeAuthStub) Authenticate(code string) error {
	p.saved = append(p.saved, code)
	return nil
}

func TestAuthCodeInputSendsTheCode(t *testing.T) {
	p := &codeAuthStub{prompt: "The code was sent to your app"}
	m := &Model{provider: p, playlist: playlist.New()}

	m.provPane.signIn = true
	m.handleProviderPaneKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.authCode.active {
		t.Fatal("enter on the sign-in prompt should open the code input")
	}
	if m.authCode.hint != p.prompt {
		t.Fatalf("hint = %q, want the provider prompt", m.authCode.hint)
	}

	m.handleAuthCodeKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.authCode.active || m.authCode.err == "" {
		t.Fatal("empty code should keep the input with an error")
	}

	for _, r := range []string{"4", "2", "4", "2", "4", "2"} {
		m.handleAuthCodeKey(tea.KeyPressMsg{Text: r})
	}
	cmd := m.handleAuthCodeKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return the authentication command")
	}
	if msg := cmd().(provAuthDoneMsg); msg.err != nil {
		t.Fatalf("authenticate: %v", msg.err)
	}

	if !slices.Equal(p.saved, []string{"424242"}) {
		t.Fatalf("saved codes = %v, want once with 424242", p.saved)
	}
	if m.authCode.active {
		t.Fatal("enter should close the input")
	}
}

func TestAuthCodeInputEscCancels(t *testing.T) {
	p := &codeAuthStub{}
	m := &Model{provider: p, playlist: playlist.New()}
	m.provPane.signIn = true
	m.handleProviderPaneKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleAuthCodeKey(tea.KeyPressMsg{Text: "9"})
	m.handleAuthCodeKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if len(p.saved) != 0 {
		t.Fatalf("esc should not authenticate: %v", p.saved)
	}
	if m.provPane.signIn {
		// The pane keeps asking: esc only closed the input.
		_ = m.provPane.signIn
	}
}
