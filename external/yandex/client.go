// Package yandex implements a playlist.Provider for Yandex Music.
package yandex

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.music.yandex.net"
	// trackURISecret is the public signing secret used to build CDN stream URLs.
	trackURISecret = "XGRlBW9FXlekgbPrRHuSiA"
	apiTimeout     = 15 * time.Second
	dlTimeout      = 30 * time.Second
	timestampFmt   = "2006-01-02T15:04:05.999Z"
)

// client is a minimal Yandex Music API client authenticated with a personal
// OAuth token.
type client struct {
	http    *http.Client
	token   string
	apiBase string
}

func newClient(token string) *client {
	return &client{
		http:    &http.Client{Timeout: apiTimeout},
		token:   token,
		apiBase: defaultAPIBase,
	}
}

// apiGet performs an authenticated GET against the Yandex Music API and
// decodes the "result" envelope into out.
func (c *client) apiGet(path string, params url.Values, out any) error {
	endpoint := c.apiBase + path
	if params != nil {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPIResponse(resp, out)
}

// apiPost performs an authenticated form POST against the Yandex Music API.
func (c *client) apiPost(path string, params url.Values, out any) error {
	req, err := http.NewRequest(http.MethodPost, c.apiBase+path, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPIResponse(resp, out)
}

// apiPostJSON performs an authenticated JSON POST against the Yandex Music API.
func (c *client) apiPostJSON(path string, params url.Values, body any, out any) error {
	endpoint := c.apiBase + path
	if params != nil {
		endpoint += "?" + params.Encode()
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPIResponse(resp, out)
}

// rotorStartWave starts a "Моя волна" (My Wave) personal radio session and
// returns its initial track batch together with the session and batch ids
// needed for continuations and feedback.
func (c *client) rotorStartWave() ([]track, string, string, error) {
	body := map[string]any{
		"includeTracksInResponse": true,
		"includeWaveModel":        false,
		"interactive":             true,
		"seeds":                   []string{"user:onyourwave"},
	}
	var session rotorSession
	if err := c.apiPostJSON("/rotor/session/new", nil, body, &session); err != nil {
		return nil, "", "", err
	}
	tracks := sessionTracks(session)
	if len(tracks) == 0 {
		return nil, "", "", fmt.Errorf("yandex: wave session returned no tracks")
	}
	return tracks, session.RadioSessionID, session.BatchID, nil
}

// rotorWaveTracks fetches the next wave batch for an ongoing session. The
// queue lists previously served tracks in "trackId:albumId" form so the
// service does not repeat them; feedbacks may be nil.
func (c *client) rotorWaveTracks(sessionID string, feedbacks []rotorFeedback, queue []string) ([]track, string, error) {
	if feedbacks == nil {
		feedbacks = []rotorFeedback{}
	}
	if queue == nil {
		queue = []string{}
	}
	body := map[string]any{"feedbacks": feedbacks, "queue": queue}
	var session rotorSession
	if err := c.apiPostJSON("/rotor/session/"+sessionID+"/tracks", nil, body, &session); err != nil {
		return nil, "", err
	}
	tracks := sessionTracks(session)
	return tracks, session.BatchID, nil
}

// rotorWaveFeedback posts a track playback event (trackStarted, trackFinished)
// for the wave session so future batches adapt to real listening.
func (c *client) rotorWaveFeedback(sessionID, batchID, eventType, trackKey string, lengthSecs, playedSecs float64) error {
	event := rotorFeedbackEvent{
		Timestamp: time.Now().Format(timestampFmt),
		Type:      eventType,
		From:      waveFrom,
		TrackID:   trackKey,
	}
	switch eventType {
	case rotorTrackStarted:
		event.TrackLengthSeconds = lengthSecs
	case rotorTrackFinished:
		event.TrackLengthSeconds = lengthSecs
		event.TotalPlayedSeconds = playedSecs
	}
	feedback := rotorFeedback{From: waveFrom, BatchID: batchID, Event: event}
	return c.apiPostJSON("/rotor/session/"+sessionID+"/feedback", nil, feedback, nil)
}

func sessionTracks(s rotorSession) []track {
	tracks := make([]track, 0, len(s.Sequence))
	for _, item := range s.Sequence {
		if item.Type == "track" && string(item.Track.ID) != "" {
			tracks = append(tracks, item.Track)
		}
	}
	return tracks
}

const waveFrom = "cliamp-wave-default"

const (
	rotorTrackStarted  = "trackStarted"
	rotorTrackFinished = "trackFinished"
)

type rotorFeedback struct {
	From    string             `json:"from"`
	BatchID string             `json:"batchId,omitempty"`
	Event   rotorFeedbackEvent `json:"event"`
}

type rotorFeedbackEvent struct {
	Timestamp          string  `json:"timestamp"`
	Type               string  `json:"type"`
	From               string  `json:"from,omitempty"`
	TotalPlayedSeconds float64 `json:"totalPlayedSeconds,omitempty"`
	TrackLengthSeconds float64 `json:"trackLengthSeconds,omitempty"`
	TrackID            string  `json:"trackId,omitempty"`
}

type rotorSequenceItem struct {
	Type  string `json:"type"`
	Track track  `json:"track"`
}

type rotorSession struct {
	Sequence       []rotorSequenceItem `json:"sequence"`
	BatchID        string              `json:"batchId"`
	RadioSessionID string              `json:"radioSessionId"`
}

func (c *client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "OAuth "+c.token)
	// Emulating the official Android client is required for download info.
	req.Header.Set("X-Yandex-Music-Client", "YandexMusicAndroid/24024312")
	req.Header.Set("User-Agent", "okhttp/4.12.0")
}

func decodeAPIResponse(resp *http.Response, out any) error {
	if resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("yandex: unauthorized (%s)", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("yandex: http status %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("yandex: decode response: %w", err)
	}
	if out != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("yandex: decode result: %w", err)
		}
	}
	return nil
}

// accountStatus verifies the token and returns the account user id.
func (c *client) accountStatus() (uint64, error) {
	var status struct {
		Account struct {
			UID uint64 `json:"uid"`
		} `json:"account"`
	}
	if err := c.apiGet("/account/status", nil, &status); err != nil {
		return 0, err
	}
	if status.Account.UID == 0 {
		return 0, fmt.Errorf("yandex: account status has no user id")
	}
	return status.Account.UID, nil
}

// playlists returns the signed-in user's playlists.
func (c *client) playlists(userID uint64) ([]remotePlaylist, error) {
	var lists []remotePlaylist
	err := c.apiGet(fmt.Sprintf("/users/%d/playlists/list", userID), nil, &lists)
	return lists, err
}

// playlistTracks returns the full track list of one playlist.
func (c *client) playlistTracks(userID, kind uint64) ([]track, error) {
	params := url.Values{
		"kinds":       {strconv.FormatUint(kind, 10)},
		"mixed":       {"false"},
		"rich-tracks": {"true"},
	}
	var lists []remotePlaylist
	if err := c.apiGet(fmt.Sprintf("/users/%d/playlists", userID), params, &lists); err != nil {
		return nil, err
	}
	if len(lists) != 1 {
		return nil, fmt.Errorf("yandex: expected one playlist, got %d", len(lists))
	}
	tracks := make([]track, 0, len(lists[0].Tracks))
	for _, t := range lists[0].Tracks {
		tracks = append(tracks, t.Track)
	}
	return tracks, nil
}

// likedTracks returns the user's liked track ids (in "trackId:albumId" form).
func (c *client) likedTracks(userID uint64) ([]string, error) {
	var desc struct {
		Library struct {
			Tracks []struct {
				ID string `json:"id"`
			} `json:"tracks"`
		} `json:"library"`
	}
	if err := c.apiGet(fmt.Sprintf("/users/%d/likes/tracks", userID), nil, &desc); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(desc.Library.Tracks))
	for _, t := range desc.Library.Tracks {
		ids = append(ids, t.ID)
	}
	return ids, nil
}

