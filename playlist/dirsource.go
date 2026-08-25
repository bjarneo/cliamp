package playlist

// DirSource is a [[dir]] section in a playlist file: a directory that is
// scanned for audio files every time the playlist loads, instead of listing
// every file explicitly.
type DirSource struct {
	Path      string // directory path; supports ~ and environment variables
	Recursive bool   // scan subdirectories too (default true)
}
