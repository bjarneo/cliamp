package radio

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// A failed save must leave the pins in memory as they were, so the pane never
// shows a pin that is not on disk.
func TestPinsToggleFailedSaveKeepsMemory(t *testing.T) {
	norway := Place{Code: "NO", Name: "Norway"}
	germany := Place{Code: "DE", Name: "Germany"}
	for _, tc := range []struct {
		name       string
		toggle     Place
		wantPinned bool
	}{
		{name: "pin a new place", toggle: Place{Code: "SE", Name: "Sweden"}, wantPinned: false},
		{name: "unpin the first place", toggle: norway, wantPinned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pins := &Pins{path: filepath.Join(t.TempDir(), pinsFile)}
			for _, place := range []Place{norway, germany} {
				if _, err := pins.Toggle(place); err != nil {
					t.Fatalf("Toggle(%s): %v", place.ID(), err)
				}
			}
			before := pins.Places()

			// A directory cannot be read or replaced as the pins file, even
			// as root.
			pins.path = t.TempDir()
			pinned, err := pins.Toggle(tc.toggle)
			if err == nil {
				t.Fatal("Toggle succeeded, want a save error")
			}
			if pinned != tc.wantPinned {
				t.Errorf("pinned = %v, want the unchanged state %v", pinned, tc.wantPinned)
			}
			if got := pins.Places(); !slices.Equal(got, before) {
				t.Errorf("pins after a failed save = %+v, want %+v", got, before)
			}
		})
	}
}

// placeIDs returns the IDs of places in order.
func placeIDs(places []Place) []string {
	var ids []string
	for _, place := range places {
		ids = append(ids, place.ID())
	}
	return ids
}

// Two instances share one pins file. A toggle applies the intent of its own
// instance to the latest file contents, so it keeps the pins of the other.
func TestPinsToggleUsesLatestFile(t *testing.T) {
	norway := Place{Code: "NO", Name: "Norway"}
	germany := Place{Code: "DE", Name: "Germany"}
	tests := []struct {
		name       string
		initial    []Place // pinned before both instances load
		other      []Place // toggled by the other instance after the load
		toggle     Place
		wantPinned bool
		wantIDs    []string
	}{
		{
			name:       "pin keeps a pin of the other instance",
			other:      []Place{germany},
			toggle:     norway,
			wantPinned: true,
			wantIDs:    []string{"DE", "NO"},
		},
		{
			name:       "unpin keeps a pin of the other instance",
			initial:    []Place{norway},
			other:      []Place{germany},
			toggle:     norway,
			wantPinned: false,
			wantIDs:    []string{"DE"},
		},
		{
			name:       "pin that the other instance made",
			other:      []Place{norway},
			toggle:     norway,
			wantPinned: true,
			wantIDs:    []string{"NO"},
		},
		{
			name:       "unpin that the other instance made",
			initial:    []Place{norway, germany},
			other:      []Place{norway},
			toggle:     norway,
			wantPinned: false,
			wantIDs:    []string{"DE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			seed := LoadPins()
			for _, place := range tt.initial {
				if _, err := seed.Toggle(place); err != nil {
					t.Fatal(err)
				}
			}
			local, other := LoadPins(), LoadPins()
			for _, place := range tt.other {
				if _, err := other.Toggle(place); err != nil {
					t.Fatal(err)
				}
			}

			pinned, err := local.Toggle(tt.toggle)
			if err != nil || pinned != tt.wantPinned {
				t.Fatalf("Toggle = (%v, %v), want (%v, nil)", pinned, err, tt.wantPinned)
			}
			if got := placeIDs(local.Places()); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("pins in memory = %v, want %v", got, tt.wantIDs)
			}
			if got := placeIDs(LoadPins().Places()); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("pins on disk = %v, want %v", got, tt.wantIDs)
			}
		})
	}
}

// A toggle that cannot read the pins file or take its lock must leave the
// file and the pins in memory as they were. A replaced file loses its pins.
func TestPinsToggleFailureKeepsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes and file locks differ on Windows")
	}
	const content = "[[country]]\ncode = \"DE\"\nname = \"Germany\"\n"
	tests := []struct {
		name      string
		breakFile func(t *testing.T, path string)
	}{
		{
			name: "unreadable file",
			breakFile: func(t *testing.T, path string) {
				if os.Geteuid() == 0 {
					t.Skip("root reads a file without read permission")
				}
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
			},
		},
		{
			name: "lock file is a directory",
			breakFile: func(t *testing.T, path string) {
				if err := os.Mkdir(path+".lock", 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), pinsFile)
			writeFile(t, path, content)
			pins := &Pins{path: path}
			tt.breakFile(t, path)

			pinned, err := pins.Toggle(Place{Code: "NO", Name: "Norway"})
			if err == nil || pinned {
				t.Fatalf("Toggle = (%v, %v), want (false, an error)", pinned, err)
			}
			if got := pins.Places(); len(got) != 0 {
				t.Errorf("pins in memory = %+v, want none", got)
			}
			_ = os.Chmod(path, 0o644)
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Errorf("pins file = %q, %v, want %q", data, err, content)
			}
		})
	}
}
