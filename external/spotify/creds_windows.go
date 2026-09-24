//go:build windows

package spotify

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// protectCreds encrypts plaintext with Windows DPAPI (CryptProtectData).
// CRYPTPROTECT_UI_FORBIDDEN keeps the call silent — no UI may appear inside a
// TUI. User scope (CRYPTPROTECT_LOCAL_MACHINE intentionally absent) means only
// the same Windows user account can decrypt the blob.
//
// Threat model: protects stored Spotify credentials against other users on
// the machine and against offline disk theft. It does NOT protect against
// malware running as the same user, which can call CryptUnprotectData itself.
func protectCreds(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(plaintext)), Data: &plaintext[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}

// unprotectCreds reverses protectCreds. Blobs protected by a different user
// account (or machine) fail at CryptUnprotectData.
func unprotectCreds(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
