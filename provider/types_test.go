package provider

import "testing"

func TestYearFromDate(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"2021-05-14", 2021},
		{"1999", 1999},
		{"", 0},
		{"abc", 0},
		{"20", 0},
		{"19xy-01-01", 0},
	}
	for _, tt := range tests {
		if got := YearFromDate(tt.in); got != tt.want {
			t.Errorf("YearFromDate(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
