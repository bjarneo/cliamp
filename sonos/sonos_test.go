package sonos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSortedUniqueUsesIDToBreakNameTies(t *testing.T) {
	devices := []Device{
		{ID: "z-empty"},
		{ID: "alpha-b", Name: "ALPHA"},
		{ID: "beta", Name: "Beta"},
		{ID: "alpha-a", Name: "aLpHa"},
		{ID: "a-empty"},
		{ID: "alpha-b", Name: "ALPHA"},
	}
	want := []Device{
		{ID: "a-empty"},
		{ID: "z-empty"},
		{ID: "alpha-a", Name: "aLpHa"},
		{ID: "alpha-b", Name: "ALPHA"},
		{ID: "beta", Name: "Beta"},
	}

	for _, input := range [][]Device{
		devices,
		{devices[5], devices[4], devices[3], devices[2], devices[1], devices[0]},
	} {
		if got := sortedUnique(input); !reflect.DeepEqual(got, want) {
			t.Errorf("sortedUnique() = %+v, want %+v", got, want)
		}
	}
}

func httptestClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := server.Client().Transport
	return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return base.RoundTrip(clone)
	})}
}
func soap(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` + body + `</s:Body></s:Envelope>`))}
}
func TestSOAPEscapesValuesAndParsesResponse(t *testing.T) {
	c := NewClient(httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "a&amp;b&lt;c&gt;") {
			t.Errorf("body not escaped: %s", b)
		}
		if r.Header.Get("SOAPACTION") != `"`+avTransport+`#SetAVTransportURI"` {
			t.Errorf("SOAPACTION=%q", r.Header.Get("SOAPACTION"))
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:SetAVTransportURIResponse><Value>ok&amp;yes</Value></u:SetAVTransportURIResponse></s:Body></s:Envelope>`)
	})))
	got, err := c.action(t.Context(), "192.0.2.1", avTransportPath, avTransport, "SetAVTransportURI", map[string]string{"InstanceID": "0", "CurrentURI": "a&b<c>", "CurrentURIMetaData": ""})
	if err != nil || got["Value"] != "ok&yes" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestSOAPURLSupportsIPv4AndIPv6Addresses(t *testing.T) {
	for _, address := range []string{"192.0.2.1", "2001:db8::1"} {
		t.Run(address, func(t *testing.T) {
			var host, hostname, port, path string
			client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				host, hostname, port, path = r.URL.Host, r.URL.Hostname(), r.URL.Port(), r.URL.Path
				return soap(`<GetTransportInfoResponse/>`, 200), nil
			})})
			if err := validateAddress(address); err != nil {
				t.Fatal(err)
			}
			if _, err := client.action(t.Context(), address, avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"}); err != nil {
				t.Fatal(err)
			}
			if host != net.JoinHostPort(address, "1400") || hostname != address || port != "1400" || path != avTransportPath {
				t.Fatalf("request URL host=%q hostname=%q port=%q path=%q", host, hostname, port, path)
			}
		})
	}
}

func TestSOAPFaultAndCancellation(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return soap(`<s:Fault><detail><UPnPError><errorCode>701</errorCode><errorDescription>Transition unavailable</errorDescription></UPnPError></detail></s:Fault>`, 500), nil
	})})
	_, e := c.action(t.Context(), "192.0.2.1", avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"})
	if e == nil || !strings.Contains(e.Error(), "701") {
		t.Fatalf("fault=%v", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request made after cancellation")
		return nil, nil
	})}
	_, e = c.action(ctx, "192.0.2.1", avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel err=%v", e)
	}
}

func TestSOAPTimeoutAndUnboundedContext(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); !ok {
			return soap(`<GetTransportInfoResponse/>`, 200), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	client.Timeout = 25 * time.Millisecond
	_, err := client.action(t.Context(), "192.0.2.1", avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}

	client.Timeout = 0
	if _, err := client.action(t.Context(), "192.0.2.1", avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"}); err != nil {
		t.Fatalf("action without SOAP timeout: %v", err)
	}
}

func TestSOAPTimeoutRespectsEarlierContextDeadline(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, hasDeadline = r.Context().Deadline()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	client.Timeout = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	_, err := client.action(ctx, "192.0.2.1", avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
	if !errors.Is(err, context.DeadlineExceeded) || !hasDeadline || !deadline.Equal(wantDeadline) {
		t.Fatalf("earlier context deadline=%v, request deadline=%v, err=%v", wantDeadline, deadline, err)
	}
}

func TestSOAPTimeoutRespectsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	client.Timeout = time.Second
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.action(ctx, "192.0.2.1", avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestDiscoveryReadTimeoutReturnsContextCancellation(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, done, err := readDiscoveryPacket(ctx, conn, make([]byte, 64))
	if done || !errors.Is(err, context.Canceled) {
		t.Fatalf("done=%t err=%v", done, err)
	}
}

func TestDiscoveryReadTimeoutEndsNormally(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, done, err := readDiscoveryPacket(t.Context(), conn, make([]byte, 64))
	if !done || err != nil {
		t.Fatalf("done=%t err=%v", done, err)
	}
}

func TestSpotifyShareAndQueueIndex(t *testing.T) {
	var actions []string
	var sought string
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		a := r.Header.Get("SOAPACTION")
		actions = append(actions, a)
		b, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(a, "#AddURIToQueue"):
			if !strings.Contains(string(b), "spotify%3aplaylist%3a37i9dQZF1E38cclX2Ha8AV") || !strings.Contains(string(b), "SA_RINCON2311_X_#Svc2311-0-Token") {
				t.Fatalf("share DIDL: %s", b)
			}
			return soap(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>7</FirstTrackNumberEnqueued></AddURIToQueueResponse>`, 200), nil
		case strings.Contains(a, "#Seek"):
			if !strings.Contains(string(b), "<Target>7</Target>") {
				t.Fatalf("wire index should be one based: %s", b)
			}
			sought = "7"
			return soap(`<SeekResponse/>`, 200), nil
		default:
			return soap(`<ok/>`, 200), nil
		}
	})})
	if e := c.playShare(t.Context(), "192.0.2.1", "RINCON_TEST", "spotify:playlist:37i9dQZF1E38cclX2Ha8AV"); e != nil {
		t.Fatal(e)
	}
	if sought != "7" || len(actions) != 4 || !strings.Contains(actions[1], "#SetAVTransportURI") || !strings.Contains(actions[3], "#Play") {
		t.Fatalf("actions=%v", actions)
	}
}
func TestSpotifyShareRejectsMalformed(t *testing.T) {
	for _, uri := range []string{"", "spotify:playlist:bad-id!", "spotify:unknown:abc", "https://invalid"} {
		if _, e := spotifyShare(uri); e == nil {
			t.Errorf("accepted %q", uri)
		}
	}
}

func TestAppendSpotifyTracksPreservesOrderWithoutChangingTransport(t *testing.T) {
	var actions, uris, bodies []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		action := r.Header.Get("SOAPACTION")
		actions = append(actions, action)
		if strings.Contains(action, "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(action, "#AddURIToQueue") {
			bodies = append(bodies, string(body))
			start := strings.Index(string(body), "<EnqueuedURI>") + len("<EnqueuedURI>")
			end := strings.Index(string(body), "</EnqueuedURI>")
			uris = append(uris, string(body)[start:end])
			position := 6 + len(uris)
			length := 6 + len(uris)
			return soap(fmt.Sprintf(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>%d</FirstTrackNumberEnqueued><NumTracksAdded>1</NumTracksAdded><NewQueueLength>%d</NewQueueLength></AddURIToQueueResponse>`, position, length), 200), nil
		}
		t.Fatalf("append changed transport with %s", action)
		return nil, nil
	})})
	got, err := client.AppendSpotifyTracks(t.Context(), "192.0.2.12", []string{
		"spotify:track:53o05J0uSWOedPwN4Z0oyo", "spotify:track:53o05J0uSWOedPwN4Z0oy1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "192.0.2.10" || got.CoordinatorID != "RINCON_ROOM" || got.FirstTrackNumber != 7 || got.Appended != 2 {
		t.Fatalf("range = %+v", got)
	}
	if len(uris) != 2 || !strings.Contains(uris[0], "53o05J0uSWOedPwN4Z0oyo") || !strings.Contains(uris[1], "53o05J0uSWOedPwN4Z0oy1") || len(actions) != 3 {
		t.Fatalf("actions=%v uris=%v", actions, uris)
	}
	want := []string{"InstanceID", "EnqueuedURI", "EnqueuedURIMetaData", "DesiredFirstTrackNumberEnqueued", "EnqueueAsNext"}
	for _, body := range bodies {
		last := -1
		for _, name := range want {
			index := strings.Index(body, "<"+name+">")
			if index <= last {
				t.Fatalf("AddURIToQueue arguments out of order: %s", body)
			}
			last = index
		}
	}
}

