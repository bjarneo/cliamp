package qobuz

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"crypto/sha256"
	"golang.org/x/crypto/hkdf"
)

type cmafStreamReader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   *client
	template string
	key      [16]byte
	segments int
	next     int
	pending  *bytes.Reader
}

func (r *cmafStreamReader) Read(p []byte) (int, error) {
	for {
		if r.pending != nil {
			n, err := r.pending.Read(p)
			if n > 0 {
				return n, nil
			}
			r.pending = nil
			if err != nil && err != io.EOF {
				return 0, err
			}
		}
		if r.next > r.segments {
			return 0, io.EOF
		}
		data, err := r.client.fetchBytes(r.ctx, strings.Replace(r.template, "$SEGMENT$", strconv.Itoa(r.next), 1))
		if err != nil {
			return 0, err
		}
		plain, err := decryptSegment(data, r.key)
		if err != nil {
			return 0, err
		}
		r.next++
		r.pending = bytes.NewReader(plain)
	}
}

func (r *cmafStreamReader) Close() error {
	r.cancel()
	return nil
}

const (
	initUUID    = "c7c75df0fdd951e98fc22971e4acf8d2"
	segmentUUID = "3b42129256f35f75923663b69a1f52b2"
)

type fileURLResponse struct {
	URLTemplate string `json:"url_template"`
	URL         string `json:"url"`
	Key         string `json:"key"`
	KeyID       string `json:"key_id"`
	Segments    int    `json:"n_segments"`
	FormatID    int    `json:"format_id"`
}

type cmafSessionResponse struct {
	SessionID string `json:"session_id"`
	Infos     string `json:"infos"`
}

func b64url(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func cmafSignature(method string, params url.Values, ts, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sortStrings(keys)
	raw := method
	for _, k := range keys {
		raw += k + params.Get(k)
	}
	raw += ts + secret
	return md5hex(raw)
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func (c *client) cmafRequest(ctx context.Context, method, endpoint, sessionID string, params url.Values, body bool, out any) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signParams := make(url.Values, len(params))
	for k, v := range params {
		signParams[k] = v
	}
	signParams.Set("request_ts", ts)
	signatureParams := make(url.Values, len(params))
	for k, v := range params {
		signatureParams[k] = v
	}
	signParams.Set("request_sig", cmafSignature(strings.ReplaceAll(endpoint, "/", ""), signatureParams, ts, cmafSeed))
	var reqBody io.Reader
	if body {
		reqBody = strings.NewReader(signParams.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, map[bool]string{true: http.MethodPost, false: http.MethodGet}[body], apiBaseURL+endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("qobuz: %s: build request: %w", endpoint, err)
	}
	req.Header.Set("X-App-Id", c.appID)
	req.Header.Set("X-User-Auth-Token", c.uat)
	if sessionID != "" {
		req.Header.Set("X-Session-Id", sessionID)
	}
	if body {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.URL.RawQuery = signParams.Encode()
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qobuz: %s: request: %w", endpoint, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("qobuz: %s: read response: %w", endpoint, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("qobuz: %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("qobuz: %s: decode: %w", endpoint, err)
	}
	return nil
}

func (c *client) cmafSession(ctx context.Context) (string, string, error) {
	var out cmafSessionResponse
	if err := c.cmafRequest(ctx, http.MethodPost, "session/start", "", url.Values{"profile": {"qbz-1"}}, true, &out); err != nil {
		return "", "", err
	}
	if out.SessionID == "" || out.Infos == "" {
		return "", "", fmt.Errorf("qobuz: session/start returned incomplete session")
	}
	return out.SessionID, out.Infos, nil
}

func deriveSessionKey(infos string) ([16]byte, error) {
	var out [16]byte
	parts := strings.Split(infos, ".")
	if len(parts) < 2 {
		return out, fmt.Errorf("qobuz: invalid CMAF session infos")
	}
	salt, err := b64url(parts[0])
	if err != nil {
		return out, fmt.Errorf("qobuz: decode CMAF salt: %w", err)
	}
	info, err := b64url(parts[1])
	if err != nil {
		return out, fmt.Errorf("qobuz: decode CMAF info: %w", err)
	}
	ikm := make([]byte, len(cmafSeed)/2)
	for i := range ikm {
		if _, err := fmt.Sscanf(cmafSeed[i*2:i*2+2], "%02x", &ikm[i]); err != nil {
			return out, err
		}
	}
	r := hkdf.New(sha256.New, ikm, salt, info)
	if _, err := io.ReadFull(r, out[:]); err != nil {
		return out, fmt.Errorf("qobuz: derive CMAF session key: %w", err)
	}
	return out, nil
}

func unwrapContentKey(session [16]byte, token string) ([16]byte, error) {
	var out [16]byte
	parts := strings.Split(token, ".")
	if len(parts) < 3 {
		return out, fmt.Errorf("qobuz: invalid CMAF key")
	}
	wrapped, err := b64url(parts[1])
	if err != nil {
		return out, err
	}
	iv, err := b64url(parts[2])
	if err != nil || len(iv) != aes.BlockSize {
		return out, fmt.Errorf("qobuz: invalid CMAF key IV")
	}
	if len(wrapped)%aes.BlockSize != 0 {
		return out, fmt.Errorf("qobuz: invalid wrapped CMAF key length")
	}
	block, _ := aes.NewCipher(session[:])
	dec := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(wrapped))
	dec.CryptBlocks(plain, wrapped)
	if len(plain) == 0 {
		return out, fmt.Errorf("qobuz: empty CMAF key")
	}
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return out, fmt.Errorf("qobuz: invalid CMAF key padding")
	}
	plain = plain[:len(plain)-pad]
	if len(plain) != 16 {
		return out, fmt.Errorf("qobuz: unexpected CMAF key size")
	}
	copy(out[:], plain)
	return out, nil
}

