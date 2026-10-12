package replaygain

import (
	"math"
	"testing"

	"github.com/dhowden/tag"
)

func TestGain(t *testing.T) {
	track := Values{TrackGain: -9.92, TrackPeak: 0.5, HasTrack: true}
	album := Values{AlbumGain: -7, AlbumPeak: 0.25, HasAlbum: true}
	both := Values{TrackGain: -9.92, TrackPeak: 0.5, AlbumGain: -7, AlbumPeak: 0.25, HasTrack: true, HasAlbum: true}
	tests := []struct {
		name   string
		v      Values
		mode   string
		preamp float64
		want   float64
		wantOK bool
	}{
		{"off", both, ModeOff, 0, 0, false},
		{"unknown mode", both, "loud", 0, 0, false},
		{"no values", Values{}, ModeTrack, 0, 0, false},
		{"track", both, ModeTrack, 0, -9.92, true},
		{"album", both, ModeAlbum, 0, -7, true},
		{"track falls back to album", album, ModeTrack, 0, -7, true},
		{"album falls back to track", track, ModeAlbum, 0, -9.92, true},
		{"preamp adds", track, ModeTrack, 3, -6.92, true},
		// Peak 0.5 allows at most +6.02 dB before clipping.
		{"peak limits a boost", Values{TrackGain: 10, TrackPeak: 0.5, HasTrack: true}, ModeTrack, 0, 20 * math.Log10(2), true},
		{"unknown peak does not limit", Values{TrackGain: 4, HasTrack: true}, ModeTrack, 0, 4, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, ok := tc.v.GainFrom(tc.mode, tc.preamp)
			if ok != tc.wantOK || math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Gain(%q, %v) = %v, %v; want %v, %v", tc.mode, tc.preamp, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestFromTags(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]interface{}
		want Values
	}{
		{
			name: "ID3v2 TXXX frames",
			raw: map[string]interface{}{
				"TXXX":   &tag.Comm{Description: "REPLAYGAIN_TRACK_GAIN", Text: "-6.54 dB"},
				"TXXX_0": &tag.Comm{Description: "REPLAYGAIN_TRACK_PEAK", Text: "0.988525"},
				"TXXX_1": &tag.Comm{Description: "replaygain_album_gain", Text: "+1.20 dB"},
				"TXXX_2": &tag.Comm{Description: "MusicBrainz Album Id", Text: "x"},
				"COMM":   &tag.Comm{Description: "REPLAYGAIN_TRACK_GAIN", Text: "-99 dB"},
			},
			want: Values{TrackGain: -6.54, TrackPeak: 0.988525, AlbumGain: 1.2, HasTrack: true, HasAlbum: true},
		},
		{
			// ffmpeg and other taggers end each ID3v2.3 text value in a NUL.
			name: "NUL-terminated TXXX values",
			raw: map[string]interface{}{
				"TXXX":   &tag.Comm{Description: "replaygain_track_gain", Text: "-6.73 dB\x00"},
				"TXXX_0": &tag.Comm{Description: "replaygain_track_peak", Text: "0.999969\x00"},
			},
			want: Values{TrackGain: -6.73, TrackPeak: 0.999969, HasTrack: true},
		},
		{
			name: "Vorbis comments",
			raw:  map[string]interface{}{"replaygain_track_gain": "-8.3 dB", "replaygain_track_peak": "1.02"},
			want: Values{TrackGain: -8.3, TrackPeak: 1.02, HasTrack: true},
		},
		{
			name: "no unit",
			raw:  map[string]interface{}{"replaygain_album_gain": "-3.5"},
			want: Values{AlbumGain: -3.5, HasAlbum: true},
		},
		{
			name: "Opus R128 converted to the ReplayGain reference",
			raw:  map[string]interface{}{"r128_track_gain": "-1280"},
			want: Values{TrackGain: 0, HasTrack: true},
		},
		{
			name: "ReplayGain wins over R128",
			raw:  map[string]interface{}{"r128_track_gain": "-1280", "replaygain_track_gain": "-2 dB"},
			want: Values{TrackGain: -2, HasTrack: true},
		},
		{
			name: "unparseable gain is absent",
			raw:  map[string]interface{}{"replaygain_track_gain": "loud"},
			want: Values{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromTags(tc.raw); got != tc.want {
				t.Errorf("FromTags = %+v, want %+v", got, tc.want)
			}
		})
	}
}
