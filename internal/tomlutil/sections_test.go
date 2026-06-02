package tomlutil

import (
	"reflect"
	"testing"
)

func TestParseSections(t *testing.T) {
	data := []byte(`
# a comment
stray = "ignored before any section"

[[station]]
name = "Radio A"
url = "http://a"
bitrate = "128"

[[station]]
name = "Radio B"
url = "http://b"
`)

	var got []map[string]string
	ParseSections(data, "station", func(f map[string]string) {
		got = append(got, f)
	})

	want := []map[string]string{
		{"name": "Radio A", "url": "http://a", "bitrate": "128"},
		{"name": "Radio B", "url": "http://b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSections = %v, want %v", got, want)
	}
}

func TestParseSectionsLastKeyWins(t *testing.T) {
	data := []byte("[[t]]\nk = \"first\"\nk = \"second\"\n")
	var got string
	ParseSections(data, "t", func(f map[string]string) { got = f["k"] })
	if got != "second" {
		t.Fatalf("k = %q, want second", got)
	}
}

func TestParseSectionsEmptySectionEmits(t *testing.T) {
	data := []byte("[[t]]\n[[t]]\nk = \"v\"\n")
	count := 0
	ParseSections(data, "t", func(map[string]string) { count++ })
	if count != 2 {
		t.Fatalf("emit count = %d, want 2", count)
	}
}
