package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// TestSubsonicAuthRoundTrip pins the Subsonic token construction:
// token = md5hex(password + salt), with a fresh random salt per call.
func TestSubsonicAuthRoundTrip(t *testing.T) {
	salt, token := subsonicAuth("sesame")
	if salt == "" {
		t.Fatal("salt is empty")
	}
	sum := md5.Sum([]byte("sesame" + salt))
	if want := hex.EncodeToString(sum[:]); token != want {
		t.Fatalf("token = %q, want md5hex(password+salt) = %q", token, want)
	}

	salt2, _ := subsonicAuth("sesame")
	if salt2 == salt {
		t.Error("salt repeated across calls; want a fresh crypto/rand salt")
	}
}
