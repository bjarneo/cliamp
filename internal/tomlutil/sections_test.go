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

func TestParseNamedSectionsInterleavedOrder(t *testing.T) {
	data := []byte(`[[dir]]
path = "/music"
recursive = "false"

[[track]]
path = "/a.mp3"
title = "A"

[[dir]]
path = "$EXTRA_DIR"

[[track]]
path = "/b.mp3"
title = "B"
`)
	t.Setenv("EXTRA_DIR", "/extra")

	var got []string
	ParseNamedSections(data, []string{"track", "dir"}, func(section string, f map[string]string) {
		switch section {
		case "track":
			got = append(got, "track:"+f["path"])
		case "dir":
			got = append(got, "dir:"+f["path"]+" rec="+f["recursive"])
		}
	})

	want := []string{
		"dir:/music rec=false",
		"track:/a.mp3",
		"dir:$EXTRA_DIR rec=",
		"track:/b.mp3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseNamedSections = %v, want %v", got, want)
	}
}

func TestParseNamedSectionsUnknownHeaderIgnored(t *testing.T) {
	data := []byte(`[[unknown]]
path = "/x"

[[track]]
path = "/a.mp3"
`)
	count := 0
	ParseNamedSections(data, []string{"track"}, func(_ string, _ map[string]string) { count++ })
	if count != 1 {
		t.Fatalf("emit count = %d, want 1", count)
	}
}

func TestParseNamedSectionsUnknownHeaderDoesNotLeakFields(t *testing.T) {
	data := []byte(`[[track]]
path = "/a.mp3"

[[unknown]]
title = "leaked"

[[track]]
path = "/b.mp3"
title = "B"
`)
	var got []string
	ParseNamedSections(data, []string{"track"}, func(section string, f map[string]string) {
		got = append(got, section+":"+f["path"]+":"+f["title"])
	})
	want := []string{
		"track:/a.mp3:",
		"track:/b.mp3:B",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseNamedSections = %v, want %v (unknown section fields leaked)", got, want)
	}
}
