package tidal

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeQuality(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"low", qualityLow, true},
		{"high", qualityHigh, true},
		{"lossless", qualityLossless, true},
		{"", qualityLossless, true},
		{"LOSSLESS", qualityLossless, true},
		{"  lossless  ", qualityLossless, true},
		{"hires", qualityHiRes, true},
		{"hi_res", qualityHiRes, true},
		{"hi-res", qualityHiRes, true},
		{"hi_res_lossless", qualityHiRes, true},
		{"max", qualityHiRes, true},
		{"320", "", false},
		{"flac", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := normalizeQuality(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("normalizeQuality(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRequestQuality(t *testing.T) {
	// The device client never receives LOSSLESS via BTS (downgrades to HIGH
	// AAC — verified live), so both FLAC settings must request HI_RES_LOSSLESS.
	tests := []struct{ in, want string }{
		{qualityLow, qualityLow},
		{qualityHigh, qualityHigh},
		{qualityLossless, qualityHiRes},
		{qualityHiRes, qualityHiRes},
	}
	for _, tt := range tests {
		if got := requestQuality(tt.in); got != tt.want {
			t.Errorf("requestQuality(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func btsBase64(jsonBody string) string {
	return base64.StdEncoding.EncodeToString([]byte(jsonBody))
}

// btsPlaybackInfo mirrors the sanitized live captures: BTS manifests carry
// AAC with encryptionType NONE.
func btsPlaybackInfo(quality, codecs, u string) apiPlaybackInfo {
	return apiPlaybackInfo{
		AudioQuality:     quality,
		ManifestMimeType: "application/vnd.tidal.bts",
		Manifest: btsBase64(fmt.Sprintf(
			`{"mimeType":"audio/mp4","codecs":%q,"encryptionType":"NONE","urls":[%q]}`, codecs, u)),
	}
}

func TestStreamSourceFromManifestBTS(t *testing.T) {
	t.Run("delivered quality and url", func(t *testing.T) {
		// Live capture shape: requested LOSSLESS, delivered HIGH AAC.
		src, err := streamSourceFromManifest(btsPlaybackInfo(qualityHigh, "mp4a.40.2", "https://cdn.tidal.com/x.mp4"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if src.url != "https://cdn.tidal.com/x.mp4" || len(src.segments) != 0 {
			t.Errorf("src = %+v", src)
		}
		if src.quality != qualityHigh {
			t.Errorf("delivered quality = %q, want %q", src.quality, qualityHigh)
		}
	})

	t.Run("empty encryption type accepted", func(t *testing.T) {
		pi := apiPlaybackInfo{
			ManifestMimeType: "application/vnd.tidal.bts",
			Manifest:         btsBase64(`{"urls":["https://cdn.tidal.com/y.m4a"]}`),
		}
		if _, err := streamSourceFromManifest(pi); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	errCases := []struct {
		name string
		pi   apiPlaybackInfo
	}{
		{"encrypted stream", apiPlaybackInfo{
			ManifestMimeType: "application/vnd.tidal.bts",
			Manifest:         btsBase64(`{"encryptionType":"OLD_AES","urls":["https://cdn.tidal.com/z"]}`),
		}},
		{"unknown mime type", apiPlaybackInfo{ManifestMimeType: "application/vnd.tidal.emu"}},
		{"invalid base64", apiPlaybackInfo{
			ManifestMimeType: "application/vnd.tidal.bts",
			Manifest:         "!!!not-base64!!!",
		}},
		{"no urls", apiPlaybackInfo{
			ManifestMimeType: "application/vnd.tidal.bts",
			Manifest:         btsBase64(`{"encryptionType":"NONE","urls":[]}`),
		}},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			if src, err := streamSourceFromManifest(tt.pi); err == nil {
				t.Errorf("expected error, got %+v", src)
			}
		})
	}
}

func TestStreamSourceFromManifestDASH(t *testing.T) {
	// Structure of a live hi-res capture: single FLAC representation,
	// absolute URLs, SegmentTimeline with a repeat.
	src, err := streamSourceFromManifest(apiPlaybackInfo{
		AudioQuality:     qualityHiRes,
		ManifestMimeType: "application/dash+xml",
		Manifest:         base64.StdEncoding.EncodeToString([]byte(sampleMPD)),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.quality != qualityHiRes {
		t.Errorf("delivered quality = %q", src.quality)
	}
	want := []string{
		"https://cdn.tidal.com/init.mp4?token=t&x=1",
		"https://cdn.tidal.com/seg1.m4s",
		"https://cdn.tidal.com/seg2.m4s",
		"https://cdn.tidal.com/seg3.m4s",
	}
	if len(src.segments) != len(want) {
		t.Fatalf("segments = %v, want %v", src.segments, want)
	}
	for i := range want {
		if src.segments[i] != want[i] {
			t.Errorf("segment[%d] = %q, want %q", i, src.segments[i], want[i])
		}
	}
	if strings.Contains(src.segments[0], "&amp;") {
		t.Error("XML entities were not decoded in segment URLs")
	}
}
