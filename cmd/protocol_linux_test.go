//go:build linux

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", t.TempDir()) // no real system entries
	t.Setenv("PATH", t.TempDir())          // no update-desktop-database or xdg-mime

	var out bytes.Buffer
	if locations, err := handlerStatus(); err != nil || len(locations) != 0 {
		t.Fatalf("status before register = (%v, %v), want (none, nil)", locations, err)
	}
	if err := ProtocolStatus(&out); err != nil {
		t.Fatalf("ProtocolStatus: %v", err)
	}
	if !strings.Contains(out.String(), "is not registered") {
		t.Errorf("status output = %q, want it to report an unregistered scheme", out.String())
	}

	out.Reset()
	if err := ProtocolRegister(&out); err != nil {
		t.Fatalf("ProtocolRegister: %v", err)
	}
	locations, err := handlerStatus()
	if err != nil || len(locations) != 1 {
		t.Fatalf("status after register = (%v, %v), want one location", locations, err)
	}
	path := locations[0]

	entry, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the handler entry: %v", err)
	}
	body := string(entry)
	for _, want := range []string{
		"MimeType=x-scheme-handler/cliamp;",
		" open %u",
		"Terminal=true",
		"NoDisplay=true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("handler entry is missing %q:\n%s", want, body)
		}
	}
	// %u passes a single URL. %U would pass a list, and the open command
	// takes exactly one argument.
	if strings.Contains(body, "%U") {
		t.Errorf("handler entry uses %%U, want %%u:\n%s", body)
	}

	out.Reset()
	if err := ProtocolUnregister(&out); err != nil {
		t.Fatalf("ProtocolUnregister: %v", err)
	}
	if locations, _ := handlerStatus(); len(locations) != 0 {
		t.Errorf("scheme still registered after unregister: %v", locations)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("handler entry still present after unregister: %v", err)
	}
}

// TestProtocolUnregisterWhenAbsent confirms removing a registration that was
// never made is a no-op rather than an error, so the command is safe to run
// twice or to run defensively.
func TestProtocolUnregisterWhenAbsent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", t.TempDir())

	var out bytes.Buffer
	if err := ProtocolUnregister(&out); err != nil {
		t.Fatalf("ProtocolUnregister on an unregistered scheme: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("output = %q, want it to report nothing to do", out.String())
	}
}

// TestProtocolRegisterIsIdempotent covers re-running register, which happens
// whenever someone reinstalls cliamp to a new path.
func TestProtocolRegisterIsIdempotent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	for i := range 2 {
		if err := ProtocolRegister(&out); err != nil {
			t.Fatalf("ProtocolRegister call %d: %v", i+1, err)
		}
	}
	dir, err := applicationsDir()
	if err != nil {
		t.Fatalf("applicationsDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 1 {
		t.Errorf("registering twice produced %d entries, want 1", len(entries))
	}
}

// TestHandlerEntryEscapesNothingUnexpected documents that the executable path
// is interpolated into a Desktop Entry Exec line verbatim. Desktop files have
// their own quoting rules, so a path with a space would need escaping; this
// asserts the common case stays intact and fails loudly if the format changes.
func TestHandlerEntryUsesAbsoluteExecutable(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	if err := ProtocolRegister(&out); err != nil {
		t.Fatalf("ProtocolRegister: %v", err)
	}
	locations, err := handlerStatus()
	if err != nil || len(locations) == 0 {
		t.Fatalf("handlerStatus = (%v, %v), want a location", locations, err)
	}
	entry, err := os.ReadFile(locations[0])
	if err != nil {
		t.Fatalf("reading the handler entry: %v", err)
	}
	for _, line := range strings.Split(string(entry), "\n") {
		after, ok := strings.CutPrefix(line, "Exec=")
		if !ok {
			continue
		}
		// The path is quoted so an install directory containing a space
		// still parses as one argument.
		if !strings.HasPrefix(after, `"`) {
			t.Errorf("Exec line %q does not quote the executable", line)
		}
		quoted, rest, ok := strings.Cut(strings.TrimPrefix(after, `"`), `"`)
		if !ok {
			t.Fatalf("Exec line %q has an unterminated quote", line)
		}
		if !filepath.IsAbs(quoted) {
			t.Errorf("Exec line %q does not use an absolute path", line)
		}
		// Field codes must sit outside the quotes to be expanded.
		if strings.TrimSpace(rest) != "open %u" {
			t.Errorf("Exec line %q does not end with an unquoted `open %%u`", line)
		}
		return
	}
	t.Errorf("handler entry has no Exec line:\n%s", entry)
}

