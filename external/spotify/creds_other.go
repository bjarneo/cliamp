//go:build !windows

package spotify

// protectCreds is the non-Windows identity shim: DPAPI only exists on
// Windows. The envelope still wraps the file; contents stay plaintext JSON
// guarded by the existing 0o600 file permissions.
func protectCreds(plaintext []byte) ([]byte, error) { return plaintext, nil }

// unprotectCreds is the matching identity shim for non-Windows platforms.
func unprotectCreds(blob []byte) ([]byte, error) { return blob, nil }
