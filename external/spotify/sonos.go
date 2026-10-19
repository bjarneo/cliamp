package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/sonos"
)

const sonosSeedCacheName = "sonos_discovery_seed.json"

const sonosDJContextURI = "spotify:playlist:37i9dQZF1EYkqdzj48dyYq"

type SonosDevice struct {
	ID          string   `json:"id"`
	RoomID      string   `json:"room_id"`
	Name        string   `json:"name"`
	Coordinator string   `json:"coordinator"`
	Members     []string `json:"members"`
	MemberIDs   []string `json:"member_ids"`
	Bonded      bool     `json:"bonded"`
	Satellite   bool     `json:"satellite"`
}

type SonosState = sonos.State
type SonosTrack = sonos.Track
type SonosEQ = sonos.EQ

var sonosClient = sonos.NewClient(http.DefaultClient)
var discoverSonosSSDP = func(ctx context.Context, client *sonos.Client) ([]sonos.Device, error) { return client.Discover(ctx) }

type sonosDiscoverySeed struct {
	Address string `json:"address"`
	ID      string `json:"id"`
}

func SonosDevices(ctx context.Context) ([]SonosDevice, error) {
	discovered, err := discoverSonos(ctx, sonosClient, os.Getenv("CLIAMP_SONOS_TARGET"), sonosSeedCachePath)
	if err != nil {
		return nil, err
	}
	return sonosOutputDevices(discovered), nil
}

func discoverSonos(ctx context.Context, client *sonos.Client, explicitTarget string, cachePath func() (string, error)) ([]sonos.Device, error) {
	clientCopy := *client
	client = &clientCopy
	if explicitTarget != "" {
		client.Target = explicitTarget
		discovered, err := client.Discover(ctx)
		if err != nil {
			return nil, err
		}
		if path, pathErr := cachePath(); pathErr == nil {
			if seed, ok := sonosSeed(discovered); ok {
				for _, device := range discovered {
					if device.Address == explicitTarget && device.ID != "" {
						seed = sonosDiscoverySeed{Address: device.Address, ID: device.ID}
						break
					}
				}
				_ = saveSonosSeed(path, seed)
			}
		}
		return discovered, nil
	}

	if path, err := cachePath(); err == nil {
		if seed, err := loadSonosSeed(path); err == nil {
			seedCtx, cancel := context.WithTimeout(ctx, sonosSeedTimeout)
			household, discoverErr := client.Household(seedCtx, seed.Address)
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if discoverErr == nil && hasSonosSeed(household, seed) {
				return household, nil
			}
		}
	}

	discovered, err := discoverSonosSSDP(ctx, client)
	if err != nil {
		return nil, err
	}
	if path, pathErr := cachePath(); pathErr == nil {
		if seed, ok := sonosSeed(discovered); ok {
			_ = saveSonosSeed(path, seed)
		}
	}
	return discovered, nil
}

const sonosSeedTimeout = 1500 * time.Millisecond

func sonosSeedCachePath() (string, error) {
	dir, err := appdir.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sonosSeedCacheName), nil
}

func loadSonosSeed(path string) (sonosDiscoverySeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sonosDiscoverySeed{}, err
	}
	var seed sonosDiscoverySeed
	if err := json.Unmarshal(data, &seed); err != nil || net.ParseIP(seed.Address) == nil || !strings.HasPrefix(seed.ID, "RINCON_") {
		return sonosDiscoverySeed{}, fmt.Errorf("invalid Sonos discovery seed")
	}
	return seed, nil
}

