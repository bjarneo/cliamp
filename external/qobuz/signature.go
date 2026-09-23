package qobuz

import (
	"crypto/md5"
	"fmt"
)

// md5hex returns the lowercase hex MD5 digest of s. The Qobuz API mandates an
// MD5-based request signature (md5(app_secret + sorted params)). This is a
// protocol requirement, not a cryptographic choice; the hash protects no local
// secret material. md5hex is used only to build Qobuz request_sig values
// (trackFileURLSig, favoriteParams).
func md5hex(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