func TestAppendSpotifyTracksRejectsContainersBeforeQueueMutation(t *testing.T) {
	requests := 0
	client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		requests++
		return topologyResponse(), nil
	})})
	for _, uri := range []string{"spotify:playlist:abc123", "spotify:album:abc123", "spotify:show:abc123"} {
		_, err := client.AppendSpotifyTracks(t.Context(), "192.0.2.10", []string{"spotify:track:abc123", uri})
		if err == nil || !strings.Contains(err.Error(), "tracks or episodes") {
			t.Errorf("AppendSpotifyTracks(%q) error = %v", uri, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid input caused %d Sonos requests", requests)
	}
}

func TestAppendSpotifyTracksAcceptsTrackAndEpisode(t *testing.T) {
	adds := 0
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		if strings.Contains(r.Header.Get("SOAPACTION"), "#AddURIToQueue") {
			adds++
			return soap(fmt.Sprintf(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>%d</FirstTrackNumberEnqueued><NumTracksAdded>1</NumTracksAdded><NewQueueLength>%d</NewQueueLength></AddURIToQueueResponse>`, adds, adds), 200), nil
		}
		t.Fatalf("unexpected action %s", r.Header.Get("SOAPACTION"))
		return nil, nil
	})})
	got, err := client.AppendSpotifyTracks(t.Context(), "192.0.2.10", []string{"spotify:track:abc123", "spotify:episode:def456"})
	if err != nil || adds != 2 || got.Appended != 2 {
		t.Fatalf("range=%+v adds=%d err=%v", got, adds, err)
	}
}

func TestAppendSpotifyTracksReportsPartialQueueOnFailure(t *testing.T) {
	adds := 0
	transportChanged := false
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		action := r.Header.Get("SOAPACTION")
		if strings.Contains(action, "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		if strings.Contains(action, "#AddURIToQueue") {
			adds++
			if adds == 2 {
				return nil, errors.New("queue append failed")
			}
			return soap(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>4</FirstTrackNumberEnqueued><NumTracksAdded>1</NumTracksAdded><NewQueueLength>4</NewQueueLength></AddURIToQueueResponse>`, 200), nil
		}
		transportChanged = true
		return soap(`<ok/>`, 200), nil
	})})
	result, err := client.AppendSpotifyTracks(t.Context(), "192.0.2.10", []string{
		"spotify:track:2FY7b99s15jUprqC0M5NCT", "spotify:track:53o05J0uSWOedPwN4Z0oyo",
	})
	if err == nil || result.Appended != 1 || adds != 2 || transportChanged {
		t.Fatalf("range=%+v err=%v adds=%d transportChanged=%t", result, err, adds, transportChanged)
	}
}

