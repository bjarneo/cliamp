package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type interactionBrowseProvider struct {
	commandsTestProvider
}

func (p interactionBrowseProvider) Artists() ([]provider.ArtistInfo, error) { return nil, nil }

func (p interactionBrowseProvider) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return nil, nil
}

func keybindingTestModel() Model {
	local := commandsTestProvider{name: "Local"}
	return Model{
		playlist: playlist.New(),
		provider: local,
		providers: []ProviderEntry{
			{Key: "local", Name: "Local", Provider: local},
			{Key: "yt", Name: "YouTube", Provider: commandsTestProvider{name: "YouTube"}},
		},
	}
}

func TestHandleKeyEnhancedShiftYSelectsYouTubeProvider(t *testing.T) {
	m := keybindingTestModel()
	msg := tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModShift}
	if got := msg.String(); got != "shift+y" {
		t.Fatalf("enhanced Shift+Y string = %q, want shift+y", got)
	}

	m.handleKey(msg)

	if got := m.provider.Name(); got != "YouTube" {
		t.Fatalf("active provider = %q, want YouTube", got)
	}
	if m.lyrics.visible {
		t.Fatal("lyrics.visible = true after enhanced Shift+Y, want false")
	}
}

func TestHandleKeyEnhancedShiftNOpensProviderBrowser(t *testing.T) {
	browse := interactionBrowseProvider{commandsTestProvider{name: "Navidrome"}}
	m := keybindingTestModel()
	m.providers = append(m.providers, ProviderEntry{Key: "navidrome", Name: "Navidrome", Provider: browse})
	msg := tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModShift}

	m.handleKey(msg)

	if !m.navBrowser.visible {
		t.Fatal("navBrowser.visible = false after enhanced Shift+N, want true")
	}
	if got := m.navBrowser.prov.Name(); got != "Navidrome" {
		t.Fatalf("browser provider = %q, want Navidrome", got)
	}
}

func TestHandleKeyEnhancedShiftLetterReachesActiveTextInput(t *testing.T) {
	m := keybindingTestModel()
	m.search.active = true

	m.handleKey(tea.KeyPressMsg{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift})

	if got := m.search.query; got != "A" {
		t.Fatalf("search query = %q, want A", got)
	}
}

func TestHandleKeyPreservesExistingLetterRepresentations(t *testing.T) {
	t.Run("lowercase toggles lyrics", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})

		if !m.lyrics.visible {
			t.Fatal("lyrics.visible = false after lowercase y, want true")
		}
		if got := m.provider.Name(); got != "Local" {
			t.Fatalf("active provider = %q, want Local", got)
		}
	})

	t.Run("uppercase text selects provider", func(t *testing.T) {
		m := keybindingTestModel()
		msg := tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Text: "Y", Mod: tea.ModShift}
		if got := msg.String(); got != "Y" {
			t.Fatalf("uppercase text string = %q, want Y", got)
		}

		m.handleKey(msg)

		if got := m.provider.Name(); got != "YouTube" {
			t.Fatalf("active provider = %q, want YouTube", got)
		}
	})
}

func TestHandleKeyPreservesUnrelatedModifiedKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{name: "shifted non-letter", msg: tea.KeyPressMsg{Code: '1', ShiftedCode: '!', Mod: tea.ModShift}, want: "shift+1"},
		{name: "control shifted letter", msg: tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModCtrl | tea.ModShift}, want: "ctrl+shift+y"},
		{name: "alt shifted letter", msg: tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModAlt | tea.ModShift}, want: "alt+shift+y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if got := tt.msg.String(); got != tt.want {
				t.Fatalf("key string = %q, want %q", got, tt.want)
			}
			if got := normalizeShiftedLetter(tt.msg).String(); got != tt.want {
				t.Fatalf("normalized key string = %q, want %q", got, tt.want)
			}

			m.handleKey(tt.msg)

			if got := m.provider.Name(); got != "Local" {
				t.Fatalf("active provider = %q, want Local", got)
			}
			if m.lyrics.visible || m.navBrowser.visible {
				t.Fatalf("unexpected action: lyrics.visible=%v navBrowser.visible=%v", m.lyrics.visible, m.navBrowser.visible)
			}
		})
	}
}

