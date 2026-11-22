package model

import (
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestReservedKeysComeFromCommandRegistry(t *testing.T) {
	reserved := ReservedKeys()
	for _, command := range commandRegistry {
		for _, key := range command.Keys {
			if !reserved[key] {
				t.Fatalf("reserved keys omit registry key %q", key)
			}
		}
	}
}

func TestMixcloudShortcutRegistryEntry(t *testing.T) {
	for _, command := range commandRegistry {
		if len(command.Keys) == 1 && command.Keys[0] == "X" && command.Mode&commandModeMain != 0 {
			if command.KeyLabel != "X" || command.Label != "Open Mixcloud provider" || !command.Keymap {
				t.Fatalf("Mixcloud command = %+v", command)
			}
			return
		}
	}
	t.Fatal("Mixcloud shortcut is missing from the command registry")
}

func TestPreviousEQPresetRegistryEntry(t *testing.T) {
	for _, command := range commandRegistry {
		if len(command.Keys) == 1 && command.Keys[0] == "alt+e" && command.Mode&commandModeMain != 0 {
			if command.KeyLabel != "Alt+E" || command.Label != "Previous EQ preset" || !command.Keymap {
				t.Fatalf("previous EQ preset command = %+v", command)
			}
			m := keybindingTestModel()
			if !command.enabled(m) {
				t.Fatal("previous EQ preset command is disabled in a full model")
			}
			m.layout.tier = layoutMinimal
			if command.enabled(m) {
				t.Fatal("previous EQ preset command is enabled in minimal layout")
			}
			m.layout.tier = layoutFull
			m.simplified = true
			if command.enabled(m) {
				t.Fatal("previous EQ preset command is enabled in simplified mode")
			}
			return
		}
	}
	t.Fatal("previous EQ preset command is missing from the command registry")
}

func TestContextHelpAdvertisesProviderBrowsing(t *testing.T) {
	browse := trackArtistBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.provider = browse
	m.providers = append(m.providers, provider.Entry{Key: "mixcloud", Name: "Mixcloud", Provider: browse})
	m.playlist.Add(playlist.Track{
		Title: "A Show", Artist: "Creator",
		ProviderMeta: map[string]string{"test.creator": "creator"},
	})
	m.focus = focusPlaylist
	if help := m.commandHelp(commandModeMain); strings.Contains(help, "Browse creator") || strings.Contains(help, " N ") {
		t.Fatalf("playlist help bar should not advertise N browse: %q", help)
	}

	m.focus = focusProvider
	paneBrowse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m.provider = paneBrowse
	m.provPane.lists = providerListsWithBrowse(paneBrowse, nil)
	help := m.commandHelp(commandModeProvider)
	if !strings.Contains(help, "Open") {
		t.Fatalf("provider help does not advertise browse entry Open: %q", help)
	}
	if strings.Contains(help, "Browse provider") {
		t.Fatalf("provider help bar should not advertise N browse: %q", help)
	}
}

func helpBarSlotBacked(mode commandMode, key string) bool {
	if registryLists(mode, key) {
		return true
	}
	for name, handlerMode := range focusKeyHandlers {
		if handlerMode != mode {
			continue
		}
		if slices.Contains(unlistedKeys[name], key) {
			return true
		}
	}
	return false
}

func TestHelpBarKeyOrderListsEveryKeyedRow(t *testing.T) {
	for mode, keys := range helpBarKeyOrder {
		for _, key := range keys {
			if !helpBarSlotBacked(mode, key) {
				t.Errorf("mode %d helpBarKeyOrder lists %q, but no registry row or focused handler backs it", mode, key)
			}
		}
	}
}
