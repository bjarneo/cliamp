package navidrome

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// subsonicAuth builds the salt and token pair for the Subsonic authentication
// scheme. The token is mandated by the Subsonic/Navidrome API protocol
// (token = md5hex(password + salt)), not a cryptographic choice; the salt is
// drawn from crypto/rand and the token protects no local secret material.
func subsonicAuth(password string) (salt, token string) {
	// Use crypto/rand for the salt as recommended by the Subsonic API spec.
	// MD5 is required by the protocol — not a choice.
	saltBytes := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		// Fallback to timestamp if crypto/rand fails (should never happen).
		saltBytes = fmt.Appendf(nil, "%d", time.Now().UnixNano())
	}
	salt = hex.EncodeToString(saltBytes)
	hash := md5.Sum([]byte(password + salt))
	token = hex.EncodeToString(hash[:])
	return salt, token
}
