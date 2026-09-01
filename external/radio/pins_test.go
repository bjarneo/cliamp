package radio

import (
	"path/filepath"
	"testing"
)

func TestPlaceID(t *testing.T) {
	for _, tc := range []struct {
		place Place
		want  string
	}{
		{Place{Code: "NO", Name: "Norway"}, "NO"},
		{Place{Code: "NO", Name: "Oslo, Norway", State: "Oslo"}, "NO/Oslo"},
	} {
		if got := tc.place.ID(); got != tc.want {
			t.Errorf("Place%+v.ID() = %q, want %q", tc.place, got, tc.want)
		}
	}
}

func TestParsePlaceID(t *testing.T) {
	for _, tc := range []struct {
		id, code, state string
		ok              bool
	}{
		{id: "NO", code: "NO", ok: true},
		{id: "NO/Oslo", code: "NO", state: "Oslo", ok: true},
		{id: "no/oslo", code: "NO", state: "oslo", ok: true},
		{id: "NO/Møre og Romsdal", code: "NO", state: "Møre og Romsdal", ok: true},
		{id: "XX", ok: false},
		{id: "", ok: false},
		{id: "browse:countries", ok: false},
	} {
		code, state, ok := parsePlaceID(tc.id)
		if ok != tc.ok || code != tc.code || state != tc.state {
			t.Errorf("parsePlaceID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.id, code, state, ok, tc.code, tc.state, tc.ok)
		}
	}
}

func TestPinsToggleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	pins := LoadPins()
	if len(pins.Places()) != 0 {
		t.Fatalf("fresh pins = %+v, want empty", pins.Places())
	}

	norway := Place{Code: "NO", Name: "Norway"}
	oslo := Place{Code: "NO", Name: "Oslo, Norway", State: "Oslo"}

	for _, place := range []Place{norway, oslo} {
		pinned, err := pins.Toggle(place)
		if err != nil {
			t.Fatalf("Toggle(%s): %v", place.ID(), err)
		}
		if !pinned {
			t.Errorf("Toggle(%s) = false, want pinned", place.ID())
		}
	}

	// A country and one of its regions are distinct pins.
	if got := len(pins.Places()); got != 2 {
		t.Fatalf("pins = %+v, want 2", pins.Places())
	}
	if !pins.Contains("NO") || !pins.Contains("NO/Oslo") {
		t.Errorf("pins = %+v, want both NO and NO/Oslo", pins.Places())
	}

	reloaded := LoadPins()
	if len(reloaded.Places()) != 2 || !reloaded.Contains("NO/Oslo") {
		t.Fatalf("reloaded pins = %+v", reloaded.Places())
	}
	if got := reloaded.Places()[1].Name; got != "Oslo, Norway" {
		t.Errorf("reloaded name = %q, want %q", got, "Oslo, Norway")
	}

	if pinned, err := pins.Toggle(norway); err != nil || pinned {
		t.Fatalf("un-toggle = (%v, %v), want (false, nil)", pinned, err)
	}
	if after := LoadPins(); after.Contains("NO") || !after.Contains("NO/Oslo") {
		t.Errorf("after unpinning NO, pins = %+v", after.Places())
	}
}

func TestLoadPinsSkipsUnusableRows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	writeFile(t, filepath.Join(dir, ".config", "cliamp", pinsFile), `
[[country]]
code = "NO"
name = "Norway"

[[country]]
code = "XX"
name = "Unknown"

[[country]]
name = "No code at all"

[[country]]
code = "de"
`)

	pins := LoadPins()
	if len(pins.Places()) != 2 {
		t.Fatalf("pins = %+v, want 2 usable rows", pins.Places())
	}
	// A row with a code but no name falls back to the code, upper-cased.
	if got := pins.Places()[1]; got.Code != "DE" || got.Name != "DE" {
		t.Errorf("second pin = %+v, want code and name DE", got)
	}
}

func TestPinsToggleSurvivesMissingConfigDir(t *testing.T) {
	// LoadPins with an unresolvable home yields an empty but usable set.
	pins := &Pins{}
	if pins.Contains("NO") {
		t.Error("zero Pins should contain nothing")
	}
	t.Setenv("HOME", t.TempDir())
	if pinned, err := pins.Toggle(Place{Code: "NO", Name: "Norway"}); err != nil || !pinned {
		t.Fatalf("Toggle on zero Pins = (%v, %v)", pinned, err)
	}
}
