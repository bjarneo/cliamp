package model

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestHideHelpBarReleasesFixedRow checks that hiding the help bar hands its row
// back to the body at every size.
func TestHideHelpBarReleasesFixedRow(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "minimal", width: 40, height: 10},
		{name: "compact", width: 56, height: 16},
		{name: "full", width: 80, height: 24},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown := newLayoutTestModel(tt.width, tt.height)
			hidden := newLayoutTestModel(tt.width, tt.height)
			hidden.SetHideHelpBar(true)

			if got, want := hidden.layout.bodyRows, shown.layout.bodyRows+1; got != want {
				t.Fatalf("body rows with hidden help bar = %d, want %d", got, want)
			}
		})
	}
}

// TestHideHelpBarLeavesSimplifiedLayoutAlone checks that the simplified view,
// which never draws the help bar, renders the same when the option is set.
func TestHideHelpBarLeavesSimplifiedLayoutAlone(t *testing.T) {
	shown := newLayoutTestModel(80, 24)
	shown.simplified = true
	shown.recomputeLayout()

	hidden := newLayoutTestModel(80, 24)
	hidden.simplified = true
	hidden.SetHideHelpBar(true)

	if got, want := hidden.mainSections(""), shown.mainSections(""); !slices.Equal(got, want) {
		t.Fatalf("simplified sections = %q, want %q", got, want)
	}
}

// TestHideHelpBarOmitsHelpSection checks that the help bar is absent from the
// rendered sections, rather than rendered as a blank row.
func TestHideHelpBarOmitsHelpSection(t *testing.T) {
	shown := newLayoutTestModel(80, 24)
	hidden := newLayoutTestModel(80, 24)
	hidden.SetHideHelpBar(true)

	help := shown.renderHelpBar()
	if help == "" {
		t.Fatal("expected the test model to render a help bar")
	}

	for _, section := range hidden.mainSections("") {
		if section == help {
			t.Fatal("help bar rendered while hide_help_bar is set")
		}
	}

	shownSections := shown.mainSections("")
	hiddenSections := hidden.mainSections("")
	if got, want := len(hiddenSections), len(shownSections)-1; got != want {
		t.Fatalf("sections with hidden help bar = %d, want %d", got, want)
	}
}

// TestCtrlGTogglesHelpBar checks that Ctrl+G reaches the toggle through the
// main key path, not just through the helper.
func TestCtrlGTogglesHelpBar(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.hideHelpBar {
		t.Fatal("ctrl+g did not hide the help bar")
	}
	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.hideHelpBar {
		t.Fatal("ctrl+g did not restore the help bar")
	}
}

// TestToggleHelpBar checks that toggling restores both the help bar and the
// row it borrowed from the body.
func TestToggleHelpBar(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	rows := m.layout.bodyRows

	m.toggleHelpBar()
	if !m.hideHelpBar {
		t.Fatal("toggle did not hide the help bar")
	}
	if m.layout.bodyRows != rows+1 {
		t.Fatalf("body rows after hiding = %d, want %d", m.layout.bodyRows, rows+1)
	}

	m.toggleHelpBar()
	if m.hideHelpBar {
		t.Fatal("toggle did not restore the help bar")
	}
	if m.layout.bodyRows != rows {
		t.Fatalf("body rows after restoring = %d, want %d", m.layout.bodyRows, rows)
	}
}

// TestToggleHelpBarPersists checks that the runtime toggle writes the choice to
// the config, so the help bar comes back the way it was left.
func TestToggleHelpBarPersists(t *testing.T) {
	saver := &recordingSaver{}
	m := newColumnTestModel(86, 24)
	m.configSaver = saver

	m.toggleHelpBar()
	if got := saver.saved["hide_help_bar"]; got != "true" {
		t.Fatalf("saved hide_help_bar = %q, want %q", got, "true")
	}

	m.toggleHelpBar()
	if got := saver.saved["hide_help_bar"]; got != "false" {
		t.Fatalf("saved hide_help_bar = %q, want %q", got, "false")
	}
}

// TestHelpBarFollowsFocusOnEveryTier checks that the help bar shows the keys
// of what owns them on each tier that draws it. On the minimal tier the
// provider list owns the keys after Esc, so the bar shows the provider hints.
func TestHelpBarFollowsFocusOnEveryTier(t *testing.T) {
	tiers := []struct {
		name          string
		width, height int
	}{
		{"minimal", 40, 10},
		{"compact", 56, 16},
		{"full", 80, 24},
	}
	focuses := []struct {
		name       string
		focus      focusArea
		provSearch bool
		want       commandMode
	}{
		{"playlist", focusPlaylist, false, commandModeMain},
		{"provider", focusProvider, false, commandModeProvider},
		{"provider filter", focusProvider, true, commandModeProviderSearch},
	}
	for _, tier := range tiers {
		for _, tc := range focuses {
			t.Run(tier.name+" "+tc.name, func(t *testing.T) {
				m := newLayoutTestModel(tier.width, tier.height)
				m.provider = &readOnlyGenreProvider{}
				m.focus = tc.focus
				m.provSearch.active = tc.provSearch
				want := m.helpBar(tc.want)
				if tc.want != commandModeMain && want == m.helpBar(commandModeMain) {
					t.Fatalf("the %s hints equal the main hints, so the test proves nothing", tc.name)
				}
				if !slices.Contains(m.mainSections(""), want) {
					t.Errorf("help bar does not show the %s hints %q", tc.name, want)
				}
			})
		}
	}
}
