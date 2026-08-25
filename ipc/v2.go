package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
)

const protocolVersion2 = 2

// V2 error codes are stable protocol values. Clients should branch on Code,
// rather than parsing Message.
const (
	V2ErrorCodeInvalidVersion   = "invalid_version"
	V2ErrorCodeInvalidRequest   = "invalid_request"
	V2ErrorCodeInvalidParams    = "invalid_params"
	V2ErrorCodeUnknownOperation = "unknown_operation"
	V2ErrorCodeNotFound         = "not_found"
	V2ErrorCodeConflict         = "conflict"
	V2ErrorCodeUnavailable      = "unavailable"
	V2ErrorCodeCanceled         = "canceled"
	V2ErrorCodeInternal         = "internal_error"
)

// V2 error messages are stable protocol values paired with the error codes.
const (
	V2MessageInvalidVersion   = "unsupported protocol version"
	V2MessageInvalidRequest   = "invalid request"
	V2MessageInvalidParams    = "invalid parameters"
	V2MessageUnknownOperation = "unknown operation"
	V2MessageNotFound         = "resource not found"
	V2MessageConflict         = "operation cannot be performed in the current state"
	V2MessageUnavailable      = "operation dispatcher is unavailable"
	V2MessageCanceled         = "job canceled"
	V2MessageInternal         = "operation failed"
)

// V2Request is a version 2 IPC envelope. ID is retained as raw JSON so a
// string, number, or null request ID is echoed without changing its type.
type V2Request struct {
	Version   int             `json:"version"`
	ID        json.RawMessage `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	JobID     string          `json:"job_id,omitempty"`
	Topics    []string        `json:"topics,omitempty"`
}

// MarshalJSON always writes the required v2 envelope version.
func (r V2Request) MarshalJSON() ([]byte, error) {
	type request V2Request
	r.Version = protocolVersion2
	return json.Marshal(request(r))
}

// V2Response is a version 2 IPC response envelope.
type V2Response struct {
	Version  int              `json:"version"`
	ID       json.RawMessage  `json:"id,omitempty"`
	OK       bool             `json:"ok"`
	Result   json.RawMessage  `json:"result,omitempty"`
	Snapshot *RuntimeSnapshot `json:"snapshot,omitempty"`
	Job      *Job             `json:"job,omitempty"`
	Error    *V2Error         `json:"error,omitempty"`
}

// MarshalJSON always writes the v2 envelope version.
func (r V2Response) MarshalJSON() ([]byte, error) {
	type response V2Response
	r.Version = protocolVersion2
	return json.Marshal(response(r))
}

// V2Error is a machine-readable protocol error. Code and Message are stable
// values defined by this package.
type V2Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// Error implements error.
func (e *V2Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func v2Error(code, message string) *V2Error {
	return &V2Error{Code: code, Message: message}
}

func invalidV2Request() *V2Error {
	return v2Error(V2ErrorCodeInvalidRequest, V2MessageInvalidRequest)
}

func invalidV2Params() *V2Error {
	return v2Error(V2ErrorCodeInvalidParams, V2MessageInvalidParams)
}

// RuntimeSnapshot is the common runtime state shape used by V2 responses and
// terminal job results. Additional runtime data belongs in V2Response.Result.
type RuntimeSnapshot struct {
	Revision         uint64     `json:"revision"`
	PlaylistRevision uint64     `json:"playlist_revision"`
	State            string     `json:"state,omitempty"`
	Track            *TrackInfo `json:"track,omitempty"`
	LogicalTrack     *TrackInfo `json:"logical_track,omitempty"`
	PlaybackDetached bool       `json:"playback_detached,omitempty"`
	Position         float64    `json:"position,omitempty"`
	Duration         float64    `json:"duration,omitempty"`
	Seekable         bool       `json:"seekable"`
	Volume           float64    `json:"volume,omitempty"`
	Playlist         string     `json:"playlist,omitempty"`
	Index            int        `json:"index,omitempty"`
	Total            int        `json:"total,omitempty"`
	PlayNextTotal    int        `json:"play_next_total,omitempty"`
	Shuffle          *bool      `json:"shuffle,omitempty"`
	Repeat           string     `json:"repeat,omitempty"`
	Mono             *bool      `json:"mono,omitempty"`
	Speed            float64    `json:"speed,omitempty"`
	EQPreset         string     `json:"eq_preset,omitempty"`
	EQBands          []float64  `json:"eq_bands,omitempty"`
	Device           string     `json:"device,omitempty"`
	Visualizer       string     `json:"visualizer,omitempty"`
	Theme            *ThemeInfo `json:"theme,omitempty"`
	StreamError      string     `json:"stream_error,omitempty"`
}

// V2Result is the dispatcher result payload. Exactly one of Result or
// Snapshot is normally set, although a dispatcher may include both.
type V2Result struct {
	Result   json.RawMessage
	Snapshot *RuntimeSnapshot
	Job      *Job
}

// V2Dispatcher is implemented by the runtime owner (the model or a future
// daemon). The context is cancelled when the server shuts down.
type V2Dispatcher interface {
	DispatchV2(ctx context.Context, request V2Request) (V2Result, *V2Error)
}

// V2DispatcherFunc adapts a function to V2Dispatcher and is useful for tests.
type V2DispatcherFunc func(context.Context, V2Request) (V2Result, *V2Error)

// DispatchV2 implements V2Dispatcher.
func (f V2DispatcherFunc) DispatchV2(ctx context.Context, request V2Request) (V2Result, *V2Error) {
	return f(ctx, request)
}

var _ V2Dispatcher = V2DispatcherFunc(nil)

func cloneRawMessage(data json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), data...)
}

func cloneV2Error(err *V2Error) *V2Error {
	if err == nil {
		return nil
	}
	return &V2Error{Code: err.Code, Message: err.Message, Detail: err.Detail}
}

func cloneSnapshot(snapshot *RuntimeSnapshot) *RuntimeSnapshot {
	if snapshot == nil {
		return nil
	}
	snapshotCopy := *snapshot
	if snapshot.Track != nil {
		track := *snapshot.Track
		track.ProviderMeta = maps.Clone(track.ProviderMeta)
		snapshotCopy.Track = &track
	}
	if snapshot.LogicalTrack != nil {
		track := *snapshot.LogicalTrack
		track.ProviderMeta = maps.Clone(track.ProviderMeta)
		snapshotCopy.LogicalTrack = &track
	}
	if snapshot.Shuffle != nil {
		shuffle := *snapshot.Shuffle
		snapshotCopy.Shuffle = &shuffle
	}
	if snapshot.Mono != nil {
		mono := *snapshot.Mono
		snapshotCopy.Mono = &mono
	}
	snapshotCopy.EQBands = append([]float64(nil), snapshot.EQBands...)
	if snapshot.Theme != nil {
		theme := *snapshot.Theme
		snapshotCopy.Theme = &theme
	}
	return &snapshotCopy
}

func v2ErrorFromError(err error) *V2Error {
	if err == nil {
		return nil
	}
	var protocolErr *V2Error
	if errors.As(err, &protocolErr) {
		return cloneV2Error(protocolErr)
	}
	return v2Error(V2ErrorCodeInternal, V2MessageInternal)
}

func validV2Result(result json.RawMessage) error {
	if len(result) == 0 || json.Valid(result) {
		return nil
	}
	return errors.New("invalid JSON result")
}