func TestAppendSpotifyTracksStillDetectsConcurrentQueueChange(t *testing.T) {
	adds := 0
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState"):
			return topologyResponse(), nil
		case strings.Contains(r.Header.Get("SOAPACTION"), "#AddURIToQueue"):
			adds++
			position := 5 + adds
			if adds == 2 {
				position++ // Another controller inserted a track between our appends.
			}
			return soap(fmt.Sprintf(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>%d</FirstTrackNumberEnqueued><NumTracksAdded>1</NumTracksAdded><NewQueueLength>8</NewQueueLength></AddURIToQueueResponse>`, position), 200), nil
		default:
			t.Fatalf("unexpected action %s", r.Header.Get("SOAPACTION"))
			return nil, nil
		}
	})})
	result, err := client.AppendSpotifyTracks(t.Context(), "192.0.2.10", []string{
		"spotify:track:2FY7b99s15jUprqC0M5NCT", "spotify:track:53o05J0uSWOedPwN4Z0oyo",
	})
	if err == nil || result.Appended != 2 || adds != 2 || !strings.Contains(err.Error(), `expected item 7, got 8; NumTracksAdded "1", NewQueueLength "8"`) {
		t.Fatalf("range=%+v err=%v adds=%d", result, err, adds)
	}
}

func TestStartSpotifyQueueSeeksTrackThenPlaysAndRestoresTime(t *testing.T) {
	var actions, bodies []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		actions = append(actions, r.Header.Get("SOAPACTION"))
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		return soap(`<ok/>`, 200), nil
	})})
	started, restored, err := client.StartSpotifyQueue(t.Context(), SpotifyQueueRange{
		Address: "192.0.2.10", CoordinatorID: "RINCON_ROOM", FirstTrackNumber: 7, Appended: 2,
	}, 83*time.Second)
	if err != nil || !started || !restored {
		t.Fatalf("started=%t restored=%t err=%v", started, restored, err)
	}
	want := []string{"#SetAVTransportURI", "#Seek", "#Play", "#Seek"}
	for i, suffix := range want {
		if !strings.Contains(actions[i], suffix) {
			t.Fatalf("action %d = %q, want %s", i, actions[i], suffix)
		}
	}
	if !strings.Contains(bodies[0], "x-rincon-queue:RINCON_ROOM#0") || !strings.Contains(bodies[1], "<Target>7</Target>") || !strings.Contains(bodies[3], "<Unit>REL_TIME</Unit>") || !strings.Contains(bodies[3], "<Target>00:01:23</Target>") {
		t.Fatalf("start bodies = %v", bodies)
	}
}

func TestStartSpotifyQueueReportsPositionSeekFailureAfterPlay(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		if strings.Contains(r.Header.Get("SOAPACTION"), "#Seek") {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "REL_TIME") {
				return soap(`<s:Fault><detail><UPnPError><errorCode>701</errorCode><errorDescription>Seek unavailable</errorDescription></UPnPError></detail></s:Fault>`, 500), nil
			}
		}
		return soap(`<ok/>`, 200), nil
	})})
	started, restored, err := client.StartSpotifyQueue(t.Context(), SpotifyQueueRange{
		Address: "192.0.2.10", CoordinatorID: "RINCON_ROOM", FirstTrackNumber: 1, Appended: 1,
	}, 10*time.Second)
	if !started || restored || err == nil || !strings.Contains(err.Error(), "Seek unavailable") {
		t.Fatalf("started=%t restored=%t err=%v", started, restored, err)
	}
}

func TestStartSpotifyQueueDoesNotStartAfterCoordinatorChanges(t *testing.T) {
	var transportActions int
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			changed := strings.ReplaceAll(zones, `Coordinator="RINCON_ROOM"`, `Coordinator="RINCON_KITCHEN"`)
			changed = strings.ReplaceAll(changed, `RINCON_ROOM:0`, `RINCON_KITCHEN:0`)
			return soap(`<GetZoneGroupStateResponse><ZoneGroupState>`+xmlEscape(changed)+`</ZoneGroupState></GetZoneGroupStateResponse>`, 200), nil
		}
		transportActions++
		return soap(`<ok/>`, 200), nil
	})})
	started, restored, err := client.StartSpotifyQueue(t.Context(), SpotifyQueueRange{
		Address: "192.0.2.10", CoordinatorID: "RINCON_ROOM", FirstTrackNumber: 7, Appended: 2,
	}, 10*time.Second)
	if err == nil || started || restored || transportActions != 0 || !strings.Contains(err.Error(), "coordinator changed") {
		t.Fatalf("started=%t restored=%t transportActions=%d err=%v", started, restored, transportActions, err)
	}
}

func TestPlayQueueIndexUsesCoordinatorAndOneBasedWireIndex(t *testing.T) {
	var target string
	var seekPath, playPath string
	var seekBody string
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		target = r.URL.Host
		action := r.Header.Get("SOAPACTION")
		switch {
		case strings.Contains(action, "#GetZoneGroupState"):
			if r.URL.Path != "/ZoneGroupTopology/Control" {
				t.Fatalf("topology path=%q", r.URL.Path)
			}
			return topologyResponse(), nil
		case strings.Contains(action, "#Seek"):
			seekPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			seekBody = string(body)
		case strings.Contains(action, "#Play"):
			playPath = r.URL.Path
		default:
			t.Fatalf("unexpected action %q", action)
		}
		return soap(`<ok/>`, 200), nil
	})
	client := NewClient(&http.Client{Transport: tr})
	if err := client.PlayQueueIndex(t.Context(), "192.0.2.12", 0); err != nil {
		t.Fatal(err)
	}
	if target != "192.0.2.10:1400" || seekPath != avTransportPath || playPath != avTransportPath || !strings.Contains(seekBody, "<Unit>TRACK_NR</Unit>") || !strings.Contains(seekBody, "<Target>1</Target>") {
		t.Fatalf("target=%q seekPath=%q playPath=%q seek=%s", target, seekPath, playPath, seekBody)
	}
}

func TestPreviousSeeksWithinKnownSonosQueue(t *testing.T) {
	for _, tt := range []struct {
		name, uri, track, relTime, transport string
		seekUnit, seekTarget                 string
		wantSeek, wantPlay, wantPrevious     bool
		seekFails                            bool
	}{
		{name: "playing queue", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:00", transport: "PLAYING", seekUnit: "TRACK_NR", seekTarget: "187", wantSeek: true, wantPlay: true},
		{name: "transitioning queue", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:00", transport: "TRANSITIONING", seekUnit: "TRACK_NR", seekTarget: "187", wantSeek: true, wantPlay: true},
		{name: "paused queue", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:00", transport: "PAUSED_PLAYBACK", seekUnit: "TRACK_NR", seekTarget: "187", wantSeek: true},
		{name: "exact threshold goes back", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:03", transport: "PLAYING", seekUnit: "TRACK_NR", seekTarget: "187", wantSeek: true, wantPlay: true},
		{name: "failed seek is not retried", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:00", transport: "PLAYING", seekUnit: "TRACK_NR", seekTarget: "187", wantSeek: true, seekFails: true},
		{name: "first queue item restarts", uri: "x-rincon-queue:RINCON_ROOM#0", track: "1", relTime: "00:00:00", transport: "PLAYING", seekUnit: "REL_TIME", seekTarget: "00:00:00", wantSeek: true, wantPlay: true},
		{name: "restart current item", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:04", transport: "PLAYING", seekUnit: "REL_TIME", seekTarget: "00:00:00", wantSeek: true, wantPlay: true},
		{name: "fractional time past restart threshold", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:03.500", transport: "PLAYING", seekUnit: "REL_TIME", seekTarget: "00:00:00", wantSeek: true, wantPlay: true},
		{name: "paused restart current item", uri: "x-rincon-queue:RINCON_ROOM#0", track: "188", relTime: "00:00:04", transport: "PAUSED_PLAYBACK", seekUnit: "REL_TIME", seekTarget: "00:00:00", wantSeek: true},
		{name: "virtual input", uri: "x-sonos-vli:input", track: "188", relTime: "00:00:00", transport: "PLAYING", wantPrevious: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var actions []string
			c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				action := r.Header.Get("SOAPACTION")
				actions = append(actions, action)
				switch {
				case strings.Contains(action, "#GetZoneGroupState"):
					return topologyResponse(), nil
				case strings.Contains(action, "#GetMediaInfo"):
					return soap(`<GetMediaInfoResponse><CurrentURI>`+tt.uri+`</CurrentURI></GetMediaInfoResponse>`, 200), nil
				case strings.Contains(action, "#GetPositionInfo"):
					return soap(`<GetPositionInfoResponse><Track>`+tt.track+`</Track><RelTime>`+tt.relTime+`</RelTime></GetPositionInfoResponse>`, 200), nil
				case strings.Contains(action, "#GetTransportInfo"):
					return soap(`<GetTransportInfoResponse><CurrentTransportState>`+tt.transport+`</CurrentTransportState></GetTransportInfoResponse>`, 200), nil
				case strings.Contains(action, "#Seek"):
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), `<Unit>`+tt.seekUnit+`</Unit><Target>`+tt.seekTarget+`</Target>`) {
						t.Fatalf("seek body=%s", body)
					}
					if tt.seekFails {
						return soap(`<s:Fault><detail><UPnPError><errorCode>701</errorCode><errorDescription>Transition unavailable</errorDescription></UPnPError></detail></s:Fault>`, 500), nil
					}
					return soap(`<SeekResponse/>`, 200), nil
				default:
					return soap(`<ok/>`, 200), nil
				}
			})})
			err := c.Control(t.Context(), "192.0.2.12", "previous", "")
			if (err != nil) != tt.seekFails {
				t.Fatalf("Control error=%v, want failure=%v", err, tt.seekFails)
			}
			joined := strings.Join(actions, ",")
			if strings.Contains(joined, "#Browse") {
				t.Fatalf("previous fetched the queue: %v", actions)
			}
			if got := strings.Contains(joined, "#Seek"); got != tt.wantSeek {
				t.Fatalf("seek=%v actions=%v", got, actions)
			}
			if got := strings.Contains(joined, "#Play"); got != tt.wantPlay {
				t.Fatalf("play=%v actions=%v", got, actions)
			}
			if got := strings.Contains(joined, "#Previous"); got != tt.wantPrevious {
				t.Fatalf("native previous=%v actions=%v", got, actions)
			}
		})
	}
}

func TestSetVolumeUsesGroupRenderingControl(t *testing.T) {
	var action, requestPath, body string
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		action = r.Header.Get("SOAPACTION")
		requestPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		return soap(`<ok/>`, 200), nil
	})
	client := NewClient(&http.Client{Transport: tr})
	if err := client.SetVolume(t.Context(), "192.0.2.12", 44); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(action, "GroupRenderingControl:1#SetGroupVolume") || requestPath != "/MediaRenderer/GroupRenderingControl/Control" || !strings.Contains(body, "<DesiredVolume>44</DesiredVolume>") {
		t.Fatalf("action=%q path=%q body=%s", action, requestPath, body)
	}
}
func TestQueueIndexClockAndMetadata(t *testing.T) {
	if clock("NOT_IMPLEMENTED") != "" || clock("1:2") != "" || clock("00:01:23") != "00:01:23" {
		t.Fatal("clock normalization mismatch")
	}
	items := parseDIDL(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>A</dc:title><dc:creator>B</dc:creator><upnp:album>C</upnp:album><res>secret</res></item></DIDL-Lite>`)
	if len(items) != 1 || items[0] != (Track{Title: "A", Artist: "B", Album: "C"}) {
		t.Fatalf("items=%+v", items)
	}
}

const zones = `<ZoneGroups><ZoneGroup Coordinator="RINCON_ROOM" ID="RINCON_ROOM:0"><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/xml/device_description.xml"><Satellite UUID="RINCON_BASS" ZoneName="Sub" Location="http://192.0.2.11:1400/xml/device_description.xml"/></ZoneGroupMember><ZoneGroupMember UUID="RINCON_KITCHEN" ZoneName="Kitchen" Location="http://192.0.2.12:1400/xml/device_description.xml"/></ZoneGroup></ZoneGroups>`

func topologyResponse() *http.Response {
	return topologyResponseFor(zones)
}

func topologyResponseFor(topology string) *http.Response {
	return soap(`<GetZoneGroupStateResponse><ZoneGroupState>`+xmlEscape(topology)+`</ZoneGroupState></GetZoneGroupStateResponse>`, 200)
}

func TestCoordinatorSelectsRequestedGroupIncludingBondedMembers(t *testing.T) {
	topology := `<ZoneGroups><ZoneGroup Coordinator="RINCON_PLAY3" ID="g1"><ZoneGroupMember UUID="RINCON_PLAY3" ZoneName="Play3" Location="http://192.0.2.10:1400/xml/device_description.xml"><Satellite UUID="RINCON_SUB" ZoneName="Sub" Location="http://192.0.2.11:1400/xml/device_description.xml"/></ZoneGroupMember><ZoneGroupMember UUID="RINCON_KITCHEN" ZoneName="Kitchen" Location="http://192.0.2.12:1400/xml/device_description.xml"/></ZoneGroup><ZoneGroup Coordinator="RINCON_ROAM" ID="g2"><ZoneGroupMember UUID="RINCON_ROAM" ZoneName="Roam" Location="http://192.0.2.20:1400/xml/device_description.xml"/></ZoneGroup><ZoneGroup Coordinator="RINCON_BEAM" ID="g3"><ZoneGroupMember UUID="RINCON_BEAM" ZoneName="Beam" Location="http://192.0.2.23:1400/xml/device_description.xml"/></ZoneGroup></ZoneGroups>`
	c := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return soap(`<GetZoneGroupStateResponse><ZoneGroupState>`+xmlEscape(topology)+`</ZoneGroupState></GetZoneGroupStateResponse>`, 200), nil
	})})
	for _, test := range []struct{ address, coordinator, coordinatorAddress string }{
		{"192.0.2.12", "RINCON_PLAY3", "192.0.2.10"},
		{"192.0.2.11", "RINCON_PLAY3", "192.0.2.10"},
		{"192.0.2.20", "RINCON_ROAM", "192.0.2.20"},
		{"192.0.2.23", "RINCON_BEAM", "192.0.2.23"},
	} {
		t.Run(test.address, func(t *testing.T) {
			got, err := c.coordinator(t.Context(), test.address)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != test.coordinator || got.Address != test.coordinatorAddress {
				t.Fatalf("coordinator=%+v, want id=%q address=%q", got, test.coordinator, test.coordinatorAddress)
			}
		})
	}
}