func TestGlobalHelpOpensOverActiveTextInput(t *testing.T) {
	m := Model{search: searchState{active: true, query: "jazz"}}

	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.keymap.visible {
		t.Fatal("keymap.visible = false after Ctrl+K, want true")
	}
	if !m.search.active || m.search.query != "jazz" {
		t.Fatalf("search state = %+v, want active input preserved", m.search)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.keymap.visible {
		t.Fatal("keymap.visible = true after second Ctrl+K, want false")
	}
	if !m.search.active || m.search.query != "jazz" {
		t.Fatalf("search state = %+v after closing help, want active input preserved", m.search)
	}
}

func TestLyricsRetryStartsNewRequest(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Artist: "Artist", Title: "Title"})
	m := Model{
		playlist: p,
		lyrics: lyricsState{
			visible: true,
			err:     errors.New("temporary failure"),
		},
	}

	if cmd := m.handleKey(tea.KeyPressMsg{Text: "r"}); cmd == nil {
		t.Fatal("lyrics retry command is nil")
	}
	if !m.lyrics.loading {
		t.Fatal("lyrics.loading = false after retry")
	}
	if m.lyrics.err != nil {
		t.Fatalf("lyrics.err = %v after retry, want nil", m.lyrics.err)
	}
	if m.lyrics.query != "Artist\nTitle" {
		t.Fatalf("lyrics.query = %q, want lookup key", m.lyrics.query)
	}
}

func TestUndoRestoresClearedQueue(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One"}, playlist.Track{Title: "Two"})
	p.Queue(0)
	p.Queue(1)
	m := Model{playlist: p, plVisible: 1, queue: queueOverlay{visible: true, cursor: 1, scroll: 1}}

	m.handleQueueKey(tea.KeyPressMsg{Text: "c"})
	if got := p.QueueLen(); got != 0 {
		t.Fatalf("queue length after clear = %d, want 0", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after clear = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.QueueTracks(); len(got) != 2 || got[0].Title != "One" || got[1].Title != "Two" {
		t.Fatalf("queue after undo = %#v, want original queue", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after undo = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
}

func TestNextTrackNormalizesQueueAfterSkippingUnavailableEntry(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Current", Path: "current.mp3"},
		{Title: "Unavailable", Unplayable: true},
		{Title: "Next", Path: "next.mp3"},
		{Title: "Remaining", Path: "remaining.mp3"},
	})
	p.Queue(1)
	p.Queue(2)
	p.Queue(3)
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		plVisible: 2,
		queue:     queueOverlay{visible: true, cursor: 2, scroll: 2},
	}

	m.nextTrack()

	if got := p.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() = %d after Next, want 1", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after Next = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	view, ok := m.activeOverlay()
	if !ok {
		t.Fatal("queue overlay is not active after Next")
	}
	header := stripAnsi(view.header(&m))
	if !strings.Contains(header, "Queue  1/1") {
		t.Fatalf("queue header after Next = %q, want valid 1/1 selection", header)
	}
}

func TestIPCQueueMutationNormalizesOverlay(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{{Title: "A"}, {Title: "B"}, {Title: "C"}})
	p.Queue(0)
	p.Queue(1)
	p.Queue(2)
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		plVisible: 2,
		queue:     queueOverlay{visible: true, cursor: 2, scroll: 2},
	}

	reply := make(chan ipc.Response, 1)
	m.handleIPCQueue(ipc.QueueRequestMsg{Op: "queue.remove", Index: 2, Reply: reply})
	<-reply
	if m.queue.cursor != 1 || m.queue.scroll != 0 {
		t.Fatalf("queue state after IPC remove = cursor %d, scroll %d; want 1, 0", m.queue.cursor, m.queue.scroll)
	}

	reply = make(chan ipc.Response, 1)
	m.handleIPCQueue(ipc.QueueRequestMsg{Op: "queue.clear", Reply: reply})
	<-reply
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after IPC clear = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	view, ok := m.activeOverlay()
	if !ok {
		t.Fatal("queue overlay is not active after IPC clear")
	}
	if header := stripAnsi(view.header(&m)); strings.Contains(header, "1/0") {
		t.Fatalf("queue header after IPC clear = %q, want no invalid selection", header)
	}
}
