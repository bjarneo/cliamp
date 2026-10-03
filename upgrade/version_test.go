package upgrade

import "testing"

func TestVersionCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{a: "v1.2.3", b: "v1.2.3", want: 0},
		{a: "v1.2.3", b: "1.2.3", want: 0},
		{a: "v1.2.3+build.5", b: "v1.2.3", want: 0},
		{a: "v1.10.0", b: "v1.9.0", want: 1},
		{a: "v2.0.0", b: "v2.0.0-rc.1", want: 1},
		{a: "v2.0.0-rc.1", b: "v1.9.9", want: 1},
		{a: "v2.0.0-rc.10", b: "v2.0.0-rc.2", want: 1},
		{a: "v2.0.0-rc.1", b: "v2.0.0-beta.9", want: 1},
		{a: "v2.0.0-alpha", b: "v2.0.0-alpha.1", want: -1},
		{a: "v2.0.0-alpha.1", b: "v2.0.0-alpha.beta", want: -1},
		{a: "v2.0.0-1", b: "v2.0.0-alpha", want: -1},
	} {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			a, ok := parseVersion(tc.a)
			if !ok {
				t.Fatalf("parseVersion(%q) failed", tc.a)
			}
			b, ok := parseVersion(tc.b)
			if !ok {
				t.Fatalf("parseVersion(%q) failed", tc.b)
			}
			if got := a.compare(b); got != tc.want {
				t.Errorf("compare = %d, want %d", got, tc.want)
			}
			if got := b.compare(a); got != -tc.want {
				t.Errorf("reverse compare = %d, want %d", got, -tc.want)
			}
		})
	}
}

func TestParseVersionRejects(t *testing.T) {
	for _, tag := range []string{"", "dev", "nightly", "v1.2", "v1.2.3.4", "v1.x.3", "v1.2.3-", "v1.2.3-rc..1", "v-1.2.3"} {
		if _, ok := parseVersion(tag); ok {
			t.Errorf("parseVersion(%q) succeeded, want failure", tag)
		}
	}
}

func TestNotNewer(t *testing.T) {
	for _, tc := range []struct {
		tag, current string
		want         bool
	}{
		{tag: "v2.3.0", current: "v2.3.0", want: true},
		{tag: "v2.0.0-rc.2", current: "v2.3.0", want: true},
		{tag: "v2.3.0", current: "v2.4.0-beta.1", want: true},
		{tag: "v2.4.0", current: "v2.4.0-rc.2", want: false},
		{tag: "v2.3.0", current: "dev", want: false},
		{tag: "nightly", current: "v2.3.0", want: false},
	} {
		if got := notNewer(tc.tag, tc.current); got != tc.want {
			t.Errorf("notNewer(%q, %q) = %v, want %v", tc.tag, tc.current, got, tc.want)
		}
	}
}