func TestCoordinatorRejectsUnknownOrInvalidTopologyReferences(t *testing.T) {
	for _, test := range []struct{ name, topology, address, want string }{
		{"unknown address", zones, "192.0.2.99", "not in household topology"},
		{"missing coordinator", `<ZoneGroups><ZoneGroup Coordinator="RINCON_MISSING"><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/device.xml"/></ZoneGroup></ZoneGroups>`, "192.0.2.10", `coordinator "RINCON_MISSING"`},
		{"empty coordinator address", `<ZoneGroups><ZoneGroup Coordinator="RINCON_COORD"><ZoneGroupMember UUID="RINCON_COORD" ZoneName="Coordinator"/><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/device.xml"/></ZoneGroup></ZoneGroups>`, "192.0.2.10", "no address"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return soap(`<GetZoneGroupStateResponse><ZoneGroupState>`+xmlEscape(test.topology)+`</ZoneGroupState></GetZoneGroupStateResponse>`, 200), nil
			})})
			_, err := client.coordinator(t.Context(), test.address)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHouseholdParsesCoordinatorMembersAndBondedSatellite(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return topologyResponse(), nil })})
	ds, e := c.Household(t.Context(), "192.0.2.10")
	if e != nil {
		t.Fatal(e)
	}
	if len(ds) != 3 || ds[0].Coordinator != "RINCON_ROOM" {
		t.Fatalf("devices=%+v", ds)
	}
	var sub Device
	for _, d := range ds {
		if d.ID == "RINCON_BASS" {
			sub = d
		}
	}
	if sub.Address != "192.0.2.11" || !contains(sub.Bonded, sub.ID) {
		t.Fatalf("satellite=%+v", sub)
	}
}