// tracks resolves track details for the given ids in one batched request.
func (c *client) tracks(ids []string) ([]track, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []track
	err := c.apiPost("/tracks", url.Values{
		"track-ids":      ids,
		"with-positions": {"false"},
	}, &out)
	return out, err
}

// search performs a track search.
func (c *client) search(query string, limit int) ([]track, error) {
	perPage := 20
	if limit > 0 && limit <= 50 {
		perPage = limit
	}
	params := url.Values{
		"text":     {query},
		"type":     {"track"},
		"page":     {"0"},
		"per-page": {strconv.Itoa(perPage)},
	}
	var result struct {
		Tracks struct {
			Results []track `json:"results"`
		} `json:"tracks"`
	}
	if err := c.apiGet("/search", params, &result); err != nil {
		return nil, err
	}
	return result.Tracks.Results, nil
}

// streamURL resolves a direct, signed CDN URL for one track. The returned URL
// is self-authorizing and stays valid for a limited time.
func (c *client) streamURL(trackID string) (string, error) {
	var infos []downloadInfo
	if err := c.apiGet("/tracks/"+trackID+"/download-info", nil, &infos); err != nil {
		return "", err
	}
	info, ok := bestDownloadInfo(infos)
	if !ok {
		return "", fmt.Errorf("yandex: no suitable download info for track %s", trackID)
	}

	full, err := c.fullDownloadInfo(info.DownloadInfoURL)
	if err != nil {
		return "", err
	}
	return buildStreamURL(info, full), nil
}

// buildStreamURL constructs the self-authorizing CDN URL from download info.
func buildStreamURL(info downloadInfo, full fullDownloadInfo) string {
	sum := md5.Sum([]byte(trackURISecret + full.Path[1:] + full.S))
	hash := hex.EncodeToString(sum[:])
	return "https://" + full.Host + "/get-" + info.Codec + "/" + hash + "/" + full.Ts + full.Path
}

