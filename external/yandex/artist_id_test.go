package yandex

import (
	"encoding/json"
	"testing"
)

func TestArtistIDStringOrNumber(t *testing.T) {
	for _, input := range []string{`{"id":"123456","name":"Artist"}`, `{"id":123456,"name":"Artist"}`} {
		var got artist
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("decode %s: %v", input, err)
		}
		if got.ID != "123456" || got.Name != "Artist" {
			t.Fatalf("unexpected artist: %+v", got)
		}
	}
}
