package player

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
)

// SupportedExts is the set of file extensions the player can decode.
var SupportedExts = map[string]bool{
	".mp3":  true,
	".wav":  true,
	".flac": true,
	".ogg":  true,
	".m4a":  true,
	".aac":  true,
	".m4b":  true,
	".alac": true,
	".wma":  true,
	".opus": true,
}

// httpClient is used for all HTTP streaming. It sets a generous header
// timeout but no overall timeout, so infinite live streams aren't killed.
// HTTP/2 is explicitly disabled via TLSNextProto because Icecast/SHOUTcast
// servers don't support it — Go's default ALPN negotiation causes EOF.
var httpClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	},
}

// isURL reports whether path is an HTTP or HTTPS URL.
func isURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// sourceResult holds the opened stream and optional HTTP metadata.
type sourceResult struct {
	body          io.ReadCloser
	contentType   string // e.g. "audio/aacp"; empty for local files
	contentLength int64  // -1 if unknown; from Content-Length header for HTTP
}

// openSource returns a ReadCloser for the given path, handling both
// local files and HTTP URLs.
//
// For HTTP URLs, it sends the Icy-MetaData:1 header to request ICY metadata.
// If the server responds with icy-metaint, the body is wrapped in an icyReader
// that strips metadata and fires onMeta with each StreamTitle update.
func openSource(path string, onMeta func(string)) (sourceResult, error) {
	return openSourceAt(path, 0, onMeta)
}

// openSourceAt is like openSource but starts the HTTP stream at the given byte
// offset using an HTTP Range request (Range: bytes=offset-). For local files
// the offset is ignored (use decoder.Seek for local files).
func openSourceAt(path string, byteOffset int64, onMeta func(string)) (sourceResult, error) {
	if !isURL(path) {
		f, err := os.Open(path)
		return sourceResult{body: f, contentLength: -1}, err
	}
	req, err := http.NewRequest("GET", path, nil)
	if err != nil {
		return sourceResult{}, fmt.Errorf("http request: %w", err)
	}
	req.Header.Set("User-Agent", "cliamp/1.0 (https://github.com/bjarneo/cliamp)")
	// Request ICY metadata — servers that don't support it simply ignore this header.
	req.Header.Set("Icy-MetaData", "1")
	if byteOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", byteOffset))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return sourceResult{}, fmt.Errorf("http get: %w", err)
	}
	// Accept 200 OK (full response) or 206 Partial Content (range response).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return sourceResult{}, fmt.Errorf("http status %s", resp.Status)
	}

	body := resp.Body

	// Wrap in ICY reader if the server provides a metaint interval.
	if metaStr := resp.Header.Get("Icy-Metaint"); metaStr != "" && onMeta != nil {
		if metaInt, err := strconv.Atoi(metaStr); err == nil && metaInt > 0 {
			body = newIcyReader(body, metaInt, onMeta)
		}
	}

	return sourceResult{
		body:          body,
		contentType:   resp.Header.Get("Content-Type"),
		contentLength: resp.ContentLength,
	}, nil
}

// extFromContentType maps an HTTP Content-Type to a file extension.
// Returns "" if the type is unrecognized.
func extFromContentType(ct string) string {
	// Strip parameters (e.g. "audio/aacp; charset=utf-8" → "audio/aacp").
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch ct {
	case "audio/aac", "audio/aacp", "audio/x-aac":
		return ".aac"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/opus":
		return ".opus"
	}
	return ""
}

// formatExt returns the audio format extension for a path.
// For URLs, it parses the path component (ignoring query params),
// checks a "format" query param as fallback, and defaults to ".mp3".
func formatExt(path string) string {
	if !isURL(path) {
		return strings.ToLower(filepath.Ext(path))
	}
	u, err := url.Parse(path)
	if err != nil {
		return ".mp3"
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	if ext == "" || ext == ".view" {
		if f := u.Query().Get("format"); f != "" {
			return "." + strings.ToLower(f)
		}
		return ".mp3"
	}
	return ext
}

// needsFFmpeg reports whether the given extension requires ffmpeg to decode.
func needsFFmpeg(ext string) bool {
	switch ext {
	case ".m4a", ".aac", ".m4b", ".alac", ".wma", ".opus":
		return true
	}
	return false
}

// decode selects the appropriate decoder based on the file extension.
func decode(rc io.ReadCloser, path string, sr beep.SampleRate) (beep.StreamSeekCloser, beep.Format, error) {
	return decodeWithExt(rc, formatExt(path), path, sr)
}

// decodeWithExt selects the decoder using an explicit extension.
func decodeWithExt(rc io.ReadCloser, ext, path string, sr beep.SampleRate) (beep.StreamSeekCloser, beep.Format, error) {
	if needsFFmpeg(ext) {
		return decodeFFmpeg(path, sr)
	}
	switch ext {
	case ".wav":
		return wav.Decode(rc)
	case ".flac":
		return flac.Decode(rc)
	case ".ogg":
		return vorbis.Decode(rc)
	default:
		return mp3.Decode(rc)
	}
}
