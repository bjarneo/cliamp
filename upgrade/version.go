package upgrade

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// version is a parsed SemVer release tag such as v1.5.0 or v1.5.0-rc.1.
type version struct {
	core [3]uint64
	pre  []string // prerelease identifiers, empty for a stable release
}

// parseVersion parses a vMAJOR.MINOR.PATCH tag with an optional prerelease
// and build suffix. The leading v is optional. Build metadata does not
// change the order, so parseVersion drops it.
func parseVersion(tag string) (version, bool) {
	s, _, _ := strings.Cut(strings.TrimPrefix(tag, "v"), "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		if !isNumeric(p) {
			return version{}, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return version{}, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		if slices.Contains(v.pre, "") {
			return version{}, false
		}
	}
	return v, true
}

// compare returns -1, 0 or +1 by SemVer precedence. A stable release ranks
// above its own prereleases.
func (v version) compare(w version) int {
	if c := slices.Compare(v.core[:], w.core[:]); c != 0 {
		return c
	}
	switch {
	case len(v.pre) == 0 && len(w.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(w.pre) == 0:
		return -1
	}
	return slices.CompareFunc(v.pre, w.pre, comparePrerelease)
}

// comparePrerelease compares two prerelease identifiers. Numeric identifiers
// compare by value and rank below alphanumeric ones.
func comparePrerelease(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

func isNumeric(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// notNewer reports whether tag is the same version as current or an older
// one. It is false when either is not a SemVer version.
func notNewer(tag, current string) bool {
	t, ok := parseVersion(tag)
	c, cok := parseVersion(current)
	return ok && cok && t.compare(c) <= 0
}
