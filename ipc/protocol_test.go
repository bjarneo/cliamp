package ipc

import (
	"encoding/json"
	"testing"
)

func TestRequestMarshal(t *testing.T) {
	req := Request{Cmd: "play"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Cmd != "play" {
		t.Errorf("Cmd = %q, want play", decoded.Cmd)
	}
}

func TestRequestWithValue(t *testing.T) {
	req := Request{Cmd: "volume", Value: -5.0}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Cmd != "volume" || decoded.Value != -5.0 {
		t.Errorf("got Cmd=%q Value=%f, want volume -5.0", decoded.Cmd, decoded.Value)
	}
}

func TestRequestOmitsEmptyFields(t *testing.T) {
	req := Request{Cmd: "next"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if _, ok := raw["value"]; ok {
		t.Error("zero value should be omitted")
	}
	if _, ok := raw["playlist"]; ok {
		t.Error("empty playlist should be omitted")
	}
	if _, ok := raw["path"]; ok {
		t.Error("empty path should be omitted")
	}
}

func TestResponseMarshal(t *testing.T) {
	track := &TrackInfo{Title: "Song", Artist: "Artist", Path: "/music/song.mp3"}
	resp := Response{
		OK:       true,
		State:    "playing",
		Track:    track,
		Position: 30.5,
		Duration: 180.0,
		Volume:   -10.0,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if !decoded.OK {
		t.Error("OK should be true")
	}
	if decoded.State != "playing" {
		t.Errorf("State = %q, want playing", decoded.State)
	}
	if decoded.Track == nil {
		t.Fatal("Track should not be nil")
	}
	if decoded.Track.Title != "Song" {
		t.Errorf("Track.Title = %q, want Song", decoded.Track.Title)
	}
	if decoded.Position != 30.5 {
		t.Errorf("Position = %f, want 30.5", decoded.Position)
	}
}

func TestResponseError(t *testing.T) {
	resp := Response{OK: false, Error: "track not found"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.OK {
		t.Error("OK should be false")
	}
	if decoded.Error != "track not found" {
		t.Errorf("Error = %q, want 'track not found'", decoded.Error)
	}
}

func TestDispatcherFunc(t *testing.T) {
	var received any
	fn := DispatcherFunc(func(msg any) {
		received = msg
	})

	fn.Send("test message")

	if received != "test message" {
		t.Errorf("received = %v, want 'test message'", received)
	}
}

func TestTrackInfoMarshal(t *testing.T) {
	info := TrackInfo{Title: "Song", Artist: "Artist", Path: "/path/song.mp3"}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded TrackInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Title != "Song" || decoded.Artist != "Artist" || decoded.Path != "/path/song.mp3" {
		t.Errorf("decoded = %+v, want Title=Song Artist=Artist Path=/path/song.mp3", decoded)
	}
}

func TestResponseBoolPointerFields(t *testing.T) {
	// Shuffle and Mono are *bool so they can distinguish unset from false
	trueBool := true
	resp := Response{
		OK:      true,
		Shuffle: &trueBool,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.Shuffle == nil || !*decoded.Shuffle {
		t.Error("Shuffle should be *true")
	}
	if decoded.Mono != nil {
		t.Error("Mono should be nil when unset")
	}
}
