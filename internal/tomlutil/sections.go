package tomlutil

import "strings"

// ParseSections parses a minimal TOML document made up of repeated
// [[<section>]] blocks of `key = "value"` lines. For each section header it
// calls emit once with the accumulated fields, with values unquoted via
// Unquote. Blank lines, comments (#), and lines outside any section are
// ignored. An empty section still triggers emit, so callers apply their own
// validation. When a key repeats within a section, the last value wins.
func ParseSections(data []byte, section string, emit func(fields map[string]string)) {
	header := "[[" + section + "]]"
	var fields map[string]string
	flush := func() {
		if fields != nil {
			emit(fields)
		}
	}
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == header {
			flush()
			fields = make(map[string]string)
			continue
		}
		if fields == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = Unquote(strings.TrimSpace(val))
	}
	flush()
}
