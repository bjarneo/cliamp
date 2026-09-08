//go:build linux

package openmpt

// libraryCandidates are tried in order; distros package the SONAME
// (libopenmpt.so.0) but not always the unversioned dev symlink.
var libraryCandidates = []string{
	"libopenmpt.so.0",
	"libopenmpt.so",
}
