package playlist

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// riffChunk returns a RIFF chunk with its pad byte.
func riffChunk(id string, data []byte) []byte {
	out := append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(data)))...)
	out = append(out, data...)
	if len(data)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

// infoList returns a LIST INFO chunk with the fields in id, value pairs.
// Each value ends with a NUL, as RIFF writers store it.
func infoList(listType string, fields ...string) []byte {
	data := []byte(listType)
	for i := 0; i+1 < len(fields); i += 2 {
		data = append(data, riffChunk(fields[i], append([]byte(fields[i+1]), 0))...)
	}
	return riffChunk("LIST", data)
}

// wavFile returns a WAVE file that holds chunks.
func wavFile(chunks ...[]byte) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}
	out := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(4+len(body)))...)
	out = append(out, "WAVE"...)
	return append(out, body...)
}

// A WAV file gives the title, artist, album, genre, year and track number
// of its LIST INFO chunk. A file with no INFO title falls back to its file
// name.
func TestReadTagsWAVInfo(t *testing.T) {
	format := riffChunk("fmt ", make([]byte, 16))
	samples := riffChunk("data", make([]byte, 7))
	full := infoList("INFO",
		"INAM", "Charlie Tone", "IART", "Smoke Artist", "IPRD", "Smoke Album",
		"IGNR", "Test", "ICRD", "2024-05-01", "ITRK", "3/12")
	tests := []struct {
		name string
		data []byte
		want Track
	}{
		{
			name: "INFO after the samples",
			data: wavFile(format, samples, full),
			want: Track{Title: "Charlie Tone", Artist: "Smoke Artist", Album: "Smoke Album", Genre: "Test", Year: 2024, TrackNumber: 3},
		},
		{
			name: "INFO before the samples",
			data: wavFile(format, full, samples),
			want: Track{Title: "Charlie Tone", Artist: "Smoke Artist", Album: "Smoke Album", Genre: "Test", Year: 2024, TrackNumber: 3},
		},
		{
			name: "another LIST comes first",
			data: wavFile(format, infoList("adtl", "labl", "cue"), infoList("INFO", "INAM", "Title", "IPRT", "7")),
			want: Track{Title: "Title", TrackNumber: 7},
		},
		{
			name: "Latin-1 value",
			data: wavFile(format, infoList("INFO", "INAM", "Song", "IART", "Bj\xf6rk")),
			want: Track{Title: "Song", Artist: "Björk"},
		},
		{
			name: "no INFO",
			data: wavFile(format, samples),
			want: Track{Title: "03-charlie"},
		},
		{
			name: "INFO with no title",
			data: wavFile(format, infoList("INFO", "IART", "Smoke Artist")),
			want: Track{Title: "03-charlie"},
		},
		{
			name: "cut INFO",
			data: wavFile(format, infoList("INFO", "INAM", "Charlie Tone"))[:40],
			want: Track{Title: "03-charlie"},
		},
		{
			name: "not a WAV file",
			data: []byte("not a riff file at all"),
			want: Track{Title: "03-charlie"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "03-charlie.wav")
			if err := os.WriteFile(path, tt.data, 0o644); err != nil {
				t.Fatal(err)
			}
			tt.want.Path = path
			if got := readTags(path); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("readTags = %+v, want %+v", got, tt.want)
			}
		})
	}
}
