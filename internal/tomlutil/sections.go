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
	var lines lineScanner
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if !lines.next(line) || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			flush()
			fields = nil
			cur = ""
			if name, ok := headers[tableHeader(line)]; ok {
				fields = make(map[string]string)
				cur = name
			}
			// An unrecognized or malformed header still ends the section we
			// were in, so its fields cannot leak into the previous section.
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

// UnknownHeaders returns the table headers in data that ParseNamedSections
// would skip for the given section names, in document order: other names such
// as [[stations]] or [station], and malformed ones such as [[station. Callers
// use it to warn about sections that are ignored.
func UnknownHeaders(data []byte, sections ...string) []string {
	known := make(map[string]bool, len(sections))
	for _, s := range sections {
		known["[["+s+"]]"] = true
	}
	var unknown []string
	var lines lineScanner
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if !lines.next(line) || !strings.HasPrefix(line, "[") {
			continue
		}
		if header := tableHeader(line); !known[header] {
			unknown = append(unknown, header)
		}
	}
	return unknown
}

// tableHeader returns a header line such as [[station]] # note without its
// trailing comment. A # inside a quoted key is part of the key.
func tableHeader(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

// lineScanner tracks multi-line strings and arrays, so that a line inside one,
// such as ["Jazz"] in an array or [Live] in a string, is not read as a header.
type lineScanner struct {
	closer string // closing delimiter of an open multi-line string
	depth  int    // brackets still open in a multi-line array
}

// next reports whether line starts outside any multi-line value, then
// consumes it. line must already be trimmed.
func (s *lineScanner) next(line string) bool {
	outside := s.closer == "" && s.depth == 0
	if outside && strings.HasPrefix(line, "[") {
		// A header line; its brackets open no array.
		return true
	}
	for i := 0; i < len(line); i++ {
		if s.closer != "" {
			// In a """ string a backslash escapes the next character, so
			// \""" is a quote followed by "" and does not close it.
			for ; i < len(line) && !strings.HasPrefix(line[i:], s.closer); i++ {
				if s.closer == `"""` && line[i] == '\\' {
					i++
				}
			}
			if i >= len(line) {
				return outside
			}
			i += len(s.closer) - 1
			s.closer = ""
			continue
		}
		switch c := line[i]; c {
		case '#':
			return outside
		case '[':
			s.depth++
		case ']':
			if s.depth > 0 {
				s.depth--
			}
		case '"', '\'':
			if strings.HasPrefix(line[i:], strings.Repeat(string(c), 3)) {
				s.closer = strings.Repeat(string(c), 3)
				i += 2
				continue
			}
			// A single-line string: skip to its closing quote.
			for i++; i < len(line) && line[i] != c; i++ {
				if c == '"' && line[i] == '\\' {
					i++
				}
			}
		}
	}
	return outside
}
