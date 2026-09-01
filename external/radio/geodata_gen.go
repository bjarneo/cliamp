//go:build ignore

// Command geodata_gen regenerates geodata.go from the system's tzdata.
//
// Usage: go generate ./external/radio
//
// It reads /usr/share/zoneinfo/zone.tab, whose first column is exactly one
// ISO 3166-1 alpha-2 country code and whose third is an IANA timezone name,
// and emits two tables: which country each timezone belongs to, and which
// broad region each country sits in.
package main

import (
	"bufio"
	"fmt"
	"go/format"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
)

const (
	zoneTab    = "/usr/share/zoneinfo/zone.tab"
	iso3166Tab = "/usr/share/zoneinfo/iso3166.tab"
)

// areaRegions maps a timezone's leading area to a region. Atlantic, Indian,
// and Arctic are missing on purpose: they are oceans, and every country under
// them is resolved by overrides instead.
var areaRegions = map[string]string{
	"Africa":     "Africa",
	"America":    "Americas",
	"Antarctica": "Antarctica",
	"Asia":       "Asia",
	"Australia":  "Oceania",
	"Europe":     "Europe",
	"Pacific":    "Oceania",
}

// overrides fix countries whose timezone area is an ocean, and
// transcontinental countries, where the region holding most of the population
// wins over the one holding most of the timezones.
var overrides = map[string]string{
	"AU": "Oceania", "BM": "Americas", "CC": "Oceania", "CL": "Americas",
	"CV": "Africa", "CX": "Oceania", "EC": "Americas", "ES": "Europe",
	"FK": "Americas", "FO": "Europe", "GS": "Americas", "IO": "Asia",
	"IS": "Europe", "KM": "Africa", "MG": "Africa", "MU": "Africa",
	"MV": "Asia", "PT": "Europe", "RE": "Africa", "RU": "Europe",
	"SC": "Africa", "SH": "Africa", "SJ": "Europe", "TF": "Antarctica",
	"US": "Americas", "YT": "Africa",
}

// extra covers countries tzdata has no row for, in either table. Kosovo is
// filed under Europe/Belgrade, but the radio directory lists it separately.
var extra = map[string]struct{ region, name string }{
	"XK": {region: "Europe", name: "Kosovo"},
}

// regionOrder keeps the emitted table stable across runs.
var regionOrder = []string{"Africa", "Americas", "Asia", "Europe", "Oceania", "Antarctica"}

func main() {
	zones, areas, err := readZoneTab(zoneTab)
	if err != nil {
		log.Fatal(err)
	}
	names, err := readISO3166(iso3166Tab)
	if err != nil {
		log.Fatal(err)
	}

	regions := map[string][]string{}
	for code, byArea := range areas {
		region, ok := overrides[code]
		if !ok {
			region, ok = areaRegions[topArea(byArea)]
		}
		if !ok {
			log.Fatalf("%s: no region for timezone areas %v; add an override", code, slices.Sorted(maps.Keys(byArea)))
		}
		regions[region] = append(regions[region], code)
	}
	for code, e := range extra {
		regions[e.region] = append(regions[e.region], code)
		names[code] = e.name
	}

	if err := write("geodata.go", zones, regions, names); err != nil {
		log.Fatal(err)
	}
}

// readZoneTab returns the zone-to-country pairs and, per country, how many
// zones it has in each timezone area.
func readZoneTab(path string) (zones [][2]string, areas map[string]map[string]int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s (is tzdata installed?): %w", path, err)
	}
	defer f.Close()

	areas = map[string]map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		code, zone := fields[0], fields[2]
		area, _, _ := strings.Cut(zone, "/")
		zones = append(zones, [2]string{zone, code})
		if areas[code] == nil {
			areas[code] = map[string]int{}
		}
		areas[code][area]++
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return zones, areas, nil
}

// topArea returns the area holding the most of a country's zones, breaking
// ties by name so the output does not change between runs.
func topArea(byArea map[string]int) string {
	best := ""
	for _, area := range slices.Sorted(maps.Keys(byArea)) {
		if best == "" || byArea[area] > byArea[best] {
			best = area
		}
	}
	return best
}

// readISO3166 returns the short country name tzdata publishes for each code.
func readISO3166(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s (is tzdata installed?): %w", path, err)
	}
	defer f.Close()

	names := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		code, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		names[strings.TrimSpace(code)] = strings.TrimSpace(name)
	}
	return names, scanner.Err()
}

func write(path string, zones [][2]string, regions map[string][]string, names map[string]string) error {
	var b strings.Builder
	b.WriteString("// Code generated from tzdata zone.tab; DO NOT EDIT.\n")
	b.WriteString("// Regenerate with: go generate ./external/radio\n\n")
	b.WriteString("package radio\n\n")
	b.WriteString("// regionCodes groups ISO 3166-1 alpha-2 country codes into the six broad\n")
	b.WriteString("// regions the country list is grouped by. Derived from the timezone area of\n")
	b.WriteString("// each country's zone.tab rows, with hand overrides where that area is an\n")
	b.WriteString("// ocean (Atlantic, Indian, Arctic) rather than a region, and for\n")
	b.WriteString("// transcontinental countries, where the region holding most of the\n")
	b.WriteString("// population wins.\n")
	b.WriteString("var regionCodes = map[string]string{\n")
	for _, region := range regionOrder {
		codes := regions[region]
		slices.Sort(codes)
		fmt.Fprintf(&b, "\t%q: %q,\n", region, strings.Join(codes, " "))
	}
	b.WriteString("}\n\n")
	b.WriteString("// zoneCountries maps IANA timezone names to the ISO 3166-1 alpha-2 code of\n")
	b.WriteString("// the country that owns them, one \"Zone CC\" pair per line. Taken from\n")
	b.WriteString("// tzdata's zone.tab, whose first column is exactly one country code.\n")
	b.WriteString("const zoneCountries = `\n")
	slices.SortFunc(zones, func(a, c [2]string) int { return strings.Compare(a[0], c[0]) })
	for _, z := range zones {
		fmt.Fprintf(&b, "%s %s\n", z[0], z[1])
	}
	b.WriteString("`\n\n")
	b.WriteString("// countryNames gives every country code a readable name without a network\n")
	b.WriteString("// call, so the pane can say \"Norway\" before the directory's own country\n")
	b.WriteString("// index has loaded. Taken from tzdata's iso3166.tab, whose names are\n")
	b.WriteString("// deliberately short (\"Britain (UK)\", not the directory's full legal title).\n")
	b.WriteString("var countryNames = map[string]string{\n")
	for _, code := range slices.Sorted(maps.Keys(names)) {
		fmt.Fprintf(&b, "\t%q: %q,\n", code, names[code])
	}
	b.WriteString("}\n")

	// Format here so `go generate` leaves nothing for `make fmt` to change.
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}
