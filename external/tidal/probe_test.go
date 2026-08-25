package tidal

import (
	"strings"
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	got := sanitizeURL("https://sp-pr-cf.audio.tidal.com/mediatracks/abc123/0.flac?token=SECRET&Expires=123")
	if strings.Contains(got, "SECRET") || strings.Contains(got, "abc123") {
		t.Fatalf("sanitizeURL leaked signed parts: %q", got)
	}
	if got != "host=sp-pr-cf.audio.tidal.com ext=.flac" {
		t.Errorf("sanitizeURL = %q", got)
	}
}

func TestSanitizeError(t *testing.T) {
	err := &testErr{"tidal: GET https://cdn/x.flac?token=SECRET failed"}
	if got := sanitizeError(err); strings.Contains(got, "SECRET") {
		t.Errorf("sanitizeError leaked query: %q", got)
	}
}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func TestMPDAttr(t *testing.T) {
	mpd := []byte(`<Representation codecs="flac" audioSamplingRate="96000"><SegmentTimeline><S d="1"/><S d="2"/></SegmentTimeline>`)
	if got := mpdAttr(mpd, "codecs"); got != "flac" {
		t.Errorf("codecs = %q", got)
	}
	if got := mpdAttr(mpd, "audioSamplingRate"); got != "96000" {
		t.Errorf("audioSamplingRate = %q", got)
	}
	if got := mpdAttr(mpd, "missing"); got != "?" {
		t.Errorf("missing attr = %q", got)
	}
}
