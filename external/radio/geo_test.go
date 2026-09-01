package radio

import "testing"

func TestNormalizeCountryCode(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"NO", "NO"},
		{"no", "NO"},
		{" de ", "DE"},
		{"XX", ""}, // the directory's placeholder for "unknown"
		{"", ""},
		{"NOR", ""},
		{"N", ""},
		{"N1", ""},
	} {
		if got := normalizeCountryCode(tc.in); got != tc.want {
			t.Errorf("normalizeCountryCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegion(t *testing.T) {
	for _, tc := range []struct {
		code, want string
	}{
		{"NO", "Europe"},
		{"no", "Europe"}, // lowercase duplicates exist in the directory
		{"US", "Americas"},
		{"BR", "Americas"},
		{"JP", "Asia"},
		{"KE", "Africa"},
		{"NZ", "Oceania"},
		{"XK", "Europe"},  // Kosovo has no zone.tab row of its own
		{"IS", "Europe"},  // Atlantic/Reykjavik
		{"MU", "Africa"},  // Indian/Mauritius
		{"MV", "Asia"},    // Indian/Maldives
		{"RU", "Europe"},  // transcontinental, most population in Europe
		{"AU", "Oceania"}, // Australia/* plus an Antarctica/* row
		{"ZZ", ""},        // not a country
		{"", ""},
	} {
		if got := Region(tc.code); got != tc.want {
			t.Errorf("Region(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestRegionCoversEveryZoneCountry(t *testing.T) {
	for zone, code := range zoneIndex() {
		if Region(code) == "" {
			t.Errorf("zone %s has country %s with no region", zone, code)
		}
	}
}

func TestZoneFromPath(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"/usr/share/zoneinfo/Europe/Oslo", "Europe/Oslo"},
		{"../usr/share/zoneinfo/America/New_York", "America/New_York"},
		{"/var/db/timezone/zoneinfo/Asia/Tokyo", "Asia/Tokyo"},
		{"/usr/share/zoneinfo/posix/Europe/Berlin", "Europe/Berlin"},
		{"/etc/localtime", ""},
		{"", ""},
	} {
		if got := zoneFromPath(tc.in); got != tc.want {
			t.Errorf("zoneFromPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCountryFromZone(t *testing.T) {
	for _, tc := range []struct {
		zone, want string
	}{
		{"Europe/Oslo", "NO"},
		{"America/New_York", "US"},
		{"Asia/Tokyo", "JP"},
		{" Europe/Berlin ", "DE"},
		{"Mars/Olympus", ""},
		{"", ""},
	} {
		if got := countryFromZone(tc.zone); got != tc.want {
			t.Errorf("countryFromZone(%q) = %q, want %q", tc.zone, got, tc.want)
		}
	}
}

func TestCountryFromLocale(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"nb_NO.UTF-8", "NO"},
		{"en_US", "US"},
		{"de_DE.UTF-8@euro", "DE"},
		{"pt_BR.iso88591", "BR"},
		{"en", ""}, // a language with no territory
		{"C", ""},
		{"", ""},
	} {
		if got := countryFromLocale(tc.in); got != tc.want {
			t.Errorf("countryFromLocale(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCurrentLocaleSkipsPlaceholderLocales(t *testing.T) {
	for _, tc := range []struct {
		name               string
		lcAll, lcMsg, lang string
		want               string
	}{
		{name: "LC_ALL wins", lcAll: "fr_FR.UTF-8", lcMsg: "nb_NO.UTF-8", lang: "en_US.UTF-8", want: "fr_FR.UTF-8"},
		{name: "LC_MESSAGES over LANG", lcMsg: "nb_NO.UTF-8", lang: "en_US.UTF-8", want: "nb_NO.UTF-8"},
		{name: "C is not a locale", lcAll: "C", lang: "nb_NO.UTF-8", want: "nb_NO.UTF-8"},
		{name: "C.UTF-8 is not a locale", lcAll: "C.UTF-8", lang: "nb_NO.UTF-8", want: "nb_NO.UTF-8"},
		{name: "nothing set", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.lcAll)
			t.Setenv("LC_MESSAGES", tc.lcMsg)
			t.Setenv("LANG", tc.lang)
			if got := currentLocale(); got != tc.want {
				t.Errorf("currentLocale() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The timezone is checked before the locale on purpose: a desktop left on
// en_US.UTF-8 while sitting in Europe/Oslo is the common case, and trusting
// the locale there would hand most users the wrong country.
func TestDetectHomeCountryPrefersTimezoneOverLocale(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tz, locale string
		want       string
	}{
		{name: "timezone beats a mismatched locale", tz: "Europe/Oslo", locale: "en_US.UTF-8", want: "NO"},
		{name: "locale fills in when the zone is unknown", tz: "Mars/Olympus", locale: "nb_NO.UTF-8", want: "NO"},
		{name: "both agree", tz: "Asia/Tokyo", locale: "ja_JP.UTF-8", want: "JP"},
		{name: "neither resolves", tz: "Mars/Olympus", locale: "C", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TZ", tc.tz)
			t.Setenv("LC_ALL", tc.locale)
			t.Setenv("LC_MESSAGES", "")
			t.Setenv("LANG", "")
			if got := DetectHomeCountry(); got != tc.want {
				t.Errorf("DetectHomeCountry() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCountryName(t *testing.T) {
	for _, tc := range []struct {
		code, want string
	}{
		{"NO", "Norway"},
		{"no", "Norway"},
		{"US", "United States"},
		{"JP", "Japan"},
		{"XX", ""},
		{"", ""},
		{"ZZ", ""},
	} {
		if got := CountryName(tc.code); got != tc.want {
			t.Errorf("CountryName(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

// Every country the region table knows must also have a name, or the pane can
// print a bare code where a country belongs.
func TestEveryRegionCountryHasAName(t *testing.T) {
	for code := range regionIndex() {
		if CountryName(code) == "" {
			t.Errorf("country %s has a region but no name", code)
		}
	}
}

func TestDisplayStateName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"oslo", "Oslo"},
		{"Oslo", "Oslo"},
		{"  rogaland  ", "Rogaland"},
		{"Møre og Romsdal", "Møre og Romsdal"}, // already capitalized: left alone
		{"østfold", "Østfold"},                 // the leading rune is not ASCII
		{"", ""},
	} {
		if got := displayStateName(tc.in); got != tc.want {
			t.Errorf("displayStateName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDisplayCountryName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"The United States Of America", "United States Of America"},
		{"Norway", "Norway"},
		{"  Japan  ", "Japan"},
		{"Theodora Islands", "Theodora Islands"}, // only the standalone article is trimmed
		{"", ""},
	} {
		if got := displayCountryName(tc.in); got != tc.want {
			t.Errorf("displayCountryName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
