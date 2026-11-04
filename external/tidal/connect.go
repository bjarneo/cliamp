package tidal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const connectQueueURL = "https://connectqueue.tidal.com/v1/queues"
const connectTimeout = 15 * time.Second

// Public Tidal trust anchors used by Connect receivers (TIDAL Root CA and TS CA).
// These are public certificates, not device credentials or private keys.
//
//go:embed connectdata/ca.pem
var connectCA []byte

type connectState struct {
	QueueID     string
	ItemID      string
	TrackID     string
	State       string
	Position    time.Duration
	Duration    time.Duration
	VolumeKnown bool
	Volume      float64
	Muted       bool
	Err         error
}

type connectSession struct {
	conn    *websocket.Conn
	cancel  context.CancelFunc
	done    chan struct{}
	mu      sync.Mutex
	state   connectState
	started chan error
	changed chan struct{}
	pending map[uint64]chan error
	nextID  uint64
}

func connectTLS() (*tls.Config, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(connectCA) {
		return nil, errors.New("tidal connect: invalid CA bundle")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Receivers use Tidal-issued certificates rather than LAN DNS names.
		// VerifyConnection replaces hostname verification with full chain verification.
		InsecureSkipVerify: true, //nolint:gosec // certificate chain is verified below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("tidal connect: missing receiver certificate")
			}
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates})
			return err
		},
	}, nil
}

func dialConnect(ctx context.Context, address, userID string) (*connectSession, error) {
	tlsConfig, err := connectTLS()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()
	return openConnect(ctx, "wss://"+address, &http.Client{Transport: transport}, userID)
}

func openConnect(ctx context.Context, endpoint string, httpc *http.Client, userID string) (*connectSession, error) {
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: httpc})
	if err != nil {
		return nil, fmt.Errorf("tidal connect: open receiver: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	run, cancel := context.WithCancel(context.Background())
	s := &connectSession{conn: conn, cancel: cancel, done: make(chan struct{}), started: make(chan error, 1), changed: make(chan struct{}, 1), pending: make(map[uint64]chan error)}
	go s.read(run)
	if err = wsjson.Write(ctx, conn, map[string]any{"command": "startSession", "appId": "tidal", "appName": "tidal", "sessionCredential": userID}); err == nil {
		select {
		case err = <-s.started:
		case <-ctx.Done():
			err = ctx.Err()
		case <-s.done:
			err = s.snapshot().Err
		}
	}
	if err != nil {
		s.close()
		return nil, fmt.Errorf("tidal connect: start session: %w", err)
	}
	go s.heartbeat(run)
	return s, nil
}

func (s *connectSession) snapshot() connectState { s.mu.Lock(); defer s.mu.Unlock(); return s.state }
func (s *connectSession) close()                 { s.cancel(); _ = s.conn.CloseNow(); <-s.done }
func (s *connectSession) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			ping, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := s.conn.Ping(ping)
			cancel()
			if err != nil {
				_ = s.conn.CloseNow()
				return
			}
		}
	}
}

