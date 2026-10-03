// Package theme handles loading and parsing color themes from TOML files.
package theme

import (
	"bufio"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appdir"
)

//go:embed themes/*.toml
var builtinThemes embed.FS

// DefaultName is the display name for the built-in ANSI fallback theme.
const DefaultName = "Default - Terminal colors"

// Theme holds a named color scheme with hex color values.
type Theme struct {
	Name     string
	BG       string
	Accent   string // hex
	BrightFG string
	FG       string
	Green    string
	Yellow   string
	Red      string
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// IsDefault returns true if this is the sentinel default theme (no hex values).
func (t Theme) IsDefault() bool {
	return t.BG == "" && t.Accent == "" && t.Green == "" && t.BrightFG == ""
}

// Validate ensures a theme file supplies the complete six-color foreground
// palette in CSS hex notation. Background is optional. The Default sentinel
// has no colors, so it does not pass.
func (t Theme) Validate() error {
	for _, color := range []struct {
		name  string
		value string
	}{
		{"accent", t.Accent},
		{"bright_fg", t.BrightFG},
		{"fg", t.FG},
		{"green", t.Green},
		{"yellow", t.Yellow},
		{"red", t.Red},
	} {
		if color.value == "" {
			return fmt.Errorf("theme %q: %s is required", t.Name, color.name)
		}
		if !hexColor.MatchString(color.value) {
			return fmt.Errorf("theme %q: %s must be #RRGGBB", t.Name, color.name)
		}
	}
	if t.BG != "" && !hexColor.MatchString(t.BG) {
		return fmt.Errorf("theme %q: bg must be #RRGGBB", t.Name)
	}
	return nil
}

// Default returns a sentinel "Default" theme with empty hex values,
// signaling that ANSI fallback colors should be used.
func Default() Theme {
	return Theme{Name: DefaultName}
}

// Parse reads flat TOML key=value lines from r and returns a Theme.
// Uses the same manual parsing approach as config/config.go.
func Parse(name string, r io.Reader) (Theme, error) {
	t := Theme{Name: name}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = parseValue(val)

		switch key {
		case "bg":
			t.BG = val
		case "accent":
			t.Accent = val
		case "bright_fg":
			t.BrightFG = val
		case "fg":
			t.FG = val
		case "red":
			t.Red = val
		case "yellow":
			t.Yellow = val
		case "green":
			t.Green = val
		}
	}
	return t, scanner.Err()
}

// parseValue returns the value of a key = value line without its quotes and
// without a trailing # comment. A # outside quotes starts a comment only
// after white space, so an unquoted #RRGGBB value stays whole.
func parseValue(val string) string {
	val = strings.TrimSpace(val)
	if val != "" && (val[0] == '"' || val[0] == '\'') {
		if end := strings.IndexByte(val[1:], val[0]); end >= 0 {
			rest := strings.TrimSpace(val[end+2:])
			if rest == "" || rest[0] == '#' {
				return val[1 : end+1]
			}
		}
	}
	for i := 1; i < len(val); i++ {
		if val[i] == '#' && (val[i-1] == ' ' || val[i-1] == '\t') {
			val = strings.TrimSpace(val[:i])
			break
		}
	}
	return strings.Trim(val, `"'`)
}

// IsDefaultName reports whether name selects the ANSI default theme. It
// accepts an empty name, "default" and DefaultName, in any case.
func IsDefaultName(name string) bool {
	return name == "" || strings.EqualFold(name, "default") || strings.EqualFold(name, DefaultName)
}

// Find returns the theme called name, matched case-insensitively across the
// built-in and user themes. A name that IsDefaultName accepts gives the ANSI
// default. ok is false when no theme has that name.
func Find(name string) (Theme, bool) {
	if IsDefaultName(name) {
		return Default(), true
	}
	for _, t := range LoadAll() {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return Theme{}, false
}

// loggedSkips holds the text of each skip error that LoadAll has logged.
var loggedSkips sync.Map

// LoadAll loads built-in themes and user custom themes from
// ~/.config/cliamp/themes/*.toml. User themes override built-in
// themes with the same name. Returns a sorted list. It logs each
// theme file that it skips, with the reason, once per process.
func LoadAll() []Theme {
	themes, errs := LoadAllWithErrors()
	for _, err := range errs {
		if _, seen := loggedSkips.LoadOrStore(err.Error(), true); !seen {
			applog.Warn("theme: %v", err)
		}
	}
	return themes
}

// LoadAllWithErrors returns the sorted themes and one error for each theme
// file that it skips. It logs nothing, so a caller with no log file can
// print the errors.
func LoadAllWithErrors() ([]Theme, []error) {
	themes := make(map[string]Theme)

	// Load embedded built-in themes (lower priority).
	errs := loadFS(builtinThemes, "themes", "built-in themes", themes)

	// Load user custom themes (override built-in if same name).
	dir, err := appdir.Dir()
	if err == nil {
		userDir := filepath.Join(dir, "themes")
		errs = append(errs, loadFS(os.DirFS(userDir), ".", userDir, themes)...)
	}

	// Sort by name.
	result := make([]Theme, 0, len(themes))
	for _, t := range themes {
		result = append(result, t)
	}
	slices.SortFunc(result, func(a, b Theme) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return result, errs
}

// loadFS parses the .toml files in dir of fsys and stores each valid theme
// in themes under its lower-case file name. It returns one error for each
// file that it skips. where names the directory in these errors. A missing
// directory is not an error.
func loadFS(fsys fs.FS, dir, where string, themes map[string]Theme) []error {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{fmt.Errorf("read %s: %w", where, err)}
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		if err := loadFile(fsys, path.Join(dir, e.Name()), themes); err != nil {
			errs = append(errs, fmt.Errorf("skip %s in %s: %w", e.Name(), where, err))
		}
	}
	return errs
}

// loadFile parses and validates one theme file and stores it in themes. It
// skips a file whose name selects the terminal colors, because no lookup or
// saved config could select that theme.
func loadFile(fsys fs.FS, file string, themes map[string]Theme) error {
	name := strings.TrimSuffix(path.Base(file), ".toml")
	if IsDefaultName(name) {
		return fmt.Errorf("theme %q: name is reserved for the terminal colors", name)
	}
	f, err := fsys.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	t, err := Parse(name, f)
	if err != nil {
		return err
	}
	if err := t.Validate(); err != nil {
		return err
	}
	themes[strings.ToLower(name)] = t
	return nil
}
