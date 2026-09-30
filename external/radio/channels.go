package radio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// ChannelsURL lists the cliamp radio channels in the order the server runs
// them, with each channel's stream and the number of songs it exposes.
const ChannelsURL = "https://radio.cliamp.stream/stations"

// maxChannelsBody bounds the channel list, which is a few kilobytes.
const maxChannelsBody = 1 << 20

// maxChannelTracksBody bounds one channel's song list. Each song is a few
// hundred bytes, so this allows tens of thousands of songs.
const maxChannelTracksBody = 16 << 20

// channelTimeout bounds one request for the channel list or a song list.
const channelTimeout = 30 * time.Second

// Compile-time interface checks.
var (
	_ playlist.Provider  = (*ChannelProvider)(nil)
	_ playlist.Refresher = (*ChannelProvider)(nil)
)

// Channel is one cliamp radio channel. A channel with Tracks > 0 opens as a
// playlist of its songs. Any other channel plays its live stream.
type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Stream    string `json:"stream"`
	Tracks    int    `json:"tracks"`
	TracksURL string `json:"tracks_url"`
}

// hasSongs reports whether the channel opens as a playlist of songs.
func (c Channel) hasSongs() bool { return c.Tracks > 0 && c.TracksURL != "" }

// ChannelProvider lists only the cliamp radio channels, one playlist per
// channel. It is the view cliamp opens on. The Radio Browser directory stays
// in the Radio provider.
type ChannelProvider struct {
	mu       sync.Mutex
	channels []Channel // nil until the channel list loads
}

// NewChannels creates a provider for the cliamp radio channels. It loads the
// channel list on first use.
func NewChannels() *ChannelProvider { return &ChannelProvider{} }

func (*ChannelProvider) Name() string { return "cliamp radio" }

// Playlists returns one row per channel. The playlist ID is the channel slug.
// The first call downloads the channel list, so call it off the UI thread.
func (p *ChannelProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	channels, err := p.loadedChannels()
	if err != nil {
		return nil, err
	}
	out := make([]playlist.PlaylistInfo, 0, len(channels))
	for _, c := range channels {
		info := playlist.PlaylistInfo{ID: c.ID, Name: c.Name}
		if c.hasSongs() {
			info.TrackCount = c.Tracks
		} else {
			info.Name += " · live"
		}
		out = append(out, info)
	}
	return out, nil
}

// Tracks returns the songs of the channel with the given slug, or its live
// stream when the channel exposes no songs.
func (p *ChannelProvider) Tracks(id string) ([]playlist.Track, error) {
	channels, err := p.loadedChannels()
	if err != nil {
		return nil, err
	}
	var channel Channel
	found := false
	for _, c := range channels {
		if c.ID == id {
			channel, found = c, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("cliamp radio: unknown channel %q", id)
	}

	if !channel.hasSongs() {
		return []playlist.Track{{Path: channel.Stream, Title: channel.Name, Stream: true, Realtime: true}}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), channelTimeout)
	defer cancel()
	tracks, err := fetchChannelTracks(ctx, catalogClient, channel.TracksURL)
	if err != nil {
		return nil, fmt.Errorf("cliamp radio %s: %w", channel.Name, err)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("cliamp radio %s: the channel has no songs", channel.Name)
	}
	return tracks, nil
}

// Refresh drops the channel list, so the next call downloads it again with
// the current song counts. Implements playlist.Refresher.
func (p *ChannelProvider) Refresh() {
	p.mu.Lock()
	p.channels = nil
	p.mu.Unlock()
}

// loadedChannels returns the channel list, and downloads it when no list is
// loaded. A failed download leaves the list empty, so the next call retries.
func (p *ChannelProvider) loadedChannels() ([]Channel, error) {
	p.mu.Lock()
	channels := p.channels
	p.mu.Unlock()
	if channels != nil {
		return channels, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), channelTimeout)
	defer cancel()
	channels, err := fetchChannels(ctx, catalogClient, ChannelsURL)
	if err != nil {
		return nil, fmt.Errorf("cliamp radio channels: %w", err)
	}
	p.mu.Lock()
	p.channels = channels
	p.mu.Unlock()
	return channels, nil
}

func fetchChannels(ctx context.Context, client *http.Client, u string) ([]Channel, error) {
	var doc struct {
		Stations []Channel `json:"stations"`
	}
	if err := getLimitedJSON(ctx, client, u, maxChannelsBody, &doc); err != nil {
		return nil, err
	}
	// Channels with songs come first: they are the playlists cliamp opens on.
	// Each group keeps the server's order.
	var songs, live []Channel
	for _, c := range doc.Stations {
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" || !isHTTPURL(c.Stream) {
			continue
		}
		if c.Name == "" {
			c.Name = c.ID
		}
		if c.TracksURL != "" && !isHTTPURL(c.TracksURL) {
			c.TracksURL = ""
		}
		if c.hasSongs() {
			songs = append(songs, c)
		} else {
			live = append(live, c)
		}
	}
	channels := append(songs, live...)
	if len(channels) == 0 {
		return nil, errors.New("the server lists no channels")
	}
	return channels, nil
}

// channelSong is one song in a channel's song list.
type channelSong struct {
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Duration float64 `json:"duration"`
	URL      string  `json:"url"`
}

// fetchChannelTracks downloads a channel's song list. The songs are files, not
// live streams: they can be seeked, preloaded, and followed by the next song.
func fetchChannelTracks(ctx context.Context, client *http.Client, u string) ([]playlist.Track, error) {
	var doc struct {
		Tracks []channelSong `json:"tracks"`
	}
	if err := getLimitedJSON(ctx, client, u, maxChannelTracksBody, &doc); err != nil {
		return nil, err
	}
	tracks := make([]playlist.Track, 0, len(doc.Tracks))
	for _, s := range doc.Tracks {
		if !isHTTPURL(s.URL) {
			continue
		}
		tracks = append(tracks, playlist.Track{
			Path:         s.URL,
			Title:        s.Title,
			Artist:       s.Artist,
			Album:        s.Album,
			DurationSecs: int(math.Round(max(s.Duration, 0))),
			Stream:       true,
		})
	}
	return tracks, nil
}

// getLimitedJSON decodes the JSON document at u into v, reading at most limit
// bytes.
func getLimitedJSON(ctx context.Context, client *http.Client, u string, limit int64, v any) error {
	resp, err := get(ctx, client, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}
