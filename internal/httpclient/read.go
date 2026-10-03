package httpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge reports a response body that is longer than the read limit.
var ErrTooLarge = errors.New("response too large")

// ReadBody reads all of r up to limit bytes. It reads one byte past the limit
// so that an oversized body returns ErrTooLarge. io.LimitReader alone cuts the
// body without an error, and a JSON decoder then fails with the unclear
// "unexpected end of JSON input".
func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrTooLarge, limit)
	}
	return body, nil
}

// ReadJSON reads a JSON body of up to limit bytes from r into v. An oversized
// body returns ErrTooLarge.
func ReadJSON(r io.Reader, limit int64, v any) error {
	body, err := ReadBody(r, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
