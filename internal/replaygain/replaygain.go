// Package replaygain reads ReplayGain loudness values and turns them into a
// playback gain.
package replaygain

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

// Modes accepted by GainFrom.
const (
	ModeOff   = "off"
	ModeTrack = "track"
	ModeAlbum = "album"
)

// Values holds a track's ReplayGain data: gains in dB against the ReplayGain
// 2.0 reference, peaks as linear sample amplitude (0 when unknown).
type Values struct {
	TrackGain, TrackPeak float64
	AlbumGain, AlbumPeak float64
	HasTrack, HasAlbum   bool
}

// GainFrom returns the dB adjustment for mode plus preamp, lowered where
// needed so the track's peak stays at or below full scale, and the value it
// used, ModeTrack or ModeAlbum. Track mode falls back to the album gain and
// album mode to the track gain, so used differs from mode when it fell back.
// ok is false when the mode is off or unknown, or the track carries no gain.
func (v Values) GainFrom(mode string, preamp float64) (db float64, used string, ok bool) {
	var peak float64
	switch {
	case mode == ModeTrack && v.HasTrack, mode == ModeAlbum && !v.HasAlbum && v.HasTrack:
		db, peak, used = v.TrackGain, v.TrackPeak, ModeTrack
	case mode == ModeAlbum && v.HasAlbum, mode == ModeTrack && v.HasAlbum:
		db, peak, used = v.AlbumGain, v.AlbumPeak, ModeAlbum
	default:
		return 0, "", false
	}
	db += preamp
	if peak > 0 {
		db = min(db, -20*math.Log10(peak))
	}
	return db, used, true
}

// ReadFile reads the ReplayGain tags of a local audio file.
func ReadFile(path string) Values {
	f, err := os.Open(path)
	if err != nil {
		return Values{}
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil || m == nil {
		return Values{}
	}
	return FromTags(m.Raw())
}

// FromTags extracts ReplayGain from raw tag data as dhowden/tag returns it:
// ID3v2 TXXX frames, Vorbis comments, and MP4 freeform atoms. Opus R128 gains
// are used only when no ReplayGain tag is present.
func FromTags(raw map[string]interface{}) Values {
	var v Values
	var r128Track, r128Album float64
	var hasR128Track, hasR128Album bool
	for key, value := range raw {
		name, text := strings.ToLower(key), ""
		switch val := value.(type) {
		case *tag.Comm:
			if !strings.HasPrefix(name, "txx") {
				continue
			}
			name, text = strings.ToLower(val.Description), val.Text
		case string:
			text = val
		default:
			continue
		}
		// ID3v2 lets a text value end in a NUL terminator, which ffmpeg and
		// other taggers write and the tag reader passes through.
		text = strings.TrimRight(text, "\x00")
		switch name {
		case "replaygain_track_gain":
			v.TrackGain, v.HasTrack = parseDB(text)
		case "replaygain_album_gain":
			v.AlbumGain, v.HasAlbum = parseDB(text)
		case "replaygain_track_peak":
			v.TrackPeak, _ = parseNumber(text)
		case "replaygain_album_peak":
			v.AlbumPeak, _ = parseNumber(text)
		case "r128_track_gain":
			r128Track, hasR128Track = parseR128(text)
		case "r128_album_gain":
			r128Album, hasR128Album = parseR128(text)
		}
	}
	if !v.HasTrack && hasR128Track {
		v.TrackGain, v.HasTrack = r128Track, true
	}
	if !v.HasAlbum && hasR128Album {
		v.AlbumGain, v.HasAlbum = r128Album, true
	}
	return v
}

// parseDB reads a gain such as "-6.54 dB".
func parseDB(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.EqualFold(s[len(s)-2:], "db") {
		s = s[:len(s)-2]
	}
	return parseNumber(s)
}

func parseNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseR128 reads an Opus R128 gain: a Q7.8 integer relative to -23 LUFS,
// which is 5 dB below the ReplayGain reference.
func parseR128(s string) (float64, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return float64(n)/256 + 5, true
}
