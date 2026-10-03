package model

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestParseJumpTarget(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		// Expected success cases.
		{name: "seconds only", in: "10", want: 10 * time.Second},
		{name: "missing minutes accepted", in: ":49", want: 49 * time.Second},
		{name: "minutes seconds", in: "58:05", want: 58*time.Minute + 5*time.Second},
		{name: "minutes one-digit seconds", in: "58:6", want: 58*time.Minute + 6*time.Second},
		{name: "minutes trailing colon", in: "58:", want: 58 * time.Minute},
		{name: "spaces trimmed", in: "  12:3  ", want: 12*time.Minute + 3*time.Second},
		{name: "hours minutes seconds", in: "1:02:03", want: time.Hour + 2*time.Minute + 3*time.Second},
		{name: "hours one-digit parts", in: "2:3:4", want: 2*time.Hour + 3*time.Minute + 4*time.Second},
		{name: "hours missing minutes accepted", in: "1::03", want: time.Hour + 3*time.Second},
		{name: "hours trailing colon accepted", in: "1:02:", want: time.Hour + 2*time.Minute},

		// Expected failure cases.
		{name: "empty", in: "", wantErr: true},
		{name: "not number", in: "abc", wantErr: true},
		{name: "bad minutes", in: "x:05", wantErr: true},
		{name: "bad seconds", in: "10:x", wantErr: true},
		{name: "hours bad minutes", in: "1:60:00", wantErr: true},
		{name: "hours bad seconds", in: "1:00:60", wantErr: true},
		{name: "hours non-numeric", in: "x:02:03", wantErr: true},
		{name: "hours minutes too many digits", in: "1:123:03", wantErr: true},
		{name: "seconds too large", in: "10:60", wantErr: true},
		{name: "high minute high second", in: "99:99", wantErr: true},
		{name: "too many colons", in: "1:2:3:4", wantErr: true},
		{name: "too many second digits", in: "10:123", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJumpTarget(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestInvalidJumpKeepsInputOpen(t *testing.T) {
	m := Model{jump: jumpState{active: true, input: "1:99"}}

	m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.jump.active {
		t.Fatal("jump.active = false after invalid target, want input to remain open")
	}
	if m.jump.input != "1:99" {
		t.Fatalf("jump input = %q, want preserved value", m.jump.input)
	}
	if m.status.text == "" {
		t.Fatal("status is empty after invalid target")
	}
}

func TestJumpSeekFailureKeepsInputOpen(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour, seekErr: errors.New("decoder refused")}
	m := Model{player: eng, jump: jumpState{active: true, input: "1:00"}}

	if cmd := m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatalf("cmd = %v, want nil for an in-place seek", cmd)
	}
	if len(eng.seekCalls) != 1 || eng.seekCalls[0] != time.Minute {
		t.Fatalf("Seek calls = %v, want [1m0s]", eng.seekCalls)
	}
	if !m.jump.active || m.jump.input != "1:00" {
		t.Fatalf("jump mode = %v with input %q, want it open with the input kept", m.jump.active, m.jump.input)
	}
	if m.jump.err != "Seek failed: decoder refused" {
		t.Fatalf("jump.err = %q, want the seek error", m.jump.err)
	}
}

func TestFormatJumpClock(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: 0, want: "00:00"},
		{in: 10 * time.Second, want: "00:10"},
		{in: 58*time.Minute + 5*time.Second, want: "58:05"},
		{in: 1 * time.Hour, want: "60:00"},
		{in: 75*time.Minute + 48*time.Second, want: "75:48"},
	}

	for _, tt := range tests {
		if got := formatJumpClock(tt.in); got != tt.want {
			t.Fatalf("formatJumpClock(%v) = %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatJumpPlaceholder(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: -1 * time.Second, want: "00:00"},
		{in: 0, want: "00:00"},
		{in: 59 * time.Second, want: "00:00"},
		{in: 1 * time.Minute, want: "00:00"},
		{in: 59*time.Minute + 59*time.Second, want: "00:00"},
		{in: 1 * time.Hour, want: "00:00"},
	}

	for _, tt := range tests {
		if got := formatJumpPlaceholder(tt.in); got != tt.want {
			t.Fatalf("formatJumpPlaceholder(%v) = %q want %q", tt.in, got, tt.want)
		}
	}
}

// TestOpeningAnInputClearsItsLastState checks that the jump input, the URL
// input and the track info overlay open empty, whatever an earlier use left
// in their state.
func TestOpeningAnInputClearsItsLastState(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		stale func(*Model)
		check func(*testing.T, Model)
	}{
		{
			name:  "jump",
			key:   tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl},
			stale: func(m *Model) { m.jump = jumpState{input: "9:99", err: "Invalid jump"} },
			check: func(t *testing.T, m Model) {
				if m.jump != (jumpState{active: true}) {
					t.Fatalf("jump = %+v, want an empty open input", m.jump)
				}
			},
		},
		{
			name:  "URL input",
			key:   tea.KeyPressMsg{Code: 'u', Text: "u"},
			stale: func(m *Model) { m.urlInput = urlInputState{input: "http://old", err: "Enter a stream"} },
			check: func(t *testing.T, m Model) {
				if m.urlInput != (urlInputState{active: true}) {
					t.Fatalf("urlInput = %+v, want an empty open input", m.urlInput)
				}
			},
		},
		{
			name:  "track info",
			key:   tea.KeyPressMsg{Code: 'i', Text: "i"},
			stale: func(m *Model) { m.info.scroll = 4 },
			check: func(t *testing.T, m Model) {
				if m.info != (infoOverlay{visible: true}) {
					t.Fatalf("info = %+v, want the overlay open at the top", m.info)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.focus = focusPlaylist
			tt.stale(&m)
			m.handleKey(tt.key)
			tt.check(t, m)
		})
	}
}