func decryptCTR(key [16]byte, iv []byte, data []byte) {
	counter := make([]byte, aes.BlockSize)
	copy(counter, iv)
	block, _ := aes.NewCipher(key[:])
	cipher.NewCTR(block, counter).XORKeyStream(data, data)
}

func boxSize(data []byte, pos int) int {
	if pos+8 > len(data) {
		return 0
	}
	n := int(binary.BigEndian.Uint32(data[pos:]))
	if n == 0 {
		return len(data) - pos
	}
	if n < 8 {
		return 0
	}
	return n
}

func uuidPayload(data []byte, wanted string) ([]byte, bool) {
	for pos := 0; pos+24 <= len(data); {
		n := boxSize(data, pos)
		if n == 0 || pos+n > len(data) {
			break
		}
		if string(data[pos+4:pos+8]) == "uuid" && fmt.Sprintf("%x", data[pos+8:pos+24]) == wanted {
			return data[pos+24 : pos+n], true
		}
		pos += n
	}
	return nil, false
}

func parseInit(data []byte) ([]byte, error) {
	p, ok := uuidPayload(data, initUUID)
	if !ok || len(p) < 30 {
		return nil, fmt.Errorf("qobuz: CMAF init box missing")
	}
	rawLen := int(binary.BigEndian.Uint16(p[26:28]))
	if 28+rawLen > len(p) {
		return nil, fmt.Errorf("qobuz: truncated CMAF init")
	}
	raw := p[28 : 28+rawLen]
	at := strings.Index(string(raw), "fLaC")
	if at < 0 || at+42 > len(raw) {
		return nil, fmt.Errorf("qobuz: FLAC header missing from CMAF init")
	}
	header := append([]byte(nil), raw[at:at+42]...)
	header[4] |= 0x80
	return header, nil
}

