package tomlutil

import "strings"

// ParseSections parses a minimal TOML document made up of repeated
// [[<section>]] blocks of `key = "value"` lines. For each section header it
// calls emit once with the accumulated fields, with values unquoted via
// Unquote. Blank lines, comments (#), and lines outside any section are
// ignored. An empty section still triggers emit, so callers apply their own
// validation. When a key repeats within a section, the last value wins.
func ParseSections(data []byte, section string, emit func(fields map[string]string)) {
	ParseNamedSections(data, []string{section}, func(_ string, fields map[string]string) {
		emit(fields)
	})
}

// ParseNamedSections is like ParseSections but accepts several section names
// and passes the matched section name to emit. Sections may be interleaved;
// emit follows document order.
func ParseNamedSections(data []byte, sections []string, emit func(section string, fields map[string]string)) {
	headers := make(map[string]string, len(sections))
	for _, s := range sections {
		headers["[["+s+"]]"] = s
	}
	var fields map[string]string
	cur := ""
	flush := func() {
		if fields != nil {
			emit(cur, fields)
		}
	}
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, ok := headers[line]; ok {
			flush()
			fields = make(map[string]string)
			cur = name
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			// Unrecognized array-table header: flush the section we were in so
			// its fields cannot leak into the next recognized section.
			flush()
			fields = nil
			cur = ""
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
