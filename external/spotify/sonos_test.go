package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/sonos"
)

const sonosTopologySOAP = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><GetZoneGroupStateResponse><ZoneGroupState>&lt;ZoneGroups&gt;&lt;ZoneGroup Coordinator="RINCON_SPEAKER" ID="RINCON_SPEAKER:0"&gt;&lt;ZoneGroupMember UUID="RINCON_SPEAKER" ZoneName="Room" Location="http://192.0.2.20:1400/xml/device_description.xml"/&gt;&lt;/ZoneGroup&gt;&lt;/ZoneGroups&gt;</ZoneGroupState></GetZoneGroupStateResponse></s:Body></s:Envelope>`

func TestSonosControlRejectsInvalidTarget(t *testing.T) {
	for _, tc := range []struct{ action, address string }{{"play", ""}, {"pause", "not-an-IP"}, {"unknown", "192.0.2.1"}} {
		if err := SonosControl(t.Context(), tc.action, tc.address, ""); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestSonosPlayFromQueueRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		address string
		index   int
	}{{"not-an-IP", 0}, {"192.0.2.1", -1}} {
		if err := SonosPlayFromQueue(t.Context(), tc.address, tc.index); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestSonosDiscoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := SonosDevices(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestSonosDiscoveryUsesSeedCacheWithoutSSDP(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sonos-seed.json")
	path := func() (string, error) { return cachePath, nil }
	var requests int
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sonosTopologySOAP))}, nil
	})}

	// A warm cache reads current topology from the seed and skips SSDP.
	if err := saveSonosSeed(cachePath, sonosDiscoverySeed{Address: "192.0.2.20", ID: "RINCON_SPEAKER"}); err != nil {
		t.Fatal(err)
	}
	devices, err := discoverSonos(t.Context(), sonos.NewClient(httpClient), "", path)
	if err != nil || len(devices) != 1 || requests != 1 {
		t.Fatalf("warm discovery devices=%+v requests=%d err=%v", devices, requests, err)
	}
}

func TestSonosGroupingUsesCachedSeedWithoutSSDP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_SONOS_TARGET", "")
	cachePath, err := sonosSeedCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveSonosSeed(cachePath, sonosDiscoverySeed{Address: "192.0.2.20", ID: "RINCON_SPEAKER"}); err != nil {
		t.Fatal(err)
	}
	oldClient := sonosClient
	defer func() { sonosClient = oldClient }()
	var requests int
	sonosClient = sonos.NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host != "192.0.2.20:1400" || req.URL.Path != "/ZoneGroupTopology/Control" {
			t.Errorf("topology request URL=%s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sonosTopologySOAP))}, nil
	})})
	oldDiscover := discoverSonosSSDP
	defer func() { discoverSonosSSDP = oldDiscover }()
	discoverSonosSSDP = func(context.Context, *sonos.Client) ([]sonos.Device, error) {
		t.Fatal("grouping bypassed the cached seed and tried SSDP")
		return nil, nil
	}
	if err := SonosSetGroup(t.Context(), []string{"RINCON_SPEAKER"}, "RINCON_SPEAKER"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || sonosClient.Target != "" {
		t.Fatalf("topology requests=%d shared target=%q", requests, sonosClient.Target)
	}
}

func TestSonosDiscoveryColdStartCachesDiscoveredSeed(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sonos-seed.json")
	oldDiscover := discoverSonosSSDP
	defer func() { discoverSonosSSDP = oldDiscover }()
	discoverSonosSSDP = func(_ context.Context, client *sonos.Client) ([]sonos.Device, error) {
		if client.Target != "" {
			t.Fatalf("unexpected target %q", client.Target)
		}
		return []sonos.Device{{ID: "RINCON_SPEAKER", Address: "192.0.2.20"}}, nil
	}
	devices, err := discoverSonos(t.Context(), sonos.NewClient(nil), "", func() (string, error) { return cachePath, nil })
	if err != nil || len(devices) != 1 {
		t.Fatalf("discovery devices=%+v err=%v", devices, err)
	}
	seed, err := loadSonosSeed(cachePath)
	if err != nil || seed != (sonosDiscoverySeed{Address: "192.0.2.20", ID: "RINCON_SPEAKER"}) {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
}

func TestSonosDiscoveryCacheFallbackAndExplicitTarget(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sonos-seed.json")
	if err := saveSonosSeed(cachePath, sonosDiscoverySeed{Address: "192.0.2.20", ID: "RINCON_STALE"}); err != nil {
		t.Fatal(err)
	}
	var seedUsed string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seedUsed = req.URL.Hostname()
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unreachable"))}, nil
	})}
	oldDiscover := discoverSonosSSDP
	defer func() { discoverSonosSSDP = oldDiscover }()
	discoverSonosSSDP = func(_ context.Context, c *sonos.Client) ([]sonos.Device, error) {
		seedUsed = c.Target
		return []sonos.Device{{ID: "RINCON_NEW", Address: "192.0.2.21"}}, nil
	}
	_, err := discoverSonos(t.Context(), sonos.NewClient(client), "", func() (string, error) { return cachePath, nil })
	if err != nil || seedUsed != "" {
		t.Fatalf("rediscovery seed=%q err=%v", seedUsed, err)
	}
	seed, err := loadSonosSeed(cachePath)
	if err != nil || seed.ID != "RINCON_NEW" || seed.Address != "192.0.2.21" {
		t.Fatalf("refreshed seed=%+v err=%v", seed, err)
	}

	sonosClient := sonos.NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seedUsed = req.URL.Hostname()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(sonosTopologySOAP, "192.0.2.20", "192.0.2.30")))}, nil
	})})
	_, err = discoverSonos(t.Context(), sonosClient, "192.0.2.30", func() (string, error) { return cachePath, nil })
	if err != nil || seedUsed != "192.0.2.30" || sonosClient.Target != "" {
		t.Fatalf("explicit target seed=%q client target=%q err=%v", seedUsed, sonosClient.Target, err)
	}
	seed, err = loadSonosSeed(cachePath)
	if err != nil || seed != (sonosDiscoverySeed{Address: "192.0.2.30", ID: "RINCON_SPEAKER"}) {
		t.Fatalf("explicit target cache seed=%+v err=%v", seed, err)
	}
}

func TestSonosSeedCacheIgnoresInvalidAndUnwritableFiles(t *testing.T) {
	for _, contents := range []string{"{", `{"address":"bad","id":"x"}`, `{"address":"192.0.2.1"}`} {
		path := filepath.Join(t.TempDir(), "seed.json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSonosSeed(path); err == nil {
			t.Fatalf("accepted invalid seed %q", contents)
		}
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveSonosSeed(filepath.Join(blocked, "seed.json"), sonosDiscoverySeed{Address: "192.0.2.1", ID: "RINCON_X"}); err == nil {
		t.Fatal("saved seed beneath a file path")
	}
	oldDiscover := discoverSonosSSDP
	defer func() { discoverSonosSSDP = oldDiscover }()
	called := false
	discoverSonosSSDP = func(context.Context, *sonos.Client) ([]sonos.Device, error) {
		called = true
		return []sonos.Device{{ID: "RINCON_X", Address: "192.0.2.1"}}, nil
	}
	discovered, err := discoverSonos(t.Context(), sonos.NewClient(nil), "", func() (string, error) { return filepath.Join(blocked, "seed.json"), nil })
	if err != nil || !called || len(discovered) != 1 {
		t.Fatalf("cache write failure blocked discovery: devices=%+v called=%v err=%v", discovered, called, err)
	}
}

func TestSonosCachedSeedHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := saveSonosSeed(path, sonosDiscoverySeed{Address: "192.0.2.20", ID: "RINCON_SPEAKER"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := discoverSonos(ctx, sonos.NewClient(nil), "", func() (string, error) { return path, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestSonosTopologyMapsUUIDsToOutputAddresses(t *testing.T) {
	devices := sonosOutputDevices([]sonos.Device{
		{ID: "room-uuid", Name: "Office", Address: "192.0.2.10", Coordinator: "room-uuid", Members: []string{"room-uuid", "kitchen-uuid"}, Bonded: []string{"sub-uuid"}},
		{ID: "kitchen-uuid", Name: "Kitchen", Address: "192.0.2.11", Coordinator: "room-uuid", Members: []string{"room-uuid", "kitchen-uuid"}},
	})
	if len(devices) != 2 || devices[0].ID != "192.0.2.10" || devices[0].RoomID != "room-uuid" || devices[0].Coordinator != "192.0.2.10" || !devices[0].Bonded || len(devices[0].MemberIDs) != 2 || devices[0].MemberIDs[1] != "kitchen-uuid" {
		t.Fatalf("topology devices=%+v", devices)
	}
	if devices[0].Members[0] != "192.0.2.10" || devices[0].Members[1] != "192.0.2.11" || devices[1].ID != "192.0.2.11" || devices[1].Bonded {
		t.Fatalf("topology member mapping=%+v", devices)
	}
}

func TestSonosSpotifyMetadataMatchesCoordinator(t *testing.T) {
	for _, name := range []string{"Office", "Other speaker"} {
		state := SonosState{Coordinator: "Office", Transport: "PAUSED_PLAYBACK", Source: "Sonos virtual input"}
		requests := 0
		client := New(stubSession(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.Method != "GET" || req.URL.Path != "/v1/me/player" {
				t.Errorf("unexpected request %s %s", req.Method, req.URL)
			}
			body := `{"device":{"name":"` + name + `","id":null,"is_restricted":true},"progress_ms":83000,"item":{"name":"Actual song","artists":[{"name":"Actual artist"}],"album":{"name":"Actual album"},"duration_ms":296000,"uri":"https://media.example?secret=signed"}}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})), "client", 320)
		err := client.SupplementSonosState(t.Context(), &state)
		if name == "Office" {
			if err != nil || state.Track.Title != "Actual song" || state.Position != "00:01:23" || state.Duration != "00:04:56" || state.Transport != "PAUSED_PLAYBACK" {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		} else if err == nil || state.Track.Title != "" {
			t.Fatal("accepted another speaker's metadata")
		}
		data, _ := json.Marshal(state)
		if strings.Contains(string(data), "signed") || requests != 1 {
			t.Fatal("leaked source URI or sent extra requests")
		}
	}
}
