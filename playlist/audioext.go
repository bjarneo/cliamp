package playlist

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// audioExtensions is the set of file extensions that cliamp plays. The player
// decodes .mp3, .wav, .flac and .ogg natively and hands the rest to ffmpeg.
var audioExtensions = map[string]bool{
	".mp3":  true,
	".wav":  true,
	".flac": true,
	".ogg":  true,
	".m4a":  true,
	".aac":  true,
	".aacp": true,
	".m4b":  true,
	".alac": true,
	".wma":  true,
	".opus": true,
	".webm": true,
}

// IsAudioFile reports whether path ends in an audio extension that cliamp
// plays. The check ignores case and reads the path only.
func IsAudioFile(path string) bool {
	return audioExtensions[strings.ToLower(filepath.Ext(path))]
}

// AudioExtensions returns the audio extensions that cliamp plays, sorted and
// with the leading dot. The result is a copy.
func AudioExtensions() []string {
	return slices.Sorted(maps.Keys(audioExtensions))
}
