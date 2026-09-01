package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// locationProvider is a radio-shaped provider: a Countries section whose first
// row offers to work out where the listener is.
type locationProvider struct {
	asking   bool
	detected string
	consent  *bool
	err      error
}

const testConsentID = "loc:ask"

func (p *locationProvider) Name() string { return "Radio" }

func (p *locationProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	var lists []playlist.PlaylistInfo
	if p.asking {
		lists = append(lists, playlist.PlaylistInfo{ID: testConsentID, Name: "Use my location"})
	}
	if p.consent != nil && *p.consent && p.detected != "" {
		lists = append(lists, playlist.PlaylistInfo{ID: "p:0", Name: p.detected + " (near you)"})
	}
	return append(lists, playlist.PlaylistInfo{ID: "l:0", Name: "cliamp radio"}), nil
}

func (p *locationProvider) Tracks(id string) ([]playlist.Track, error) {
	if id == testConsentID {
		panic("Tracks must never be called for the location row")
	}
	return nil, nil
}

func (p *locationProvider) NeedsLocationConsent() bool { return p.asking }
func (p *locationProvider) LocationPrompt() string     { return "Use your location?" }

func (p *locationProvider) LocationConsentID() string {
	if !p.asking {
		return ""
	}
	return testConsentID
}

func (p *locationProvider) SetLocationConsent(allowed bool) (string, error) {
	p.asking = false
	p.consent = &allowed
	if !allowed {
		return "", p.err
	}
	return p.detected, p.err
}

var (
	_ playlist.Provider          = (*locationProvider)(nil)
	_ provider.LocationConsenter = (*locationProvider)(nil)
)

func newLocationModel(p *locationProvider) Model {
	m := Model{provider: p, playlist: playlist.New(), plVisible: 10, focus: focusProvider}
	lists, _ := p.Playlists()
	m.providerLists = providerListsWithBrowse(p, lists)
	return m
}

// Selecting the offer row must ask, and must not load anything or work out
// where the listener is on the way.
func TestSelectingTheLocationRowAsksFirst(t *testing.T) {
	p := &locationProvider{asking: true, detected: "Norway"}
	m := newLocationModel(p)

	if got := m.providerLists[0].ID; got != testConsentID {
		t.Fatalf("first row = %q, want the location offer", got)
	}
	if cmd := m.openProviderList(0); cmd != nil {
		t.Error("selecting the offer should ask, not fetch")
	}
	if !m.provAskLoc {
		t.Fatal("the location question was not raised")
	}
	if p.consent != nil {
		t.Error("consent was recorded before the listener answered")
	}
	if m.provLoading {
		t.Error("a load was started for a row that is a question")
	}
}

func TestLocationPromptAnsweredYes(t *testing.T) {
	p := &locationProvider{asking: true, detected: "Norway"}
	m := newLocationModel(p)
	m.openProviderList(0)

	if cmd := m.answerLocationPrompt(true); cmd == nil {
		t.Error("agreeing should refresh the pane")
	}
	if m.provAskLoc {
		t.Error("the question is still on screen")
	}
	if p.consent == nil || !*p.consent {
		t.Fatalf("consent = %v, want true", p.consent)
	}
	// The offer is spent: it must not come back on the next render.
	if p.LocationConsentID() != "" {
		t.Error("the offer row is still being advertised")
	}
}

func TestLocationPromptAnsweredNo(t *testing.T) {
	p := &locationProvider{asking: true, detected: "Norway"}
	m := newLocationModel(p)
	m.openProviderList(0)

	m.answerLocationPrompt(false)

	if m.provAskLoc {
		t.Error("the question is still on screen")
	}
	if p.consent == nil || *p.consent {
		t.Fatalf("consent = %v, want false", p.consent)
	}
	if p.LocationConsentID() != "" {
		t.Error("declining should retire the offer, not repeat it")
	}
}

// The question owns the keyboard while it is up: keys that mean something else
// in this pane must not dismiss it by accident.
func TestLocationPromptKeys(t *testing.T) {
	for _, tc := range []struct {
		name         string
		key          tea.KeyPressMsg
		wantAnswer   *bool
		wantOnScreen bool
	}{
		{name: "y", key: tea.KeyPressMsg{Text: "y"}, wantAnswer: ptr(true)},
		{name: "Y", key: tea.KeyPressMsg{Text: "Y"}, wantAnswer: ptr(true)},
		{name: "enter", key: tea.KeyPressMsg{Code: tea.KeyEnter}, wantAnswer: ptr(true)},
		{name: "n", key: tea.KeyPressMsg{Text: "n"}, wantAnswer: ptr(false)},
		{name: "N", key: tea.KeyPressMsg{Text: "N"}, wantAnswer: ptr(false)},
		{name: "esc", key: tea.KeyPressMsg{Code: tea.KeyEscape}, wantAnswer: ptr(false)},
		{name: "j scrolls elsewhere", key: tea.KeyPressMsg{Text: "j"}, wantOnScreen: true},
		{name: "f favorites elsewhere", key: tea.KeyPressMsg{Text: "f"}, wantOnScreen: true},
		{name: "slash searches elsewhere", key: tea.KeyPressMsg{Text: "/"}, wantOnScreen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &locationProvider{asking: true, detected: "Norway"}
			m := newLocationModel(p)
			m.openProviderList(0)

			m.handleKey(tc.key)

			switch {
			case tc.wantAnswer == nil && p.consent != nil:
				t.Errorf("%s answered the question with %v", tc.name, *p.consent)
			case tc.wantAnswer != nil && p.consent == nil:
				t.Errorf("%s did not answer the question", tc.name)
			case tc.wantAnswer != nil && *p.consent != *tc.wantAnswer:
				t.Errorf("%s answered %v, want %v", tc.name, *p.consent, *tc.wantAnswer)
			}
			if m.provAskLoc != tc.wantOnScreen {
				t.Errorf("%s left the question on screen = %v, want %v", tc.name, m.provAskLoc, tc.wantOnScreen)
			}
		})
	}
}

// A provider that never offers the row is untouched by any of this.
func TestProviderWithoutLocationConsentIsUnaffected(t *testing.T) {
	m := Model{provider: &commandsTestProvider{}, playlist: playlist.New(), focus: focusProvider}
	if cmd := m.answerLocationPrompt(true); cmd != nil {
		t.Error("answering should be inert for a provider that never asked")
	}
	if m.provAskLoc {
		t.Error("no question should be on screen")
	}
}

func ptr[T any](v T) *T { return &v }