func (c *client) fullDownloadInfo(infoURL string) (fullDownloadInfo, error) {
	var full fullDownloadInfo
	req, err := http.NewRequest(http.MethodGet, infoURL+"&format=json", nil)
	if err != nil {
		return full, err
	}
	c.setHeaders(req)

	dl := &http.Client{Timeout: dlTimeout}
	resp, err := dl.Do(req)
	if err != nil {
		return full, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return full, fmt.Errorf("yandex: download info status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
		return full, fmt.Errorf("yandex: decode download info: %w", err)
	}
	if full.Host == "" || len(full.Path) < 2 {
		return full, fmt.Errorf("yandex: incomplete download info")
	}
	return full, nil
}

// reportPlayback posts play-audio feedback so the service tracks listening.
func (c *client) reportPlayback(userID uint64, trackID string, playedSeconds int) error {
	params := url.Values{
		"uid":                  {strconv.FormatUint(userID, 10)},
		"track-id":             {trackID},
		"from":                 {"cliamp"},
		"play-id":              {time.Now().Format(timestampFmt)},
		"track-length-seconds": {strconv.Itoa(playedSeconds)},
		"total-played-seconds": {strconv.Itoa(playedSeconds)},
		"timestamp":            {time.Now().Format(timestampFmt)},
	}
	return c.apiPost("/play-audio", params, nil)
}

// IsStreamURL reports whether u is a Yandex Music signed stream endpoint.
// These are finite MP3 files with a known length, so the player can use the
// buffered download pipeline for them.
func IsStreamURL(u string) bool {
	return strings.Contains(u, "//api.music.yandex.net/get-") ||
		strings.Contains(u, ".strm.yandex.net/")
}

type fullDownloadInfo struct {
	Host string `json:"host"`
	Path string `json:"path"`
	Ts   string `json:"ts"`
	S    string `json:"s"`
}

type downloadInfo struct {
	Codec           string `json:"codec"`
	BitrateInKbps   int    `json:"bitrateInKbps"`
	Preview         bool   `json:"preview"`
	DownloadInfoURL string `json:"downloadInfoUrl"`
}

// bestDownloadInfo picks the highest quality entry, preferring non-preview
// MP3 (natively decodable by cliamp), then AAC, then bitrate.
func bestDownloadInfo(infos []downloadInfo) (downloadInfo, bool) {
	var best downloadInfo
	found := false
	for _, info := range infos {
		if info.DownloadInfoURL == "" {
			continue
		}
		if !found || betterDownloadInfo(info, best) {
			best = info
			found = true
		}
	}
	return best, found
}

func betterDownloadInfo(a, b downloadInfo) bool {
	if ra, rb := downloadInfoRank(a), downloadInfoRank(b); ra != rb {
		return ra > rb
	}
	return a.BitrateInKbps > b.BitrateInKbps
}

func downloadInfoRank(info downloadInfo) int {
	codec := 0
	switch info.Codec {
	case "mp3":
		codec = 2
	case "aac":
		codec = 1
	}
	if info.Preview {
		return codec
	}
	return codec*2 + 1
}

// plainID strips the ":albumId" suffix from like ids.
func plainID(id string) string {
	if v, _, ok := strings.Cut(id, ":"); ok {
		return v
	}
	return id
}

type remotePlaylist struct {
	UID        uint64 `json:"uid"`
	Kind       uint64 `json:"kind"`
	Title      string `json:"title"`
	TrackCount int    `json:"trackCount"`
	Owner      struct {
		UID uint64 `json:"uid"`
	} `json:"owner"`
	Tracks []struct {
		Track track `json:"track"`
	} `json:"tracks"`
}

type track struct {
	ID         flexString `json:"id"`
	RealID     flexString `json:"realId"`
	Title      string     `json:"title"`
	Version    string     `json:"version"`
	Available  bool       `json:"available"`
	DurationMs int        `json:"durationMs"`
	Artists    []artist   `json:"artists"`
	Albums     []album    `json:"albums"`
	Error      *apiError  `json:"error"`
}

// flexString accepts JSON values that arrive as strings or numbers. The
// Yandex Music API returns track ids as strings from /tracks but as numbers
// from /search.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		var fl float64
		if err := json.Unmarshal(data, &fl); err != nil {
			return err
		}
		*f = flexString(strconv.FormatInt(int64(fl), 10))
		return nil
	}
	*f = flexString(strconv.FormatInt(n, 10))
	return nil
}

type artist struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type album struct {
	ID    uint64 `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"year"`
}

type apiError struct {
	Name    string
	Message string
}

// UnmarshalJSON accepts errors that arrive as plain strings ("not-available")
// or as objects ({name, message}).
func (e *apiError) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*e = apiError{Name: s}
		return nil
	}
	var raw struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = apiError{Name: raw.Name, Message: raw.Message}
	return nil
}

func (e *apiError) Error() string {
	if e == nil {
		return "yandex: unknown track error"
	}
	if e.Message != "" {
		return "yandex: " + e.Name + ": " + e.Message
	}
	return "yandex: " + e.Name
}
