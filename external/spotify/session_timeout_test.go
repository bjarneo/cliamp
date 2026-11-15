package spotify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	"golang.org/x/oauth2"
)

// TestWebRequestsTimeOut checks that a stalled connection ends in a timeout
// error for each request that webHTTPClient sends, even when the caller
// context has no deadline.
func TestWebRequestsTimeOut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	originalClient := webHTTPClient
	webHTTPClient = &http.Client{
		Timeout: 50 * time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}
	t.Cleanup(func() { webHTTPClient = originalClient })

	staticSession := func() *Session {
		return &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	}
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "web api",
			call: func() error {
				_, err := staticSession().webAPIOnce(context.Background(), http.MethodGet, "/v1/me", nil, nil, "")
				return err
			},
		},
		{
			name: "lyrics",
			call: func() error {
				_, err := staticSession().trackLyrics(context.Background(), "track")
				return err
			},
		},
		{
			name: "token refresh",
			call: func() error {
				expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}
				_, err := webAPITokenSource("client", expired, storedCreds{}).Token()
				return err
			},
		},
		{
			name: "silent token refresh",
			call: func() error {
				_, err := silentTokenRefresh(context.Background(), "client", "refresh")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- tt.call() }()
			select {
			case err := <-done:
				var urlErr *url.Error
				if !errors.As(err, &urlErr) || !urlErr.Timeout() {
					t.Fatalf("error = %v, want a client timeout", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not time out")
			}
		})
	}
}

// chunkTrack is an audio file of two librespot chunks.
var chunkTrack = bytes.Repeat([]byte{0x5a}, 2*audio.DefaultChunkSize)

// TestChunkReadEndsWhenDownloadStalls reads the second chunk of a track
// through librespot's chunked reader while its download hangs. A stalled
// body fails within the stall timeout. A closed stream fails at once,
// without the retry backoff of 1 second in librespot.
func TestChunkReadEndsWhenDownloadStalls(t *testing.T) {
	tests := []struct {
		name  string
		stall time.Duration
		// partial writes the response headers and part of the body before
		// the download hangs.
		partial     bool
		closeStream bool
		wantErr     error
		within      time.Duration
	}{
		{
			name:    "body stalls",
			stall:   100 * time.Millisecond,
			partial: true,
			wantErr: errChunkStalled,
			within:  5 * time.Second,
		},
		{
			name:        "stream closed",
			stall:       time.Minute,
			closeStream: true,
			wantErr:     net.ErrClosed,
			within:      900 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requested := make(chan struct{}, 1)
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.Header.Get("Range"), "bytes=0-") {
					http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(chunkTrack))
					return
				}
				select {
				case requested <- struct{}{}:
				default:
				}
				if tt.partial {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", audio.DefaultChunkSize, len(chunkTrack)-1, len(chunkTrack)))
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(chunkTrack[:1024])
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })

			tr := newChunkTransport(tt.stall)
			t.Cleanup(tr.CloseIdleConnections)
			streamCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			reader, err := audio.NewHttpChunkedReader(&librespot.NullLogger{}, newSpotifyStreamHTTPClient(streamCtx, tr, tt.stall), srv.URL)
			if err != nil {
				t.Fatalf("NewHttpChunkedReader() error = %v", err)
			}

			readDone := make(chan error, 1)
			go func() {
				_, err := reader.ReadAt(make([]byte, 16), audio.DefaultChunkSize)
				readDone <- err
			}()
			<-requested
			start := time.Now()
			if tt.closeStream {
				cancel()
			}
			select {
			case err := <-readDone:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ReadAt() error = %v, want %v", err, tt.wantErr)
				}
				if elapsed := time.Since(start); elapsed > tt.within {
					t.Errorf("ReadAt() returned after %v, want at most %v", elapsed, tt.within)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("ReadAt() still waits for the stalled download")
			}
		})
	}
}

// freezeProxy forwards TCP connections to a backend. freeze stops the
// connections that are open at that time without closing them, as a network
// path change does. New connections still work.
type freezeProxy struct {
	ln      net.Listener
	backend string

	mu       sync.Mutex
	accepted int
	frozen   chan struct{}
	conns    []net.Conn
}

func newFreezeProxy(t *testing.T, backend string) *freezeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &freezeProxy{ln: ln, backend: backend, frozen: make(chan struct{})}
	t.Cleanup(p.close)
	go p.serve()
	return p
}

func (p *freezeProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.backend)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.mu.Lock()
		p.accepted++
		p.conns = append(p.conns, client, server)
		frozen := p.frozen
		p.mu.Unlock()
		go forwardUntilFrozen(server, client, frozen)
		go forwardUntilFrozen(client, server, frozen)
	}
}

// forwardUntilFrozen copies src to dst. After frozen closes, it drops the
// data, so neither side sees an error.
func forwardUntilFrozen(dst, src net.Conn, frozen <-chan struct{}) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		select {
		case <-frozen:
			continue
		default:
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

func (p *freezeProxy) freeze() {
	p.mu.Lock()
	close(p.frozen)
	p.frozen = make(chan struct{})
	p.mu.Unlock()
}

func (p *freezeProxy) connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

func (p *freezeProxy) close() {
	_ = p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// TestChunkTransportReplacesDeadHTTP2Connection opens a stream after a
// network path change killed the pooled HTTP/2 connection. A cancelled
// request does not close an HTTP/2 connection, so without the ping health
// check every retry of librespot uses the dead connection again.
func TestChunkTransportReplacesDeadHTTP2Connection(t *testing.T) {
	const stall = 400 * time.Millisecond
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(chunkTrack))
	}))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)
	proxy := newFreezeProxy(t, backend.Listener.Addr().String())

	roots := x509.NewCertPool()
	roots.AddCert(backend.Certificate())
	tr := newChunkTransport(stall)
	tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	t.Cleanup(tr.CloseIdleConnections)
	streamCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := newSpotifyStreamHTTPClient(streamCtx, tr, stall)
	trackURL := "https://" + proxy.ln.Addr().String() + "/track"

	// Open the HTTP/2 connection that the path change kills.
	req, err := http.NewRequest(http.MethodGet, trackURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-15")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("first request error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("first request used HTTP/%d, want HTTP/2", resp.ProtoMajor)
	}

	proxy.freeze()
	if _, err := audio.NewHttpChunkedReader(&librespot.NullLogger{}, client, trackURL); err != nil {
		t.Fatalf("NewHttpChunkedReader() after the path change error = %v", err)
	}
	if got := proxy.connections(); got < 2 {
		t.Fatalf("connections = %d, want a new connection after the path change", got)
	}
}