func saveSonosSeed(path string, seed sonosDiscoverySeed) error {
	data, err := json.Marshal(seed)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func sonosSeed(devices []sonos.Device) (sonosDiscoverySeed, bool) {
	for _, device := range devices {
		if net.ParseIP(device.Address) != nil && device.ID != "" {
			return sonosDiscoverySeed{Address: device.Address, ID: device.ID}, true
		}
	}
	return sonosDiscoverySeed{}, false
}

func hasSonosSeed(devices []sonos.Device, seed sonosDiscoverySeed) bool {
	for _, device := range devices {
		if device.Address == seed.Address && device.ID == seed.ID {
			return true
		}
	}
	return false
}

func sonosOutputDevices(discovered []sonos.Device) []SonosDevice {
	addresses := make(map[string]string, len(discovered))
	for _, device := range discovered {
		addresses[device.ID] = device.Address
	}
	devices := make([]SonosDevice, 0, len(discovered))
	for _, device := range discovered {
		members := make([]string, 0, len(device.Members))
		for _, id := range device.Members {
			if address := addresses[id]; address != "" {
				members = append(members, address)
			}
		}
		coordinator := addresses[device.Coordinator]
		if coordinator == "" {
			coordinator = device.Address
		}
		devices = append(devices, SonosDevice{
			ID: device.Address, RoomID: device.ID, Name: device.Name, Coordinator: coordinator,
			Members: members, MemberIDs: append([]string(nil), device.Members...), Bonded: len(device.Bonded) > 0, Satellite: containsSonosID(device.Bonded, device.ID),
		})
	}
	return devices
}

func containsSonosID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func SonosControl(ctx context.Context, action, address, uri string) error {
	if uri == "spotify:dj" || uri == sonosDJContextURI || "spotify:playlist:"+uri == sonosDJContextURI || strings.HasPrefix(uri, "spotify:dj:") {
		return fmt.Errorf("Spotify DJ requires Local output; Sonos DJ is unverified")
	}
	if net.ParseIP(address) == nil {
		return fmt.Errorf("Sonos target must be a speaker IP address")
	}
	switch action {
	case "play", "pause", "toggle", "next", "previous":
	default:
		return fmt.Errorf("unsupported Sonos action %q", action)
	}
	return sonosClient.Control(ctx, address, action, uri)
}

func SonosGroupSet(ctx context.Context, room, coordinator string, join bool) error {
	if net.ParseIP(room) == nil || net.ParseIP(coordinator) == nil {
		return fmt.Errorf("Sonos group targets must be speaker IP addresses")
	}
	if join {
		return sonosClient.Join(ctx, room, coordinator)
	}
	return sonosClient.Unjoin(ctx, room)
}

func SonosSetGroup(ctx context.Context, rooms []string, playback string) error {
	client, err := sonosGroupingClient(ctx)
	if err != nil {
		return err
	}
	return client.SetGroup(ctx, rooms, playback)
}

func SonosSeparateGroup(ctx context.Context, room string) error {
	client, err := sonosGroupingClient(ctx)
	if err != nil {
		return err
	}
	return client.SeparateGroup(ctx, room)
}

func sonosGroupingClient(ctx context.Context) (*sonos.Client, error) {
	discovered, err := discoverSonos(ctx, sonosClient, os.Getenv("CLIAMP_SONOS_TARGET"), sonosSeedCachePath)
	if err != nil {
		return nil, err
	}
	seed, ok := sonosSeed(discovered)
	if !ok {
		return nil, fmt.Errorf("Sonos discovery returned no usable topology seed")
	}
	client := *sonosClient
	client.Target = seed.Address
	return &client, nil
}

func SonosPlayFromQueue(ctx context.Context, address string, index int) error {
	if net.ParseIP(address) == nil {
		return fmt.Errorf("Sonos target must be a speaker IP address")
	}
	if index < 0 {
		return fmt.Errorf("Sonos queue index must be zero or greater")
	}
	return sonosClient.PlayQueueIndex(ctx, address, index)
}

func SonosAppendSpotifyTracks(ctx context.Context, address string, uris []string) (sonos.SpotifyQueueRange, error) {
	return sonosClient.AppendSpotifyTracks(ctx, address, uris)
}

type SonosQueueRange = sonos.SpotifyQueueRange

func SonosStartSpotifyQueue(ctx context.Context, segment sonos.SpotifyQueueRange, position time.Duration) (bool, bool, error) {
	return sonosClient.StartSpotifyQueue(ctx, segment, position)
}

func SonosSetVolume(ctx context.Context, address string, value int) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}
	if net.ParseIP(address) == nil {
		return fmt.Errorf("Sonos target must be a speaker IP address")
	}
	return sonosClient.SetVolume(ctx, address, value)
}

func ReadSonosState(ctx context.Context, address string) (SonosState, error) {
	return readSonosState(ctx, address, true)
}

func ReadSonosTransportState(ctx context.Context, address string) (SonosState, error) {
	return readSonosState(ctx, address, false)
}

func readSonosState(ctx context.Context, address string, queue bool) (SonosState, error) {
	if net.ParseIP(address) == nil {
		return SonosState{}, fmt.Errorf("Sonos target must be a speaker IP address")
	}
	return sonosClient.ReadState(ctx, address, queue)
}

// SupplementSonosState reads Spotify metadata only for the same named speaker.
func (p *SpotifyProvider) SupplementSonosState(ctx context.Context, state *SonosState) error {
	resp, err := p.webAPIWithRetry(ctx, http.MethodGet, "/v1/me/player", nil, nil, "", http.StatusOK, http.StatusNoContent)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent {
		resp.Body.Close()
		return fmt.Errorf("Spotify has no current playback metadata")
	}
	var playing struct {
		Device struct {
			Name string `json:"name"`
		} `json:"device"`
		Progress *int `json:"progress_ms"`
		Item     struct {
			Name     string          `json:"name"`
			Artists  []spotifyArtist `json:"artists"`
			Duration int             `json:"duration_ms"`
			Album    struct {
				Name string `json:"name"`
			} `json:"album"`
		} `json:"item"`
	}
	if err := decodeBody(resp, &playing); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(playing.Device.Name), strings.TrimSpace(state.Coordinator)) || playing.Device.Name == "" {
		return fmt.Errorf("Spotify playback metadata does not match the Sonos coordinator")
	}
	if playing.Item.Name == "" {
		return fmt.Errorf("Spotify has no current track metadata")
	}
	state.Track = SonosTrack{Title: playing.Item.Name, Artist: artistNames(playing.Item.Artists), Album: playing.Item.Album.Name}
	if playing.Progress != nil {
		state.Position = sonosClockMilliseconds(*playing.Progress)
	}
	if playing.Item.Duration > 0 {
		state.Duration = sonosClockMilliseconds(playing.Item.Duration)
	}
	state.Source = "Spotify Connect input"
	return nil
}

func sonosClockMilliseconds(ms int) string {
	seconds := max(0, ms/1000)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}
