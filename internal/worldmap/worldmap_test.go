package worldmap

import (
	"regexp"
	"testing"
)

func TestIDAt(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		lat, lon float64
		want     string
	}{
		{"Paris", 48.86, 2.35, "FR"},
		{"Birmingham", 52.5, -1.9, "GB"},
		{"Kansas", 38.5, -98.0, "US"},
		{"Winnipeg", 49.9, -97.1, "CA"},
		{"Brasilia", -15.8, -47.9, "BR"},
		{"Alice Springs", -23.7, 133.9, "AU"},
		{"Nagano", 36.6, 138.2, "JP"},
		{"Moscow", 55.75, 37.6, "RU"},
		{"Xian", 34.3, 108.9, "CN"},
		{"Delhi", 28.6, 77.2, "IN"},
		{"Nairobi", -1.29, 36.8, "KE"},
		{"Cairo", 30.0, 31.2, "EG"},
		{"Mexico City", 19.4, -99.1, "MX"},
		{"Lillehammer", 61.1, 10.5, "NO"},
		{"Berlin", 52.5, 13.4, "DE"},
		{"Iceland interior", 64.8, -18.5, "IS"},
		{"Antarctica", -80, 0, "AQ"},
		{"Kosovo", 42.6, 20.9, "XK"},
		{"mid Pacific", 0, -160, ""},
		{"south of Africa", -40, 20, ""},
		{"north Atlantic", 45, -35, ""},
		{"north pole", 90, 0, ""},
		{"wrapped longitude", 38.5, -98.0 + 360, "US"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(m.IDAt(tt.lat, tt.lon)); got != tt.want {
				t.Errorf("IDAt(%v, %v) = %q, want %q", tt.lat, tt.lon, got, tt.want)
			}
		})
	}
}

func TestCentroidsSitOnTheirCountry(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	drawn := make(map[string]bool)
	for _, id := range m.cells {
		drawn[Code(id)] = true
	}
	checked := 0
	for code := range centroids {
		if !drawn[code] {
			continue // too small for the atlas; marker comes from tzdata
		}
		lat, lon, ok := Centroid(code)
		if !ok {
			t.Fatalf("Centroid(%q) not ok", code)
		}
		if got := Code(m.IDAt(lat, lon)); got != code {
			t.Errorf("centroid of %s (%v, %v) lies in %q", code, lat, lon, got)
		}
		checked++
	}
	if checked < 150 {
		t.Errorf("only %d drawn countries checked", checked)
	}
}

func TestCentroidFallbacks(t *testing.T) {
	for _, code := range []string{"SG", "HK", "MT", "LU", "BH"} {
		lat, lon, ok := Centroid(code)
		if !ok || lat == 0 && lon == 0 {
			t.Errorf("Centroid(%q) = %v, %v, %t; want a tzdata position", code, lat, lon, ok)
		}
	}
	if _, _, ok := Centroid("ZZ"); ok {
		t.Error("Centroid(ZZ) ok, want unknown")
	}
}

func TestIDRoundTrip(t *testing.T) {
	alpha2 := regexp.MustCompile(`^[A-Z]{2}$`)
	for i, code := range codes {
		if i == 0 {
			if code != "" {
				t.Fatalf("codes[0] = %q, want sea", code)
			}
			continue
		}
		if !alpha2.MatchString(code) {
			t.Errorf("codes[%d] = %q, want alpha-2", i, code)
		}
		if got := ID(code); int(got) != i {
			t.Errorf("ID(%q) = %d, want %d", code, got, i)
		}
	}
	if ID("") != 0 || ID("ZZ") != 0 {
		t.Error("ID of sea or unknown code should be 0")
	}
	if Code(255) != "" {
		t.Error("Code(255) should be empty for an out-of-range index")
	}
}
