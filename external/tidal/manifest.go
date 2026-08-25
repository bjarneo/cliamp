package tidal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Audio quality values accepted by the playbackinfo endpoint. Any paid Tidal
// subscription includes all four tiers (the HiFi/HiFi Plus split ended in
// 2024).
const (
	qualityLow      = "LOW"             // 96 kbps AAC
	qualityHigh     = "HIGH"            // 320 kbps AAC
	qualityLossless = "LOSSLESS"        // FLAC 16-bit/44.1kHz
	qualityHiRes    = "HI_RES_LOSSLESS" // FLAC up to 24-bit/192kHz
)

// normalizeQuality maps a [tidal] config quality string to a Tidal
// audioquality value. ok is false when s is not a recognized quality name.
func normalizeQuality(s string) (quality string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return qualityLow, true
	case "high":
		return qualityHigh, true
	case "", "lossless":
		return qualityLossless, true
	case "hires", "hi_res", "hi-res", "hi_res_lossless", "max":
		return qualityHiRes, true
	default:
		return "", false
	}
}

// isFLACQuality reports whether q names a lossless (FLAC) delivery tier.
func isFLACQuality(q string) bool {
	return q == qualityLossless || q == qualityHiRes
}

// requestQuality maps the user's configured quality to the value actually
// sent to playbackinfo. The device client cliamp uses never receives the
// LOSSLESS tier (BTS caps at HIGH AAC — verified against the live API), so
// both FLAC settings request HI_RES_LOSSLESS: that returns DASH FLAC when
// the track has it and downgrades to HIGH AAC otherwise, which
// streamSource's delivered-quality reporting surfaces.
func requestQuality(configured string) string {
	if isFLACQuality(configured) {
		return qualityHiRes
	}
	return configured
}

// btsManifest is Tidal's "basic track stream" manifest: a JSON document with
// direct CDN URLs, delivered base64-encoded in the playbackinfo response for
// the AAC tiers.
type btsManifest struct {
	MimeType       string   `json:"mimeType"`
	Codecs         string   `json:"codecs"`
	EncryptionType string   `json:"encryptionType"`
	URLs           []string `json:"urls"`
}

// streamSource is a playable source extracted from a playbackinfo response:
// either a direct URL (BTS) or an ordered DASH segment list, plus the quality
// the server actually delivered.
type streamSource struct {
	url      string
	segments []string
	quality  string // delivered audioQuality (may be lower than requested)
}

// streamSourceFromManifest decodes the base64 manifest in a playbackinfo
// response into a playable source.
func streamSourceFromManifest(pi apiPlaybackInfo) (streamSource, error) {
	raw, err := base64.StdEncoding.DecodeString(pi.Manifest)
	if err != nil {
		return streamSource{}, fmt.Errorf("tidal: decode manifest: %w", err)
	}

	switch {
	case strings.Contains(pi.ManifestMimeType, "vnd.tidal.bts"):
		var m btsManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return streamSource{}, fmt.Errorf("tidal: parse manifest: %w", err)
		}
		if m.EncryptionType != "" && m.EncryptionType != "NONE" {
			return streamSource{}, fmt.Errorf("tidal: stream is encrypted (%s), not supported", m.EncryptionType)
		}
		if len(m.URLs) == 0 || m.URLs[0] == "" {
			return streamSource{}, errors.New("tidal: manifest contains no stream URL")
		}
		return streamSource{url: m.URLs[0], quality: pi.AudioQuality}, nil

	case strings.Contains(pi.ManifestMimeType, "dash+xml"):
		segments, err := dashSegments(raw)
		if err != nil {
			return streamSource{}, err
		}
		return streamSource{segments: segments, quality: pi.AudioQuality}, nil

	default:
		return streamSource{}, fmt.Errorf("tidal: unsupported manifest type %q", pi.ManifestMimeType)
	}
}
