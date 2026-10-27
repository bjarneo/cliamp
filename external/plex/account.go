package plex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

const (
	accountBaseURL         = "https://plex.tv"
	accountProduct         = "cliamp"
	accountPollInterval    = time.Second
	connectionProbeTimeout = 4 * time.Second
	connectionSelectGrace  = 100 * time.Millisecond
)

// AccountAPI implements Plex browser sign-in and server discovery.
type AccountAPI struct {
	baseURL      string
	client       *http.Client
	pollInterval time.Duration
}

// NewAccountAPI returns an account client for plex.tv.
func NewAccountAPI() *AccountAPI {
	return &AccountAPI{
		baseURL:      accountBaseURL,
		client:       httpclient.NewAPI(30 * time.Second),
		pollInterval: accountPollInterval,
	}
}

// NewClientIdentifier returns a stable identifier suitable for one cliamp
// installation. Callers must persist it and reuse it for later sign-ins.
func NewClientIdentifier() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("plex: create client identifier: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

// SignInSession is one pending Plex browser sign-in.
type SignInSession struct {
	api      *AccountAPI
	id       int64
	code     string
	clientID string
}

// BeginSignIn creates a strong Plex PIN for browser authorization.
func (a *AccountAPI) BeginSignIn(ctx context.Context, clientID string) (*SignInSession, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("plex: sign in: missing client identifier")
	}
	var pin struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	if err := a.doJSON(ctx, http.MethodPost, "/api/v2/pins?strong=true", clientID, "", &pin); err != nil {
		return nil, fmt.Errorf("plex: start sign in: %w", err)
	}
	if pin.ID == 0 || pin.Code == "" {
		return nil, fmt.Errorf("plex: start sign in: incomplete PIN response")
	}
	return &SignInSession{api: a, id: pin.ID, code: pin.Code, clientID: clientID}, nil
}

// URL returns the Plex-hosted page where the user approves this sign-in.
func (s *SignInSession) URL() string {
	params := url.Values{
		"clientID":                 {s.clientID},
		"code":                     {s.code},
		"context[device][product]": {accountProduct},
	}
	return "https://app.plex.tv/auth#?" + params.Encode()
}