func (s *connectSession) read(ctx context.Context) {
	defer close(s.done)
	defer s.conn.CloseNow()
	for {
		var msg struct {
			Command     string `json:"command"`
			RequestID   uint64 `json:"requestId"`
			ResultCode  int    `json:"resultCode"`
			PlayerState string `json:"playerState"`
			Progress    int64  `json:"progress"`
			Duration    int64  `json:"duration"`
			QueueInfo   struct {
				QueueID string `json:"queueId"`
			} `json:"queueInfo"`
			MediaInfo struct {
				MediaID string `json:"mediaId"`
				ItemID  string `json:"itemId"`
			} `json:"mediaInfo"`
			Volume *struct {
				Level *float64 `json:"level"`
				Mute  bool     `json:"mute"`
			} `json:"volume"`
		}
		if err := wsjson.Read(ctx, s.conn, &msg); err != nil {
			s.mu.Lock()
			s.state.Err = errors.New("tidal connect: receiver disconnected; select the output again to reconnect")
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		switch msg.Command {
		case "notifySessionStarted":
			select {
			case s.started <- nil:
			default:
			}
		case "notifySessionError", "notifySessionEnded":
			s.state.Err = errors.New("tidal connect: session ended or was rejected")
			select {
			case s.started <- s.state.Err:
			default:
			}
		case "notifyRequestResult":
			if ch := s.pending[msg.RequestID]; ch != nil {
				var err error
				if msg.ResultCode != 0 {
					err = fmt.Errorf("tidal connect: receiver rejected command (%d)", msg.ResultCode)
				}
				ch <- err
				delete(s.pending, msg.RequestID)
			}
		case "notifyPlayerStatusChanged":
			s.state.State = msg.PlayerState
			s.state.Position = time.Duration(msg.Progress) * time.Millisecond
			s.state.Duration = time.Duration(msg.Duration) * time.Millisecond
		case "notifyQueueChanged":
			s.state.QueueID = msg.QueueInfo.QueueID
		case "notifyMediaChanged":
			s.state.TrackID = msg.MediaInfo.MediaID
			s.state.ItemID = msg.MediaInfo.ItemID
		case "notifyDeviceStatusChanged":
			if msg.Volume != nil && msg.Volume.Level != nil && *msg.Volume.Level >= 0 && *msg.Volume.Level <= 1 {
				s.state.Volume = *msg.Volume.Level
				s.state.Muted = msg.Volume.Mute
				s.state.VolumeKnown = true
			}
		}
		s.mu.Unlock()
		if msg.Command == "notifySessionError" || msg.Command == "notifySessionEnded" {
			return
		}
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
}

func (s *connectSession) command(ctx context.Context, name string, args map[string]any) error {
	if args == nil {
		args = make(map[string]any)
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan error, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	args["command"] = name
	args["requestId"] = id
	if err := wsjson.Write(ctx, s.conn, args); err != nil {
		return fmt.Errorf("tidal connect: send %s: %w", name, err)
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return fmt.Errorf("tidal connect: %s: %w", name, ctx.Err())
	case <-s.done:
		return s.snapshot().Err
	}
}

func (s *connectSession) wait(ctx context.Context, condition func(connectState) bool) error {
	for {
		state := s.snapshot()
		if state.Err != nil {
			return state.Err
		}
		if condition(state) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return s.snapshot().Err
		case <-s.changed:
		}
	}
}

type connectQueue struct {
	ID    string `json:"id"`
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
}

func (c *client) createConnectQueue(ctx context.Context, endpoint, trackID string) (connectQueue, error) {
	payload, _ := json.Marshal(map[string]any{
		"properties":  map[string]any{"position": 0},
		"repeat_mode": "off", "shuffled": false,
		"items": []any{map[string]any{
			"type": "track", "media_id": trackID,
			"properties": map[string]any{"active": false, "original_order": 0},
		}},
	})
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureToken(ctx, ""); err != nil {
			return connectQueue{}, err
		}
		c.mu.Lock()
		token := c.accessToken
		c.mu.Unlock()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
		if err != nil {
			return connectQueue{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return connectQueue{}, fmt.Errorf("tidal connect: create queue: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if err := c.ensureToken(ctx, token); err != nil {
				return connectQueue{}, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return connectQueue{}, fmt.Errorf("tidal connect: create queue: HTTP %d", resp.StatusCode)
		}
		if readErr != nil {
			return connectQueue{}, readErr
		}
		var q connectQueue
		if err := json.Unmarshal(body, &q); err != nil {
			return q, fmt.Errorf("tidal connect: decode queue: %w", err)
		}
		if q.ID == "" || len(q.Items) != 1 || q.Items[0].ID == "" {
			return q, errors.New("tidal connect: incomplete queue response")
		}
		return q, nil
	}
	return connectQueue{}, errors.New("tidal connect: authentication failed")
}

func (c *client) connectAuth() any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"oauthServerInfo": map[string]any{
		"serverUrl": c.tokenURL,
		"authInfo": map[string]any{
			"headerAuth":      "Bearer " + c.accessToken,
			"oauthParameters": map[string]any{"accessToken": c.accessToken, "refreshToken": c.refreshToken},
		},
		"httpHeaderFields": []any{},
		"formParameters":   map[string]any{"scope": "r_usr", "grant_type": "switch_client"},
	}}
}

// loadConnectTrack gives the receiver its own one-item queue. cliamp continues
// to own shuffle, repeat and play-next; receiver notifications own audio state.
func (p *TidalProvider) loadConnectTrack(ctx context.Context, s *connectSession, path string, offset time.Duration, paused bool) (queueID string, err error) {
	id := strings.TrimPrefix(path, TrackURIPrefix)
	if id == path || id == "" || strings.Trim(id, "0123456789") != "" {
		return "", errors.New("tidal connect: this output only plays Tidal tracks")
	}
	c, err := p.ensureClient()
	if err != nil {
		return "", err
	}
	var track apiTrack
	if err = c.doGet(ctx, "tracks/"+id, nil, &track); err != nil {
		return "", err
	}
	q, err := c.createConnectQueue(ctx, c.queueURL, id)
	if err != nil {
		return "", err
	}
	artists := make([]string, 0, len(track.Artists))
	for _, artist := range track.Artists {
		artists = append(artists, artist.Name)
	}
	album := ""
	if track.Album != nil {
		album = track.Album.Title
	}
	server := func(url string, params map[string]any) any {
		return map[string]any{"serverUrl": url, "authInfo": c.connectAuth(), "httpHeaderFields": []any{}, "queryParameters": params}
	}
	args := map[string]any{
		"autoplay": false, "position": 0,
		"queueServerInfo": server(c.queueURL, map[string]any{}),
		"contentServerInfo": server(strings.TrimSuffix(c.baseURL, "/"), map[string]any{
			"audiomode": "STEREO", "audioquality": p.quality,
		}),
		"queueInfo": map[string]any{
			"queueId": q.ID, "repeatMode": "off", "shuffled": false,
			"maxBeforeSize": 10, "maxAfterSize": 10,
		},
		"currentMediaInfo": map[string]any{
			"itemId": q.Items[0].ID, "mediaId": id, "mediaType": 0,
			"metadata": map[string]any{
				"title": track.Title, "albumTitle": album, "artists": artists,
				"duration": track.Duration * 1000,
			},
		},
	}
	// A canceled command may already have reached the receiver. Reconcile it
	// before the worker starts another track, even when its result was lost.
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.command(cleanup, "pause", nil)
		}
	}()
	if err = s.command(ctx, "loadCloudQueue", args); err != nil {
		return "", err
	}
	if err = s.wait(ctx, func(st connectState) bool {
		return st.QueueID == q.ID && st.ItemID == q.Items[0].ID && st.State == "PAUSED"
	}); err != nil {
		return "", fmt.Errorf("tidal connect: confirm track: %w", err)
	}
	if offset > 0 {
		offset = min(offset, time.Duration(track.Duration)*time.Second)
		if err = s.seek(ctx, offset); err != nil {
			return "", err
		}
	}
	if !paused {
		if err = s.command(ctx, "play", nil); err != nil {
			return "", err
		}
	}
	if err = s.wait(ctx, func(st connectState) bool { return st.State == "PLAYING" && !paused || st.State == "PAUSED" && paused }); err != nil {
		return "", err
	}
	return q.ID, nil
}

func (s *connectSession) seek(ctx context.Context, target time.Duration) error {
	if err := s.command(ctx, "seek", map[string]any{"position": target.Milliseconds()}); err != nil {
		return err
	}
	return s.wait(ctx, func(st connectState) bool {
		return st.Position >= target-time.Second && st.Position <= target+2*time.Second
	})
}
