//go:build darwin

package openmpt

// libraryCandidates covers the plain dylib name plus the Homebrew prefixes
// for Apple Silicon and Intel, since Homebrew's lib dir isn't always on the
// dynamic linker's default search path.
var libraryCandidates = []string{
	"libopenmpt.dylib",
	"/opt/homebrew/lib/libopenmpt.dylib",
	"/usr/local/lib/libopenmpt.dylib",
	"/usr/local/opt/libopenmpt/lib/libopenmpt.dylib",
}