func TestHouseholdAndSetGroupIgnoreInvisibleMembers(t *testing.T) {
	topology := `<ZoneGroups><ZoneGroup Coordinator="RINCON_ROOM" ID="g"><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/device.xml"><Satellite UUID="RINCON_SUB" ZoneName="Sub" Location="http://192.0.2.11:1400/device.xml"/></ZoneGroupMember><ZoneGroupMember UUID="RINCON_KITCHEN" ZoneName="Kitchen" Location="http://192.0.2.12:1400/device.xml"/><ZoneGroupMember UUID="RINCON_HIDDEN" ZoneName="Hidden" Location="http://192.0.2.13:1400/device.xml" Invisible="1"/></ZoneGroup></ZoneGroups>`
	var actions []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		actions = append(actions, r.Header.Get("SOAPACTION"))
		return soap(`<GetZoneGroupStateResponse><ZoneGroupState>`+xmlEscape(topology)+`</ZoneGroupState></GetZoneGroupStateResponse>`, 200), nil
	})})
	client.Target = "192.0.2.10"
	devices, err := client.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || !reflect.DeepEqual(devices[0].Members, []string{"RINCON_ROOM", "RINCON_KITCHEN"}) {
		t.Fatalf("devices=%+v", devices)
	}
	if err := client.SetGroup(t.Context(), []string{"RINCON_ROOM", "RINCON_KITCHEN"}, "RINCON_ROOM"); err != nil {
		t.Fatalf("visible group rejected because of invisible member: %v", err)
	}
	var room Device
	for _, d := range devices {
		if d.ID == "RINCON_ROOM" {
			room = d
		}
	}
	if !contains(room.Bonded, "RINCON_SUB") || len(actions) != 2 {
		t.Fatalf("bonded safety or request count lost: devices=%+v actions=%v", devices, actions)
	}
}

func TestSetGroupUsesExactCheckedRoomsAcrossSourceGroups(t *testing.T) {
	devices := []Device{
		{ID: "a", Address: "192.0.2.1", Name: "Alpha", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "b", Address: "192.0.2.2", Name: "Bravo", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "c", Address: "192.0.2.3", Name: "Charlie", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "d", Address: "192.0.2.4", Name: "Delta", Coordinator: "d", GroupID: "g2", Members: []string{"d", "e"}},
		{ID: "e", Address: "192.0.2.5", Name: "Echo", Coordinator: "d", GroupID: "g2", Members: []string{"d", "e"}},
		{ID: "f", Address: "192.0.2.6", Name: "Foxtrot", Coordinator: "f", GroupID: "g3", Members: []string{"f"}},
	}
	var calls []string
	join := func(_ context.Context, room, playback string) error {
		calls = append(calls, "join:"+room+":"+playback)
		return nil
	}
	unjoin := func(_ context.Context, room string) error {
		calls = append(calls, "unjoin:"+room)
		return nil
	}
	err := setGroup(t.Context(), devices, []string{"a", "c", "d"}, "a", join, unjoin)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unjoin:192.0.2.1", "unjoin:192.0.2.3", "unjoin:192.0.2.4", "join:192.0.2.3:192.0.2.1", "join:192.0.2.4:192.0.2.1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSetGroupUnchecksRoomAndExtractsSoloWithoutMovingOthers(t *testing.T) {
	devices := []Device{
		{ID: "a", Address: "192.0.2.1", Coordinator: "a", GroupID: "g", Members: []string{"a", "b", "c"}},
		{ID: "b", Address: "192.0.2.2", Coordinator: "a", GroupID: "g", Members: []string{"a", "b", "c"}},
		{ID: "c", Address: "192.0.2.3", Coordinator: "a", GroupID: "g", Members: []string{"a", "b", "c"}},
	}
	var calls []string
	join := func(_ context.Context, room, playback string) error {
		calls = append(calls, "join:"+room+":"+playback)
		return nil
	}
	unjoin := func(_ context.Context, room string) error {
		calls = append(calls, "unjoin:"+room)
		return nil
	}
	if err := setGroup(t.Context(), devices, []string{"a", "c"}, "a", join, unjoin); err != nil {
		t.Fatal(err)
	}
	want := []string{"unjoin:192.0.2.1", "unjoin:192.0.2.3", "join:192.0.2.3:192.0.2.1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("uncheck calls = %v, want %v", calls, want)
	}
	calls = nil
	if err := setGroup(t.Context(), devices, []string{"b"}, "b", join, unjoin); err != nil {
		t.Fatal(err)
	}
	want = []string{"unjoin:192.0.2.2"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("solo calls = %v, want %v", calls, want)
	}
}

func TestSetGroupUsesRequestedPlaybackRoomAsCoordinator(t *testing.T) {
	devices := []Device{
		{ID: "a", Address: "192.0.2.1", Coordinator: "a", GroupID: "g", Members: []string{"a", "b"}},
		{ID: "b", Address: "192.0.2.2", Coordinator: "a", GroupID: "g", Members: []string{"a", "b"}},
	}
	var calls []string
	join := func(_ context.Context, room, playback string) error {
		calls = append(calls, "join:"+room+":"+playback)
		return nil
	}
	unjoin := func(_ context.Context, room string) error {
		calls = append(calls, "unjoin:"+room)
		return nil
	}
	if err := setGroup(t.Context(), devices, []string{"a", "b"}, "b", join, unjoin); err != nil {
		t.Fatal(err)
	}
	want := []string{"unjoin:192.0.2.1", "unjoin:192.0.2.2", "join:192.0.2.1:192.0.2.2"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("reform calls=%v, want %v", calls, want)
	}

	calls = nil
	if err := setGroup(t.Context(), devices, []string{"a", "b"}, "a", join, unjoin); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("already-correct group mutated: %v", calls)
	}
}

func TestSetGroupAllowsBondedRoomButNeverSatelliteAndRejectsStaleID(t *testing.T) {
	devices := []Device{
		{ID: "room", Address: "192.0.2.1", Coordinator: "room", GroupID: "g", Members: []string{"room", "other"}, Bonded: []string{"sub"}},
		{ID: "sub", Address: "192.0.2.2", Coordinator: "room", GroupID: "g", Members: []string{"room", "other"}, Bonded: []string{"sub"}},
		{ID: "other", Address: "192.0.2.3", Coordinator: "room", GroupID: "g", Members: []string{"room", "other"}},
	}
	calls := 0
	join := func(context.Context, string, string) error { calls++; return nil }
	unjoin := func(context.Context, string) error { calls++; return nil }
	if err := setGroup(t.Context(), devices, []string{"room"}, "room", join, unjoin); err != nil {
		t.Fatalf("bonded logical room rejected: %v", err)
	}
	calls = 0
	if err := setGroup(t.Context(), devices, []string{"sub"}, "sub", join, unjoin); err == nil || calls != 0 {
		t.Fatalf("bonded satellite accepted or mutated: err=%v calls=%d", err, calls)
	}
	if err := setGroup(t.Context(), devices, []string{"gone"}, "gone", join, unjoin); err == nil || calls != 0 {
		t.Fatalf("stale room accepted or mutated: err=%v calls=%d", err, calls)
	}
}