func decryptSegment(data []byte, key [16]byte) ([]byte, error) {
	var uuid []byte
	var uuidStart int
	var mdatEnd int
	for pos := 0; pos+8 <= len(data); {
		n := boxSize(data, pos)
		if n == 0 || pos+n > len(data) {
			break
		}
		typ := string(data[pos+4 : pos+8])
		if typ == "uuid" && pos+24 <= len(data) && fmt.Sprintf("%x", data[pos+8:pos+24]) == segmentUUID {
			uuid = data[pos:]
			uuidStart = pos
		}
		if typ == "mdat" {
			mdatEnd = pos + n
		}
		pos += n
	}
	if uuid == nil {
		return nil, fmt.Errorf("qobuz: CMAF segment crypto box missing")
	}
	p := uuid[24:]
	if len(p) < 12 {
		return nil, fmt.Errorf("qobuz: truncated CMAF crypto box")
	}
	dataOffset := uuidStart + int(binary.BigEndian.Uint32(p[4:8]))
	ivSize := int(p[8])
	count := int(p[9])<<16 | int(p[10])<<8 | int(p[11])
	at := 12
	audioStart := dataOffset
	if audioStart < 0 || audioStart > len(data) {
		return nil, fmt.Errorf("qobuz: invalid CMAF audio offset")
	}
	out := make([]byte, 0, mdatEnd-audioStart)
	for i := 0; i < count; i++ {
		if at+8+ivSize > len(p) {
			return nil, fmt.Errorf("qobuz: truncated CMAF frame table")
		}
		size := int(binary.BigEndian.Uint32(p[at:]))
		at += 4
		at += 2
		flags := binary.BigEndian.Uint16(p[at:])
		at += 2
		iv := p[at : at+ivSize]
		at += ivSize
		if audioStart+size > len(data) {
			return nil, fmt.Errorf("qobuz: truncated CMAF frame")
		}
		frame := append([]byte(nil), data[audioStart:audioStart+size]...)
		if flags != 0 {
			decryptCTR(key, iv, frame)
		}
		out = append(out, frame...)
		audioStart += size
	}
	if audioStart < mdatEnd {
		out = append(out, data[audioStart:mdatEnd]...)
	}
	return out, nil
}

func (c *client) cmafFile(ctx context.Context, trackID string, formatID int) ([]byte, error) {
	stream, err := c.cmafStream(ctx, trackID, formatID)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return io.ReadAll(stream)
}

func (c *client) cmafStream(ctx context.Context, trackID string, formatID int) (io.ReadCloser, error) {
	sid, infos, err := c.cmafSession(ctx)
	if err != nil {
		return nil, err
	}
	sessionKey, err := deriveSessionKey(infos)
	if err != nil {
		return nil, err
	}
	var file fileURLResponse
	params := url.Values{"track_id": {trackID}, "format_id": {strconv.Itoa(formatID)}, "intent": {"stream"}}
	if err := c.cmafRequest(ctx, http.MethodGet, "file/url", sid, params, false, &file); err != nil {
		return nil, err
	}
	if file.URLTemplate == "" || file.Key == "" {
		if file.URL != "" {
			return nil, fmt.Errorf("qobuz: preview streams are not supported")
		}
		return nil, fmt.Errorf("qobuz: file/url returned no stream")
	}
	key, err := unwrapContentKey(sessionKey, file.Key)
	if err != nil {
		return nil, err
	}
	initURL := strings.Replace(file.URLTemplate, "$SEGMENT$", "0", 1)
	initData, err := c.fetchBytes(ctx, initURL)
	if err != nil {
		return nil, err
	}
	header, err := parseInit(initData)
	if err != nil {
		return nil, err
	}
	if file.Segments < 1 {
		return nil, fmt.Errorf("qobuz: file/url returned no segments")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &cmafStreamReader{
		ctx:      streamCtx,
		cancel:   cancel,
		client:   c,
		template: file.URLTemplate,
		key:      key,
		segments: file.Segments,
		next:     1,
		pending:  bytes.NewReader(header),
	}, nil
}

func (c *client) fetchBytes(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qobuz: CMAF segment HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 128<<20))
}