// Wait polls Plex until the browser sign-in is approved or ctx ends.
func (s *SignInSession) Wait(ctx context.Context) (string, error) {
	path := fmt.Sprintf("/api/v2/pins/%d?code=%s", s.id, url.QueryEscape(s.code))
	for {
		var pin struct {
			AuthToken string `json:"authToken"`
		}
		if err := s.api.doJSON(ctx, http.MethodGet, path, s.clientID, "", &pin); err != nil {
			return "", fmt.Errorf("plex: wait for sign in: %w", err)
		}
		if pin.AuthToken != "" {
			return pin.AuthToken, nil
		}
		timer := time.NewTimer(s.api.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("plex: wait for sign in: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// AccountServer is one Plex Media Server available to the signed-in account.
type AccountServer struct {
	Name             string              `json:"name"`
	ClientIdentifier string              `json:"clientIdentifier"`
	Provides         string              `json:"provides"`
	AccessToken      string              `json:"accessToken"`
	Owned            bool                `json:"owned"`
	Connections      []AccountConnection `json:"connections"`
}

// AccountConnection is one advertised route to a Plex Media Server.
type AccountConnection struct {
	URI      string `json:"uri"`
	Protocol string `json:"protocol"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
}

// ReachableServer pairs a server with the best connection reachable now.
type ReachableServer struct {
	Name             string
	ClientIdentifier string
	URL              string
	AccessToken      string
	Owned            bool
}

// Servers returns the Plex Media Servers available to the account.
func (a *AccountAPI) Servers(ctx context.Context, accountToken, clientID string) ([]AccountServer, error) {
	var resources []AccountServer
	path := "/api/v2/resources?includeHttps=1&includeRelay=1&includeIPv6=0"
	if err := a.doJSON(ctx, http.MethodGet, path, clientID, accountToken, &resources); err != nil {
		return nil, fmt.Errorf("plex: discover servers: %w", err)
	}
	servers := resources[:0]
	for _, resource := range resources {
		if resource.ClientIdentifier != "" && providesServer(resource.Provides) {
			servers = append(servers, resource)
		}
	}
	slices.SortFunc(servers, func(a, b AccountServer) int {
		if a.Owned != b.Owned {
			if a.Owned {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return servers, nil
}

func providesServer(provides string) bool {
	for _, capability := range strings.Split(provides, ",") {
		if strings.TrimSpace(capability) == "server" {
			return true
		}
	}
	return false
}

// ReachableServers returns servers with at least one connection that answers.
func (a *AccountAPI) ReachableServers(ctx context.Context, accountToken, clientID string) ([]ReachableServer, error) {
	servers, err := a.Servers(ctx, accountToken, clientID)
	if err != nil {
		return nil, err
	}
	type probeResult struct {
		index      int
		connection AccountConnection
		token      string
		err        error
	}
	results := make(chan probeResult, len(servers))
	for i, server := range servers {
		go func() {
			connection, token, err := a.ReachableConnection(ctx, server, accountToken, clientID)
			results <- probeResult{index: i, connection: connection, token: token, err: err}
		}()
	}
	reachableByIndex := make([]*ReachableServer, len(servers))
	for range servers {
		result := <-results
		if result.err != nil {
			continue
		}
		server := servers[result.index]
		reachableByIndex[result.index] = &ReachableServer{
			Name:             server.Name,
			ClientIdentifier: server.ClientIdentifier,
			URL:              result.connection.URI,
			AccessToken:      result.token,
			Owned:            server.Owned,
		}
	}
	reachable := make([]ReachableServer, 0, len(servers))
	for _, server := range reachableByIndex {
		if server != nil {
			reachable = append(reachable, *server)
		}
	}
	if len(reachable) == 0 {
		if len(servers) == 0 {
			return nil, fmt.Errorf("plex: no Plex Media Servers are available to this account")
		}
		return nil, fmt.Errorf("plex: none of the account's Plex Media Servers are reachable")
	}
	return reachable, nil
}

// ConnectServer rediscovers one selected server and resolves its best current
// connection. This lets a saved account configuration follow address changes
// and move between local and remote networks.
func (a *AccountAPI) ConnectServer(ctx context.Context, accountToken, clientID, serverID string) (ReachableServer, error) {
	servers, err := a.Servers(ctx, accountToken, clientID)
	if err != nil {
		return ReachableServer{}, err
	}
	for _, server := range servers {
		if server.ClientIdentifier != serverID {
			continue
		}
		connection, token, err := a.ReachableConnection(ctx, server, accountToken, clientID)
		if err != nil {
			return ReachableServer{}, err
		}
		return ReachableServer{
			Name:             server.Name,
			ClientIdentifier: server.ClientIdentifier,
			URL:              connection.URI,
			AccessToken:      token,
			Owned:            server.Owned,
		}, nil
	}
	return ReachableServer{}, fmt.Errorf("plex: selected server is no longer available to this account")
}

// ReachableConnection returns the best currently reachable connection for a server.
func (a *AccountAPI) ReachableConnection(ctx context.Context, server AccountServer, accountToken, clientID string) (AccountConnection, string, error) {
	token := server.AccessToken
	if token == "" {
		token = accountToken
	}
	candidates := slices.Clone(server.Connections)
	slices.SortStableFunc(candidates, func(a, b AccountConnection) int {
		return connectionRank(a) - connectionRank(b)
	})
	valid := candidates[:0]
	for _, connection := range candidates {
		connection.URI = strings.TrimRight(connection.URI, "/")
		if connectionRank(connection) >= 100 || !looksLikeServerURL(connection.URI) {
			continue
		}
		valid = append(valid, connection)
	}
	if len(valid) == 0 {
		return AccountConnection{}, "", fmt.Errorf("plex: server %q has no reachable secure connection", server.Name)
	}

	type probeResult struct {
		connection AccountConnection
		rank       int
		err        error
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan probeResult, len(valid))
	for _, connection := range valid {
		go func() {
			requestCtx, requestCancel := context.WithTimeout(probeCtx, connectionProbeTimeout)
			defer requestCancel()
			results <- probeResult{
				connection: connection,
				rank:       connectionRank(connection),
				err:        a.probe(requestCtx, connection.URI, token, clientID),
			}
		}()
	}

	var (
		best       AccountConnection
		bestRank   = 101
		grace      *time.Timer
		graceTimer <-chan time.Time
	)
	for range valid {
		select {
		case <-ctx.Done():
			return AccountConnection{}, "", fmt.Errorf("plex: probe server %q: %w", server.Name, ctx.Err())
		case <-graceTimer:
			return best, token, nil
		case result := <-results:
			if result.err != nil {
				continue
			}
			if result.rank < bestRank {
				best, bestRank = result.connection, result.rank
			}
			if bestRank == 0 {
				return best, token, nil
			}
			if grace == nil {
				grace = time.NewTimer(connectionSelectGrace)
				defer grace.Stop()
				graceTimer = grace.C
			}
		}
	}
	if bestRank < 101 {
		return best, token, nil
	}
	return AccountConnection{}, "", fmt.Errorf("plex: server %q has no reachable secure connection", server.Name)
}

func connectionRank(connection AccountConnection) int {
	secure := strings.EqualFold(connection.Protocol, "https") || strings.HasPrefix(connection.URI, "https://")
	switch {
	case secure && connection.Local:
		return 0
	case connection.Local && !connection.Relay:
		return 1 // plain HTTP is acceptable only inside the LAN
	case secure && !connection.Relay:
		return 2 // direct remote connection
	case secure && connection.Relay:
		return 3 // Plex Relay may be bandwidth-limited
	default:
		return 100 // never send a Plex token over public plain HTTP
	}
}

func looksLikeServerURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func (a *AccountAPI) probe(ctx context.Context, baseURL, token, clientID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/identity", nil)
	if err != nil {
		return err
	}
	setAccountHeaders(req, clientID, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

// ErrTokenStillAccepted reports that plex.tv still accepts a token after the
// sign-out request, so the token is not known to be revoked.
var ErrTokenStillAccepted = errors.New("plex: plex.tv still accepts the token after sign-out")

// SignOut revokes accountToken at plex.tv and then checks that plex.tv
// rejects it. Only a token that cliamp obtained through browser sign-in may
// be revoked: a token copied from Plex Web belongs to that browser session.
func (a *AccountAPI) SignOut(ctx context.Context, accountToken, clientID string) error {
	if strings.TrimSpace(accountToken) == "" {
		return fmt.Errorf("plex: sign out: no token")
	}
	status, err := a.do(ctx, http.MethodDelete, "/api/v2/users/signout", clientID, accountToken)
	if err != nil {
		return fmt.Errorf("plex: sign out: %w", err)
	}
	// 401 means the token was already invalid, which is the goal.
	if (status < 200 || status >= 300) && status != http.StatusUnauthorized {
		return fmt.Errorf("plex: sign out: HTTP %d", status)
	}
	status, err = a.do(ctx, http.MethodGet, "/api/v2/user", clientID, accountToken)
	if err != nil {
		return fmt.Errorf("plex: check sign-out: %w", err)
	}
	switch {
	case status == http.StatusUnauthorized:
		return nil
	case status >= 200 && status < 300:
		return ErrTokenStillAccepted
	}
	return fmt.Errorf("plex: check sign-out: HTTP %d", status)
}

// do sends a request without a body and returns the response status. The
// response body is drained and discarded.
func (a *AccountAPI) do(ctx context.Context, method, path, clientID, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	setAccountHeaders(req, clientID, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return resp.StatusCode, nil
}

func (a *AccountAPI) doJSON(ctx context.Context, method, path, clientID, token string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, nil)
	if err != nil {
		return err
	}
	setAccountHeaders(req, clientID, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := httpclient.ReadJSON(resp.Body, maxResponseBody, result); err != nil {
		return err
	}
	return nil
}

func setAccountHeaders(req *http.Request, clientID, token string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", accountProduct)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
}