func TestSetGroupAndSeparateKeepBondedSatelliteWithPrimaryRoom(t *testing.T) {
	devices := []Device{
		{ID: "bonded", Address: "192.0.2.1", Coordinator: "bonded", GroupID: "living", Members: []string{"bonded", "sub", "left"}, Bonded: []string{"sub"}},
		{ID: "sub", Address: "192.0.2.2", Coordinator: "bonded", GroupID: "living", Members: []string{"bonded", "sub", "left"}, Bonded: []string{"sub"}},
		{ID: "left", Address: "192.0.2.3", Coordinator: "bonded", GroupID: "living", Members: []string{"bonded", "sub", "left"}},
		{ID: "other", Address: "192.0.2.4", Coordinator: "other", GroupID: "office", Members: []string{"other"}},
		{ID: "untouched", Address: "192.0.2.5", Coordinator: "untouched", GroupID: "bedroom", Members: []string{"untouched"}},
	}
	var calls []string
	join := func(_ context.Context, room, playback string) error {
		calls = append(calls, "join:"+room+":"+playback)
		return nil
	}
	unjoin := func(_ context.Context, room string) error {
		calls = append(calls, "unjoin:"+room)
		return nil
	}
	if err := setGroup(t.Context(), devices, []string{"bonded", "other"}, "bonded", join, unjoin); err != nil {
		t.Fatalf("group bonded primary with another room: %v", err)
	}
	want := []string{"unjoin:192.0.2.1", "join:192.0.2.4:192.0.2.1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("group calls = %v, want %v", calls, want)
	}

	calls = nil
	if err := separateGroup(t.Context(), devices, "bonded", unjoin); err != nil {
		t.Fatalf("separate current group: %v", err)
	}
	want = []string{"unjoin:192.0.2.1", "unjoin:192.0.2.3"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("separate calls = %v, want %v", calls, want)
	}
}

