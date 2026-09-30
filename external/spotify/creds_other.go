//go:build !windows

package spotify

// credsProtected is false off Windows: the file keeps the plain storedCreds
// JSON format, guarded by its 0o600 permissions.
const credsProtected = false

// protectCreds is the non-Windows identity shim: DPAPI only exists on
// Windows.
func protectCreds(plaintext []byte) ([]byte, error) { return plaintext, nil }

// unprotectCreds is the matching identity shim for non-Windows platforms.
func unprotectCreds(blob []byte) ([]byte, error) { return blob, nil }
