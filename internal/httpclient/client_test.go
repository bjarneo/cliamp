package httpclient

import (
	"net/http"
	"testing"
	"time"
)

func TestStreamingClientExists(t *testing.T) {
	if Streaming == nil {
		t.Fatal("Streaming client is nil")
	}
}

func TestStreamingHeaderTimeout(t *testing.T) {
	tr, ok := Streaming.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not *http.Transport")
	}
	want := 30 * time.Second
	if tr.ResponseHeaderTimeout != want {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, want)
	}
}

func TestStreamingHTTP2Disabled(t *testing.T) {
	tr, ok := Streaming.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not *http.Transport")
	}
	if tr.TLSNextProto == nil {
		t.Fatal("TLSNextProto is nil, should be empty map to disable HTTP/2")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Fatalf("TLSNextProto has %d entries, want 0", len(tr.TLSNextProto))
	}
}

func TestStreamingNoOverallTimeout(t *testing.T) {
	if Streaming.Timeout != 0 {
		t.Fatalf("Timeout = %v, want 0 (infinite streams)", Streaming.Timeout)
	}
}