func TestSetGroupStopsAtPartialFailureAndSeparateOnlyTouchesCurrentGroup(t *testing.T) {
	devices := []Device{
		{ID: "a", Address: "192.0.2.1", Name: "Alpha", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "b", Address: "192.0.2.2", Name: "Bravo", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "c", Address: "192.0.2.3", Name: "Charlie", Coordinator: "a", GroupID: "g1", Members: []string{"a", "b", "c"}},
		{ID: "x", Address: "192.0.2.9", Name: "Other", Coordinator: "x", GroupID: "g2", Members: []string{"x"}},
	}
	var calls []string
	err := setGroup(t.Context(), devices, []string{"b", "c"}, "b", func(_ context.Context, room, playback string) error {
		calls = append(calls, "join:"+room+":"+playback)
		return errors.New("join failed")
	}, func(_ context.Context, room string) error {
		calls = append(calls, "unjoin:"+room)
		return nil
	})
	if err == nil || len(calls) != 3 || calls[0] != "unjoin:192.0.2.2" || calls[1] != "unjoin:192.0.2.3" || calls[2] != "join:192.0.2.3:192.0.2.2" {
		t.Fatalf("partial apply err=%v calls=%v", err, calls)
	}
	calls = nil
	if err := separateGroup(t.Context(), devices, "a", func(_ context.Context, room string) error {
		calls = append(calls, room)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}) {
		t.Fatalf("separate calls = %v, want only the current group", calls)
	}
}
func TestReadStateRoutesTransportAndEQByScope(t *testing.T) {
	var calls []struct{ action, host, path string }
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		a := r.Header.Get("SOAPACTION")
		calls = append(calls, struct{ action, host, path string }{a, r.URL.Host, r.URL.Path})
		switch {
		case strings.Contains(a, "#GetZoneGroupState"):
			return topologyResponse(), nil
		case strings.Contains(a, "#GetTransportInfo"):
			return soap(`<GetTransportInfoResponse><CurrentTransportState>PAUSED_PLAYBACK</CurrentTransportState></GetTransportInfoResponse>`, 200), nil
		case strings.Contains(a, "#GetGroupVolume"):
			return soap(`<GetGroupVolumeResponse><CurrentVolume>27</CurrentVolume></GetGroupVolumeResponse>`, 200), nil
		case strings.Contains(a, "#GetBass"):
			return soap(`<GetBassResponse><CurrentBass>2</CurrentBass></GetBassResponse>`, 200), nil
		case strings.Contains(a, "#GetTreble"):
			return soap(`<GetTrebleResponse><CurrentTreble>-1</CurrentTreble></GetTrebleResponse>`, 200), nil
		case strings.Contains(a, "#GetLoudness"):
			return soap(`<GetLoudnessResponse><CurrentLoudness>1</CurrentLoudness></GetLoudnessResponse>`, 200), nil
		case strings.Contains(a, "#GetPositionInfo"):
			return soap(`<GetPositionInfoResponse><Track>83</Track><RelTime>00:01:23</RelTime><TrackDuration>00:04:56</TrackDuration><TrackMetaData>`+xmlEscape(`<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>Song</dc:title><dc:creator>Artist</dc:creator><upnp:album>Album</upnp:album></item></DIDL-Lite>`)+`</TrackMetaData></GetPositionInfoResponse>`, 200), nil
		case strings.Contains(a, "#GetMediaInfo"):
			return soap(`<GetMediaInfoResponse><CurrentURI>x-rincon-queue:RINCON_ROOM#0</CurrentURI></GetMediaInfoResponse>`, 200), nil
		case strings.Contains(a, "#Browse"):
			items := `<item><dc:title>Queued</dc:title><dc:creator>Q Artist</dc:creator><upnp:album>Q Album</upnp:album><res>secret</res></item>` + strings.Repeat(`<item><dc:title>Filler</dc:title></item>`, 200)
			didl := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` + items + `</DIDL-Lite>`
			return soap(`<BrowseResponse><Result>`+xmlEscape(didl)+`</Result><TotalMatches>301</TotalMatches></BrowseResponse>`, 200), nil
		default:
			t.Fatalf("unexpected action %s", a)
			return nil, nil
		}
	})
	c := NewClient(&http.Client{Transport: tr})
	s, err := c.ReadState(t.Context(), "192.0.2.12", true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Coordinator != "Room" || s.Transport != "PAUSED_PLAYBACK" || s.Track.Title != "Song" || s.QueueIndex != 82 || s.QueueTotal != 301 || len(s.Queue) != maxQueueItems || s.Queue[0].Artist != "Q Artist" || s.Volume == nil || *s.Volume != 27 || s.SpeakerEQ == nil || s.SpeakerEQ.Bass != 2 || s.SpeakerEQ.Treble != -1 || !s.SpeakerEQ.Loudness {
		t.Fatalf("state=%+v", s)
	}
	for _, call := range calls {
		a := call.action
		wantHost := "192.0.2.10:1400"
		wantPath := "/MediaRenderer/GroupRenderingControl/Control"
		switch {
		case strings.Contains(a, "#GetZoneGroupState"):
			wantHost = "192.0.2.12:1400"
			wantPath = "/ZoneGroupTopology/Control"
		case strings.Contains(a, "GroupRenderingControl:1#"):
			wantPath = "/MediaRenderer/GroupRenderingControl/Control"
		case strings.Contains(a, "RenderingControl:1#"):
			wantHost = "192.0.2.12:1400"
			wantPath = renderingPath
		case strings.Contains(a, "AVTransport:1#"):
			wantPath = avTransportPath
		case strings.Contains(a, "ContentDirectory:1#"):
			wantPath = contentPath
		}
		if call.host != wantHost {
			t.Errorf("%s host=%q, want %q", a, call.host, wantHost)
		}
		if call.path != wantPath {
			t.Errorf("%s request path=%q, want %q", a, call.path, wantPath)
		}
	}
}

func TestSetEQUsesRequestedLogicalRoomAndRejectsBondedSatellite(t *testing.T) {
	var bassHosts []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		if strings.Contains(r.Header.Get("SOAPACTION"), "#SetBass") {
			bassHosts = append(bassHosts, r.URL.Host)
		}
		return soap(`<ok/>`, 200), nil
	})})
	if err := client.SetEQ(t.Context(), "192.0.2.12", "Bass", 2); err != nil {
		t.Fatal(err)
	}
	if err := client.SetEQ(t.Context(), "192.0.2.11", "Bass", 2); err == nil {
		t.Fatal("bonded satellite accepted for EQ")
	}
	if _, err := client.ReadState(t.Context(), "192.0.2.11", false); err == nil {
		t.Fatal("bonded satellite accepted for EQ state")
	}
	if !reflect.DeepEqual(bassHosts, []string{"192.0.2.12:1400"}) {
		t.Fatalf("EQ requests went to %v", bassHosts)
	}
}
func TestJoinTargetsRequestedRoomCoordinator(t *testing.T) {
	var uri string
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#GetZoneGroupState") {
			return topologyResponse(), nil
		}
		if strings.Contains(r.Header.Get("SOAPACTION"), "#SetAVTransportURI") {
			b, _ := io.ReadAll(r.Body)
			uri = string(b)
		}
		return soap(`<ok/>`, 200), nil
	})
	c := NewClient(&http.Client{Transport: tr})
	if err := c.Join(t.Context(), "192.0.2.12", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "x-rincon:RINCON_ROOM") || strings.Contains(uri, "192.0.2.12:1400") {
		t.Fatalf("join URI=%s", uri)
	}
}

func TestShareLinkRegionFallbackOnlyAfterSOAPFault(t *testing.T) {
	var adds int
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		a := r.Header.Get("SOAPACTION")
		if strings.Contains(a, "#AddURIToQueue") {
			adds++
			b, _ := io.ReadAll(r.Body)
			want := "SA_RINCON2311_X_#Svc2311-0-Token"
			if adds == 1 {
				if !strings.Contains(string(b), want) {
					t.Fatalf("primary DIDL missing service: %s", b)
				}
				return soap(`<s:Fault><detail><UPnPError><errorCode>800</errorCode><errorDescription>service unavailable</errorDescription></UPnPError></detail></s:Fault>`, 500), nil
			}
			want = "SA_RINCON3079_X_#Svc3079-0-Token"
			if !strings.Contains(string(b), want) {
				t.Fatalf("alternate DIDL missing service: %s", b)
			}
			return soap(`<AddURIToQueueResponse><FirstTrackNumberEnqueued>1</FirstTrackNumberEnqueued></AddURIToQueueResponse>`, 200), nil
		}
		return soap(`<ok/>`, 200), nil
	})})
	if err := c.playShare(t.Context(), "192.0.2.1", "RINCON_TEST", "spotify:playlist:37i9dQZF1E38cclX2Ha8AV"); err != nil {
		t.Fatal(err)
	}
	if adds != 2 {
		t.Fatalf("AddURIToQueue calls=%d", adds)
	}
}
func TestShareLinkDoesNotRetryUncertainAppend(t *testing.T) {
	adds := 0
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#AddURIToQueue") {
			adds++
			return nil, errors.New("connection reset")
		}
		return soap(`<ok/>`, 200), nil
	})})
	err := c.playShare(t.Context(), "192.0.2.1", "RINCON_TEST", "spotify:playlist:37i9dQZF1E38cclX2Ha8AV")
	if err == nil || adds != 1 {
		t.Fatalf("err=%v append calls=%d", err, adds)
	}
}

func TestSeededDiscoveryUsesHouseholdTopology(t *testing.T) {
	var actions []string
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		actions = append(actions, r.Header.Get("SOAPACTION"))
		return topologyResponse(), nil
	})})
	c.Target = "192.0.2.10"
	devices, err := c.Discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || devices[0].Name != "Kitchen" || devices[0].Address != "192.0.2.12" {
		t.Fatalf("devices=%+v", devices)
	}
	if len(actions) != 1 || !strings.Contains(actions[0], "#GetZoneGroupState") {
		t.Fatalf("actions=%v", actions)
	}
}

func TestDiscoveryEnrichmentCancellationAndFallback(t *testing.T) {
	device := Device{ID: "192.0.2.10", Address: "192.0.2.10"}
	t.Run("caller cancellation returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, context.Canceled
		})})
		got, err := client.enrichDiscovered(ctx, []Device{device})
		if !errors.Is(err, context.Canceled) || len(got) != 0 {
			t.Fatalf("devices=%+v err=%v", got, err)
		}
	})
	t.Run("ordinary SOAP failure keeps bare device", func(t *testing.T) {
		client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("speaker unavailable")
		})})
		got, err := client.enrichDiscovered(t.Context(), []Device{device})
		if err != nil || !reflect.DeepEqual(got, []Device{device}) {
			t.Fatalf("devices=%+v err=%v", got, err)
		}
	})
	t.Run("per-action timeout keeps bare device while caller active", func(t *testing.T) {
		client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})})
		client.Timeout = 10 * time.Millisecond
		ctx := t.Context()
		got, err := client.enrichDiscovered(ctx, []Device{device})
		if err != nil || ctx.Err() != nil || !reflect.DeepEqual(got, []Device{device}) {
			t.Fatalf("devices=%+v caller err=%v discovery err=%v", got, ctx.Err(), err)
		}
	})
}

func TestDiscoveryEnrichmentEmptyHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := NewClient(nil)
	got, err := client.enrichDiscovered(ctx, nil)
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("devices=%+v err=%v", got, err)
	}
}

func TestDiscoveryEnrichmentEmptyHonorsExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	client := NewClient(nil)
	got, err := client.enrichDiscovered(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) || len(got) != 0 {
		t.Fatalf("devices=%+v err=%v", got, err)
	}
}

func TestDiscoveryEnrichmentSkipsCoveredAddressesAndKeepsOtherHouseholds(t *testing.T) {
	firstHousehold := `<ZoneGroups><ZoneGroup Coordinator="RINCON_ROOM" ID="room"><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/device.xml"><Satellite UUID="RINCON_SUB" ZoneName="Sub" Location="http://192.0.2.11:1400/device.xml"/></ZoneGroupMember><ZoneGroupMember UUID="RINCON_KITCHEN" ZoneName="Kitchen" Location="http://192.0.2.12:1400/device.xml"/></ZoneGroup></ZoneGroups>`
	secondHousehold := `<ZoneGroups><ZoneGroup Coordinator="RINCON_OFFICE" ID="office"><ZoneGroupMember UUID="RINCON_OFFICE" ZoneName="Office" Location="http://192.0.2.20:1400/device.xml"/><ZoneGroupMember UUID="RINCON_GUEST" ZoneName="Guest" Location="http://192.0.2.21:1400/device.xml"/></ZoneGroup></ZoneGroups>`
	var requests []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Hostname())
		switch r.URL.Hostname() {
		case "192.0.2.10":
			return topologyResponseFor(firstHousehold), nil
		case "192.0.2.20":
			return topologyResponseFor(secondHousehold), nil
		default:
			t.Fatalf("unexpected topology request to %s", r.URL.Hostname())
			return nil, nil
		}
	})})
	got, err := client.enrichDiscovered(t.Context(), []Device{
		{ID: "192.0.2.10", Address: "192.0.2.10"},
		{ID: "192.0.2.12", Address: "192.0.2.12"},
		{ID: "192.0.2.20", Address: "192.0.2.20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("topology requests=%v", requests)
	}
	ids := deviceIDs(got)
	for _, id := range []string{"RINCON_ROOM", "RINCON_SUB", "RINCON_KITCHEN", "RINCON_OFFICE", "RINCON_GUEST"} {
		if !contains(ids, id) {
			t.Errorf("missing device %s from %+v", id, got)
		}
	}
}

func TestDiscoveryEnrichmentRemovesCoveredFallbackAndKeepsUncovered(t *testing.T) {
	topology := `<ZoneGroups><ZoneGroup Coordinator="RINCON_ROOM" ID="room"><ZoneGroupMember UUID="RINCON_ROOM" ZoneName="Room" Location="http://192.0.2.10:1400/device.xml"/><ZoneGroupMember UUID="RINCON_RECOVERED" ZoneName="Recovered" Location="http://192.0.2.13:1400/device.xml"/></ZoneGroup></ZoneGroups>`
	var requests []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		address := r.URL.Hostname()
		requests = append(requests, address)
		if address == "192.0.2.10" {
			return topologyResponseFor(topology), nil
		}
		return nil, errors.New("speaker unavailable")
	})})
	got, err := client.enrichDiscovered(t.Context(), []Device{
		{ID: "192.0.2.13", Address: "192.0.2.13"},
		{ID: "192.0.2.99", Address: "192.0.2.99"},
		{ID: "192.0.2.10", Address: "192.0.2.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []string{"192.0.2.13", "192.0.2.99", "192.0.2.10"}) {
		t.Fatalf("topology requests=%v", requests)
	}
	ids := deviceIDs(got)
	if contains(ids, "192.0.2.13") || !contains(ids, "RINCON_RECOVERED") || !contains(ids, "192.0.2.99") {
		t.Fatalf("fallback or recovered topology incorrect: %+v", got)
	}
}

func deviceIDs(devices []Device) []string {
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestDiscoveryDeadlineLeavesCallerTimeForTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 800*time.Millisecond)
	defer cancel()
	client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return topologyResponse(), nil
	})})
	client.Timeout = 2 * time.Second
	callerDeadline, _ := ctx.Deadline()
	deadline := discoveryDeadline(ctx, client.Timeout)
	if !deadline.Before(callerDeadline) {
		t.Fatalf("SSDP deadline %v does not leave caller time before %v", deadline, callerDeadline)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	target := conn.LocalAddr().(*net.UDPAddr)
	sender, err := net.DialUDP("udp4", nil, target)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	response := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:ZonePlayer:1\r\nLOCATION: http://192.0.2.10:1400/device.xml\r\n\r\n"
	if _, err := sender.Write([]byte(response)); err != nil {
		t.Fatal(err)
	}
	seen, err := collectSSDP(ctx, conn, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("caller context expired during SSDP collection: %v", err)
	}
	devices, err := client.enrichDiscovered(ctx, []Device{seen["192.0.2.10"]})
	if err != nil || len(devices) != 3 {
		t.Fatalf("topology devices=%+v err=%v", devices, err)
	}
}