// TestDesktopExecArgQuoting covers the paths that break an unquoted Exec line.
func TestDesktopExecArgQuoting(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/usr/bin/cliamp", `"/usr/bin/cliamp"`},
		{"/home/a b/cliamp", `"/home/a b/cliamp"`},
		{"/home/a\"b/cliamp", "\"/home/a\\\"b/cliamp\""},
		{`/home/a\b/cliamp`, `"/home/a\\b/cliamp"`},
		{"/home/a$b/cliamp", `"/home/a\$b/cliamp"`},
		{"/home/a`b/cliamp", "\"/home/a\\`b/cliamp\""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := desktopExecArg(tt.path); got != tt.want {
				t.Errorf("desktopExecArg(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestProtocolStatusFindsSystemWideEntry covers the install.sh case: for an
// install outside the home directory the handler entry lands in a system
// applications directory. Status must see it, or a user would be told the
// scheme is unregistered while links still open cliamp.
func TestProtocolStatusFindsSystemWideEntry(t *testing.T) {
	systemShare := t.TempDir()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", systemShare)

	systemApps := filepath.Join(systemShare, "applications")
	if err := os.MkdirAll(systemApps, 0o755); err != nil {
		t.Fatalf("creating %s: %v", systemApps, err)
	}
	systemEntry := filepath.Join(systemApps, desktopFileName)
	if err := os.WriteFile(systemEntry, []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", systemEntry, err)
	}

	locations, err := handlerStatus()
	if err != nil {
		t.Fatalf("handlerStatus: %v", err)
	}
	if len(locations) != 1 || locations[0] != systemEntry {
		t.Fatalf("handlerStatus = %v, want [%s]", locations, systemEntry)
	}

	var out bytes.Buffer
	if err := ProtocolStatus(&out); err != nil {
		t.Fatalf("ProtocolStatus: %v", err)
	}
	if !strings.Contains(out.String(), "is registered") {
		t.Errorf("status output = %q, want it to report a registered scheme", out.String())
	}
}

// TestProtocolStatusOrdersUserEntryFirst confirms the precedence note is
// accurate: XDG_DATA_HOME shadows the system directories.
func TestProtocolStatusOrdersUserEntryFirst(t *testing.T) {
	systemShare := t.TempDir()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_DATA_DIRS", systemShare)
	t.Setenv("PATH", t.TempDir())

	systemApps := filepath.Join(systemShare, "applications")
	if err := os.MkdirAll(systemApps, 0o755); err != nil {
		t.Fatalf("creating %s: %v", systemApps, err)
	}
	if err := os.WriteFile(filepath.Join(systemApps, desktopFileName), []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatalf("writing the system entry: %v", err)
	}

	var out bytes.Buffer
	if err := ProtocolRegister(&out); err != nil {
		t.Fatalf("ProtocolRegister: %v", err)
	}
	locations, err := handlerStatus()
	if err != nil {
		t.Fatalf("handlerStatus: %v", err)
	}
	if len(locations) != 2 {
		t.Fatalf("handlerStatus = %v, want two locations", locations)
	}
	userDir, _ := applicationsDir()
	if filepath.Dir(locations[0]) != userDir {
		t.Errorf("locations[0] = %s, want the user entry under %s", locations[0], userDir)
	}

	// Unregister removes the writable ones; both are writable here.
	removed, blocked, err := unregisterHandler()
	if err != nil {
		t.Fatalf("unregisterHandler: %v", err)
	}
	if len(removed) != 2 || len(blocked) != 0 {
		t.Errorf("unregisterHandler removed %v, blocked %v; want both removed", removed, blocked)
	}
}
