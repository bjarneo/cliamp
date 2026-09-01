package radio

//go:generate go run geodata_gen.go

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// Region returns the broad region a country code belongs to ("Europe",
// "Americas", …), or "" when the code is unknown. Codes are matched
// case-insensitively: the Radio Browser directory holds a handful of
// lowercase duplicates such as "de" next to "DE".
func Region(code string) string {
	return regionIndex()[normalizeCountryCode(code)]
}

var regionIndex = sync.OnceValue(func() map[string]string {
	index := make(map[string]string, 256)
	for region, codes := range regionCodes {
		for code := range strings.FieldsSeq(codes) {
			index[code] = region
		}
	}
	return index
})

var zoneIndex = sync.OnceValue(func() map[string]string {
	index := make(map[string]string, 448)
	for line := range strings.SplitSeq(strings.TrimSpace(zoneCountries), "\n") {
		zone, code, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		index[zone] = normalizeCountryCode(code)
	}
	return index
})

// normalizeCountryCode upper-cases a country code and rejects anything that is
// not two ASCII letters. "XX" is the directory's placeholder for "unknown" and
// is treated as absent.
func normalizeCountryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 || code == "XX" {
		return ""
	}
	for i := range 2 {
		if code[i] < 'A' || code[i] > 'Z' {
			return ""
		}
	}
	return code
}

// DetectHomeCountry guesses the listener's country as an ISO 3166-1 alpha-2
// code, returning "" when it cannot tell.
//
// The timezone is consulted before the locale, and deliberately so: a desktop
// set to en_US.UTF-8 in Europe/Oslo is the common case, not the exception, so
// trusting the locale first would hand most users the wrong country. The
// timezone is the setting people actually keep accurate.
//
// Nothing here touches the network. A geo-IP lookup would be more precise and
// would also mean telling a third party where the user lives every launch,
// which is not a trade this feature is worth.
func DetectHomeCountry() string {
	if code := countryFromZone(currentZone()); code != "" {
		return code
	}
	return countryFromLocale(currentLocale())
}

// currentZone resolves the IANA timezone name from TZ, the /etc/localtime
// symlink, or /etc/timezone. Go's time.Local reports itself as "Local" when TZ
// is unset, so the name has to be recovered from the filesystem.
func currentZone() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if zone := zoneFromPath(target); zone != "" {
			return zone
		}
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// zoneFromPath extracts "Europe/Oslo" from a zoneinfo path such as
// /usr/share/zoneinfo/Europe/Oslo or ../usr/share/zoneinfo/Europe/Oslo.
func zoneFromPath(path string) string {
	path = filepath.ToSlash(path)
	_, zone, ok := strings.Cut(path, "zoneinfo/")
	if !ok {
		return ""
	}
	// Some distributions symlink through a posix/ or right/ subtree.
	for _, prefix := range []string{"posix/", "right/"} {
		zone = strings.TrimPrefix(zone, prefix)
	}
	return zone
}

func countryFromZone(zone string) string {
	return zoneIndex()[strings.TrimSpace(zone)]
}

// currentLocale returns the first locale setting that names a real locale.
// POSIX ranks LC_ALL over the category over LANG.
func currentLocale() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.TrimSpace(os.Getenv(key))
		switch value {
		case "", "C", "POSIX", "C.UTF-8", "C.utf8":
			continue
		}
		return value
	}
	return ""
}

// countryFromLocale pulls NO out of "nb_NO.UTF-8". A locale without a
// territory ("en", "nb") names a language only and yields "".
func countryFromLocale(locale string) string {
	_, rest, ok := strings.Cut(locale, "_")
	if !ok {
		return ""
	}
	// Trim the codeset and any @modifier: nb_NO.UTF-8@euro.
	rest, _, _ = strings.Cut(rest, ".")
	rest, _, _ = strings.Cut(rest, "@")
	return normalizeCountryCode(rest)
}

// CountryName returns a readable name for a country code without a network
// call, or "" when the code is unknown. The directory's own country index has
// better names for a few entries, so it wins where it is loaded; this is what
// the pane shows before then.
func CountryName(code string) string {
	return countryNames[normalizeCountryCode(code)]
}

// displayStateName capitalizes an all-lowercase region name ("oslo" becomes
// "Oslo"). Names that already carry capitals are left alone, so local spellings
// such as "Møre og Romsdal" survive intact.
func displayStateName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.ToLower(name) != name {
		return name
	}
	r := []rune(name)
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// displayCountryName trims the directory's officious country names down to
// what a list can show: "The United States Of America" becomes "United States
// Of America", which the width budget can then truncate sensibly.
func displayCountryName(name string) string {
	name = strings.TrimSpace(name)
	if rest, ok := strings.CutPrefix(name, "The "); ok {
		return rest
	}
	return name
}