func TestCollectSSDPRequiresSonosIdentityAndLocation(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sender, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	responses := []string{
		"HTTP/1.1 200 OK\r\nsT: URN:schemas-upnp-org:device:zoneplayer:1\r\nLOCATION: http://192.0.2.10:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.11:1400/device.xml\r\nuSn: UUID:rincon_valid::urn:schemas-upnp-org:device:ZonePlayer:1\r\n\r\n",
		"HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.15:1400/device.xml\r\nuSn: UUID:RINCON_bareuuid\r\n\r\n",
		"HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:NotZonePlayer:1\r\nUSN: uuid:NOTRINCON_router::upnp:rootdevice\r\nLOCATION: http://192.0.2.12:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaServer:1\r\nUSN: uuid:UPNP_media_server::upnp:rootdevice\r\nLOCATION: http://192.0.2.13:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nUSN: uuid:RINCON_::upnp:rootdevice\r\nLOCATION: http://192.0.2.16:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nUSN: RINCON_without_uuid_prefix::upnp:rootdevice\r\nLOCATION: http://192.0.2.17:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.14:1400/device.xml\r\n\r\n",
		"HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:ZonePlayer:1\r\n\r\n",
	}
	for _, response := range responses {
		if _, err := sender.Write([]byte(response)); err != nil {
			t.Fatal(err)
		}
	}
	seen, err := collectSSDP(t.Context(), conn, time.Now().Add(40*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen["192.0.2.10"].Address == "" || seen["192.0.2.11"].Address == "" || seen["192.0.2.15"].Address == "" {
		t.Fatalf("qualified discovery results=%+v", seen)
	}

	var requests []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Hostname())
		return nil, errors.New("offline topology")
	})})
	devices, err := client.enrichDiscovered(t.Context(), []Device{seen["192.0.2.10"], seen["192.0.2.11"], seen["192.0.2.15"]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []string{"192.0.2.10", "192.0.2.11", "192.0.2.15"}) || len(devices) != 3 {
		t.Fatalf("topology requests=%v fallback devices=%+v", requests, devices)
	}
}

func TestShareLinkDoesNotRetryHTTPFailureWithoutUPnPFault(t *testing.T) {
	adds := 0
	c := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.Header.Get("SOAPACTION"), "#AddURIToQueue") {
			adds++
			return soap(`<s:Fault/>`, 500), nil
		}
		return soap(`<ok/>`, 200), nil
	})})
	err := c.playShare(t.Context(), "192.0.2.1", "RINCON_TEST", "spotify:playlist:37i9dQZF1E38cclX2Ha8AV")
	if err == nil || adds != 1 {
		t.Fatalf("err=%v append calls=%d", err, adds)
	}
}
