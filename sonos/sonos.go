package sonos

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	avTransport      = "urn:schemas-upnp-org:service:AVTransport:1"
	renderingControl = "urn:schemas-upnp-org:service:RenderingControl:1"
	contentDirectory = "urn:schemas-upnp-org:service:ContentDirectory:1"
	groupRendering   = "urn:schemas-upnp-org:service:GroupRenderingControl:1"
	topology         = "urn:schemas-upnp-org:service:ZoneGroupTopology:1"
	avTransportPath  = "/MediaRenderer/AVTransport/Control"
	renderingPath    = "/MediaRenderer/RenderingControl/Control"
	contentPath      = "/MediaServer/ContentDirectory/Control"
	maxQueueItems    = 200
)

var sonosActionArguments = map[string][]string{
	avTransport + "#Play":                               {"InstanceID", "Speed"},
	avTransport + "#Pause":                              {"InstanceID"},
	avTransport + "#Next":                               {"InstanceID"},
	avTransport + "#Previous":                           {"InstanceID"},
	avTransport + "#GetTransportInfo":                   {"InstanceID"},
	avTransport + "#GetPositionInfo":                    {"InstanceID"},
	avTransport + "#GetMediaInfo":                       {"InstanceID"},
	avTransport + "#SetAVTransportURI":                  {"InstanceID", "CurrentURI", "CurrentURIMetaData"},
	avTransport + "#Seek":                               {"InstanceID", "Unit", "Target"},
	avTransport + "#BecomeCoordinatorOfStandaloneGroup": {"InstanceID"},
	avTransport + "#AddURIToQueue":                      {"InstanceID", "EnqueuedURI", "EnqueuedURIMetaData", "DesiredFirstTrackNumberEnqueued", "EnqueueAsNext"},
	renderingControl + "#GetBass":                       {"InstanceID"},
	renderingControl + "#SetBass":                       {"InstanceID", "DesiredBass"},
	renderingControl + "#GetTreble":                     {"InstanceID"},
	renderingControl + "#SetTreble":                     {"InstanceID", "DesiredTreble"},
	renderingControl + "#GetLoudness":                   {"InstanceID", "Channel"},
	renderingControl + "#SetLoudness":                   {"InstanceID", "Channel", "DesiredLoudness"},
	groupRendering + "#GetGroupVolume":                  {"InstanceID"},
	groupRendering + "#SetGroupVolume":                  {"InstanceID", "DesiredVolume"},
	contentDirectory + "#Browse":                        {"ObjectID", "BrowseFlag", "Filter", "StartingIndex", "RequestedCount", "SortCriteria"},
	topology + "#GetZoneGroupState":                     {},
}

type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Address     string   `json:"address,omitempty"`
	Coordinator string   `json:"coordinator,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	Members     []string `json:"members,omitempty"`
	Bonded      []string `json:"bonded,omitempty"`
}
type Track struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}
type State struct {
	Coordinator string  `json:"coordinator"`
	Volume      *int    `json:"volume,omitempty"`
	SpeakerEQ   *EQ     `json:"speaker_eq,omitempty"`
	Source      string  `json:"source"`
	Warning     string  `json:"warning,omitempty"`
	Transport   string  `json:"transport"`
	Track       Track   `json:"track"`
	Position    string  `json:"position"`
	Duration    string  `json:"duration"`
	QueueIndex  int     `json:"queue_index"`
	QueueTotal  int     `json:"queue_total"`
	Queue       []Track `json:"queue"`
}
type EQ struct {
	Bass     int  `json:"bass"`
	Treble   int  `json:"treble"`
	Loudness bool `json:"loudness"`
}
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
	Target  string
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{HTTP: httpClient, Timeout: 3 * time.Second}
}
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
func validateAddress(address string) error {
	if net.ParseIP(address) == nil {
		return fmt.Errorf("invalid Sonos IP address %q", address)
	}
	return nil
}

// Discover uses SSDP unless Target is set, in which case it reads household topology from that speaker.
func (c *Client) Discover(ctx context.Context) ([]Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Target != "" {
		if err := validateAddress(c.Target); err != nil {
			return nil, err
		}
		return c.Household(ctx, c.Target)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("Sonos discovery: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: urn:schemas-upnp-org:device:ZonePlayer:1\r\n\r\n"
	if _, err = conn.WriteToUDP([]byte(msg), &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}); err != nil {
		return nil, err
	}
	seen := map[string]Device{}
	buf := make([]byte, 8192)
	stopCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-stopCancel:
		}
	}()
	defer close(stopCancel)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, peer, e := conn.ReadFromUDP(buf)
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				break
			}
			return nil, fmt.Errorf("Sonos discovery: %w", e)
		}
		loc := ""
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "location:") {
				loc = strings.TrimSpace(line[len("location:"):])
				break
			}
		}
		if loc == "" {
			continue
		}
		u, e := url.Parse(loc)
		if e != nil || u.Hostname() == "" {
			continue
		}
		ip := u.Hostname()
		if net.ParseIP(ip) == nil {
			ip = peer.IP.String()
		}
		seen[ip] = Device{ID: ip, Address: ip}
	}
	out := make([]Device, 0, len(seen))
	for _, d := range seen {
		if household, e := c.Household(ctx, d.Address); e == nil {
			out = append(out, household...)
		} else {
			out = append(out, d)
		}
	}
	return sortedUnique(out), nil
}
func sortedUnique(in []Device) []Device {
	m := map[string]Device{}
	for _, d := range in {
		m[d.ID] = d
	}
	out := make([]Device, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
func (c *Client) Household(ctx context.Context, address string) ([]Device, error) {
	if err := validateAddress(address); err != nil {
		return nil, err
	}
	r, e := c.action(ctx, address, "/ZoneGroupTopology/Control", topology, "GetZoneGroupState", nil)
	if e != nil {
		return nil, e
	}
	var out []Device
	dec := xml.NewDecoder(strings.NewReader(r["ZoneGroupState"]))
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "ZoneGroup" {
			continue
		}
		var g struct {
			Coordinator string `xml:"Coordinator,attr"`
			ID          string `xml:"ID,attr"`
			Members     []struct {
				UUID       string `xml:"UUID,attr"`
				Location   string `xml:"Location,attr"`
				ZoneName   string `xml:"ZoneName,attr"`
				Invisible  string `xml:"Invisible,attr"`
				Satellites []struct {
					UUID     string `xml:"UUID,attr"`
					Location string `xml:"Location,attr"`
					ZoneName string `xml:"ZoneName,attr"`
				} `xml:"Satellite"`
			} `xml:"ZoneGroupMember"`
		}
		if e = dec.DecodeElement(&g, &start); e != nil {
			return nil, e
		}
		members := []string{}
		bondedByMember := make(map[string][]string)
		for _, m := range g.Members {
			members = append(members, m.UUID)
			for _, satellite := range m.Satellites {
				bondedByMember[m.UUID] = append(bondedByMember[m.UUID], satellite.UUID)
			}
		}
		for _, m := range g.Members {
			u, _ := url.Parse(m.Location)
			ip := ""
			if u != nil {
				ip = u.Hostname()
			}
			if ip == "" && m.UUID == "" {
				continue
			}
			id := m.UUID
			if id == "" {
				id = ip
			}
			if strings.EqualFold(m.Invisible, "1") {
				continue
			}
			out = append(out, Device{ID: id, Name: m.ZoneName, Address: ip, Coordinator: g.Coordinator, GroupID: g.ID, Members: members, Bonded: bondedByMember[m.UUID]})
			for _, satellite := range m.Satellites {
				su, _ := url.Parse(satellite.Location)
				satelliteIP := ""
				if su != nil {
					satelliteIP = su.Hostname()
				}
				out = append(out, Device{ID: satellite.UUID, Name: satellite.ZoneName, Address: satelliteIP, Coordinator: g.Coordinator, GroupID: g.ID, Members: members, Bonded: []string{satellite.UUID}})
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Sonos topology contained no zones")
	}
	return sortedUnique(out), nil
}
func (c *Client) coordinator(ctx context.Context, address string) (Device, error) {
	ds, e := c.Household(ctx, address)
	if e != nil {
		return Device{}, e
	}
	var member *Device
	for _, d := range ds {
		if d.Address == address {
			member = &d
			break
		}
	}
	if member == nil {
		return Device{}, fmt.Errorf("Sonos address %q is not in household topology", address)
	}
	for _, d := range ds {
		if d.ID == member.Coordinator {
			if d.Address == "" {
				return Device{}, fmt.Errorf("Sonos coordinator %q has no address", d.ID)
			}
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("Sonos coordinator %q for address %q is missing from household topology", member.Coordinator, address)
}
func (c *Client) Control(ctx context.Context, address, action, uri string) error {
	if err := validateAddress(address); err != nil {
		return err
	}
	d, e := c.coordinator(ctx, address)
	if e != nil {
		return e
	}
	switch action {
	case "play":
		if uri != "" {
			return c.playShare(ctx, d.Address, d.ID, uri)
		}
		_, e = c.action(ctx, d.Address, avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"})
	case "pause":
		_, e = c.action(ctx, d.Address, avTransportPath, avTransport, "Pause", map[string]string{"InstanceID": "0"})
	case "next":
		_, e = c.action(ctx, d.Address, avTransportPath, avTransport, "Next", map[string]string{"InstanceID": "0"})
	case "previous":
		var handled bool
		handled, e = c.previousQueueTrack(ctx, d.Address)
		if e == nil && !handled {
			_, e = c.action(ctx, d.Address, avTransportPath, avTransport, "Previous", map[string]string{"InstanceID": "0"})
		}
	case "toggle":
		r, x := c.action(ctx, d.Address, avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
		if x != nil {
			return x
		}
		if r["CurrentTransportState"] == "PLAYING" {
			return c.Control(ctx, address, "pause", "")
		}
		return c.Control(ctx, address, "play", "")
	default:
		return fmt.Errorf("unsupported Sonos action %q", action)
	}
	return e
}

func (c *Client) previousQueueTrack(ctx context.Context, address string) (bool, error) {
	media, err := c.action(ctx, address, avTransportPath, avTransport, "GetMediaInfo", map[string]string{"InstanceID": "0"})
	if err != nil || !strings.HasPrefix(media["CurrentURI"], "x-rincon-queue:") {
		return false, nil
	}
	position, err := c.action(ctx, address, avTransportPath, avTransport, "GetPositionInfo", map[string]string{"InstanceID": "0"})
	if err != nil {
		return false, nil
	}
	relTime, timeErr := time.Parse("15:04:05", position["RelTime"])
	elapsed := time.Duration(relTime.Hour())*time.Hour + time.Duration(relTime.Minute())*time.Minute + time.Duration(relTime.Second())*time.Second + time.Duration(relTime.Nanosecond())
	if timeErr != nil {
		return false, nil
	}
	track, err := strconv.Atoi(position["Track"])
	if err != nil || track < 1 {
		return false, nil
	}
	transport, err := c.action(ctx, address, avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
	if err != nil {
		return false, nil
	}
	unit, target := "TRACK_NR", strconv.Itoa(track-1)
	if track == 1 || elapsed > 3*time.Second {
		unit, target = "REL_TIME", "00:00:00"
	}
	if _, err = c.action(ctx, address, avTransportPath, avTransport, "Seek", map[string]string{"InstanceID": "0", "Unit": unit, "Target": target}); err != nil {
		return true, err
	}
	if transport["CurrentTransportState"] == "PLAYING" || transport["CurrentTransportState"] == "TRANSITIONING" {
		_, err = c.action(ctx, address, avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"})
	}
	return true, err
}
func (c *Client) PlayQueueIndex(ctx context.Context, address string, index int) error {
	if index < 0 {
		return fmt.Errorf("queue index must be zero or greater")
	}
	d, err := c.coordinator(ctx, address)
	if err != nil {
		return err
	}
	if _, err = c.action(ctx, d.Address, avTransportPath, avTransport, "Seek", map[string]string{"InstanceID": "0", "Unit": "TRACK_NR", "Target": strconv.Itoa(index + 1)}); err != nil {
		return err
	}
	_, err = c.action(ctx, d.Address, avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"})
	return err
}

// SpotifyQueueRange identifies a segment appended to a Sonos group queue.
type SpotifyQueueRange struct {
	Address          string
	CoordinatorID    string
	FirstTrackNumber int
	Appended         int
}

// AppendSpotifyTracks appends Spotify tracks without changing Sonos transport.
// The returned count includes successful appends if a later append fails.
func (c *Client) AppendSpotifyTracks(ctx context.Context, address string, uris []string) (result SpotifyQueueRange, err error) {
	if err := validateAddress(address); err != nil {
		return result, err
	}
	if len(uris) == 0 {
		return result, fmt.Errorf("Spotify handoff requires tracks")
	}
	for _, uri := range uris {
		if _, err := spotifyShare(uri); err != nil {
			return result, err
		}
	}
	d, err := c.coordinator(ctx, address)
	if err != nil {
		return result, err
	}
	result.Address, result.CoordinatorID = d.Address, d.ID
	first := 0
	for i, uri := range uris {
		index, added, queueLength, err := c.enqueueSpotify(ctx, d.Address, uri)
		if err != nil {
			return result, fmt.Errorf("enqueue track %d: %w", i+1, err)
		}
		if i == 0 {
			first = index
			result.FirstTrackNumber = first
		} else if index != first+i {
			result.Appended++
			return result, fmt.Errorf("Sonos queue changed during handoff (expected item %d, got %d; NumTracksAdded %q, NewQueueLength %q)", first+i, index, added, queueLength)
		}
		result.Appended++
	}
	result.FirstTrackNumber = first
	return result, nil
}

// StartSpotifyQueue starts an appended segment. started reports whether Sonos
// accepted Play; positionRestored distinguishes a later REL_TIME seek failure.
func (c *Client) StartSpotifyQueue(ctx context.Context, segment SpotifyQueueRange, position time.Duration) (started, positionRestored bool, err error) {
	if err := validateAddress(segment.Address); err != nil {
		return false, false, err
	}
	if segment.CoordinatorID == "" || segment.FirstTrackNumber < 1 || segment.Appended < 1 || position < 0 {
		return false, false, fmt.Errorf("invalid Sonos Spotify queue segment")
	}
	d, err := c.coordinator(ctx, segment.Address)
	if err != nil {
		return false, false, fmt.Errorf("verify Sonos handoff coordinator: %w", err)
	}
	if d.Address != segment.Address || d.ID != segment.CoordinatorID {
		return false, false, fmt.Errorf("Sonos group coordinator changed during handoff; appended songs remain queued")
	}
	if _, err := c.action(ctx, segment.Address, avTransportPath, avTransport, "SetAVTransportURI", map[string]string{"InstanceID": "0", "CurrentURI": "x-rincon-queue:" + segment.CoordinatorID + "#0", "CurrentURIMetaData": ""}); err != nil {
		return false, false, err
	}
	if _, err := c.action(ctx, segment.Address, avTransportPath, avTransport, "Seek", map[string]string{"InstanceID": "0", "Unit": "TRACK_NR", "Target": strconv.Itoa(segment.FirstTrackNumber)}); err != nil {
		return false, false, err
	}
	if _, err := c.action(ctx, segment.Address, avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"}); err != nil {
		return false, false, err
	}
	started = true
	if position == 0 {
		return true, true, nil
	}
	if _, err := c.action(ctx, segment.Address, avTransportPath, avTransport, "Seek", map[string]string{"InstanceID": "0", "Unit": "REL_TIME", "Target": sonosTime(position)}); err != nil {
		return true, false, err
	}
	return true, true, nil
}

func sonosTime(position time.Duration) string {
	seconds := int64(position / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

func (c *Client) ReadState(ctx context.Context, address string, includeQueue bool) (State, error) {
	state := State{QueueIndex: -1}
	d, e := c.coordinator(ctx, address)
	if e != nil {
		return state, e
	}
	state.Coordinator = d.Name
	if value, err := c.action(ctx, d.Address, "/MediaRenderer/GroupRenderingControl/Control", groupRendering, "GetGroupVolume", map[string]string{"InstanceID": "0"}); err == nil {
		if volume, err := strconv.Atoi(value["CurrentVolume"]); err == nil {
			state.Volume = &volume
		}
	}
	bass, bassErr := c.action(ctx, d.Address, renderingPath, renderingControl, "GetBass", map[string]string{"InstanceID": "0"})
	treble, trebleErr := c.action(ctx, d.Address, renderingPath, renderingControl, "GetTreble", map[string]string{"InstanceID": "0"})
	loudness, loudnessErr := c.action(ctx, d.Address, renderingPath, renderingControl, "GetLoudness", map[string]string{"InstanceID": "0", "Channel": "Master"})
	if bassErr == nil && trebleErr == nil && loudnessErr == nil {
		bassValue, bassErr := strconv.Atoi(bass["CurrentBass"])
		trebleValue, trebleErr := strconv.Atoi(treble["CurrentTreble"])
		loudnessValue, loudnessErr := strconv.Atoi(loudness["CurrentLoudness"])
		if bassErr == nil && trebleErr == nil && loudnessErr == nil {
			state.SpeakerEQ = &EQ{Bass: bassValue, Treble: trebleValue, Loudness: loudnessValue != 0}
		}
	}
	t, e := c.action(ctx, d.Address, avTransportPath, avTransport, "GetTransportInfo", map[string]string{"InstanceID": "0"})
	if e != nil {
		return state, e
	}
	state.Transport = t["CurrentTransportState"]
	p, e := c.action(ctx, d.Address, avTransportPath, avTransport, "GetPositionInfo", map[string]string{"InstanceID": "0"})
	if e == nil {
		state.Track = metadataTrack(p["TrackMetaData"])
		state.Position = clock(p["RelTime"])
		state.Duration = clock(p["TrackDuration"])
		if track, err := strconv.Atoi(p["Track"]); err == nil && track > 0 {
			state.QueueIndex = track - 1
		}
	}
	m, e := c.action(ctx, d.Address, avTransportPath, avTransport, "GetMediaInfo", map[string]string{"InstanceID": "0"})
	if e != nil {
		return state, e
	}
	uri := m["CurrentURI"]
	switch {
	case strings.HasPrefix(uri, "x-rincon-queue:"):
		state.Source = "Sonos queue"
	case strings.HasPrefix(uri, "x-sonos-vli:"):
		state.Source = "Sonos virtual input"
	default:
		state.Source = "Other Sonos source"
	}
	if !includeQueue {
		return state, nil
	}
	r, e := c.action(ctx, d.Address, contentPath, contentDirectory, "Browse", map[string]string{"ObjectID": "Q:0", "BrowseFlag": "BrowseDirectChildren", "Filter": "*", "StartingIndex": "0", "RequestedCount": strconv.Itoa(maxQueueItems), "SortCriteria": ""})
	if e != nil {
		return state, e
	}
	state.QueueTotal, _ = strconv.Atoi(r["TotalMatches"])
	state.Queue = parseDIDL(r["Result"])
	if len(state.Queue) > maxQueueItems {
		state.Queue = state.Queue[:maxQueueItems]
	}
	return state, nil
}
func clock(s string) string {
	p := strings.Split(s, ":")
	if len(p) != 3 {
		return ""
	}
	for i, x := range p {
		if _, e := strconv.Atoi(x); e != nil || len(x) < 2 && i > 0 {
			return ""
		}
	}
	return s
}
func metadataTrack(raw string) Track {
	items := parseDIDL(raw)
	if len(items) > 0 {
		return items[0]
	}
	return Track{}
}
func parseDIDL(raw string) []Track {
	var v struct {
		Items []struct {
			Title   string `xml:"title"`
			Creator string `xml:"creator"`
			Album   string `xml:"album"`
		} `xml:"item"`
	}
	_ = xml.Unmarshal([]byte(raw), &v)
	out := make([]Track, 0, len(v.Items))
	for _, i := range v.Items {
		out = append(out, Track{Title: i.Title, Artist: i.Creator, Album: i.Album})
	}
	return out
}
func (c *Client) SetVolume(ctx context.Context, address string, value int) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}
	d, e := c.coordinator(ctx, address)
	if e != nil {
		return e
	}
	_, e = c.action(ctx, d.Address, "/MediaRenderer/GroupRenderingControl/Control", groupRendering, "SetGroupVolume", map[string]string{"InstanceID": "0", "DesiredVolume": strconv.Itoa(value)})
	return e
}
func (c *Client) SetEQ(ctx context.Context, address, setting string, value int) error {
	if setting != "Bass" && setting != "Treble" && setting != "Loudness" {
		return fmt.Errorf("unsupported EQ setting %q", setting)
	}
	if setting == "Loudness" && (value < 0 || value > 1) || setting != "Loudness" && (value < -10 || value > 10) {
		return fmt.Errorf("EQ value out of range")
	}
	d, e := c.coordinator(ctx, address)
	if e != nil {
		return e
	}
	arg := map[string]string{"InstanceID": "0"}
	action := "SetBass"
	switch setting {
	case "Bass":
		arg["DesiredBass"] = strconv.Itoa(value)
	case "Treble":
		action = "SetTreble"
		arg["DesiredTreble"] = strconv.Itoa(value)
	case "Loudness":
		action = "SetLoudness"
		arg["Channel"] = "Master"
		arg["DesiredLoudness"] = strconv.Itoa(value)
	}
	_, e = c.action(ctx, d.Address, renderingPath, renderingControl, action, arg)
	return e
}
func (c *Client) Join(ctx context.Context, target, room string) error {
	return c.group(ctx, target, room, true)
}
func (c *Client) Unjoin(ctx context.Context, target string) error {
	return c.group(ctx, target, "", false)
}

// SetGroup makes rooms the exact membership of one group while preserving
// every unchecked room in each source group it touches.
func (c *Client) SetGroup(ctx context.Context, rooms []string, playback string) error {
	discovery, err := c.Discover(ctx)
	if err != nil {
		return err
	}
	return setGroup(ctx, discovery, rooms, playback, c.Join, c.Unjoin)
}

// SeparateGroup turns the logical rooms in the target's current group into
// standalone groups, leaving every other household group untouched.
func (c *Client) SeparateGroup(ctx context.Context, target string) error {
	discovery, err := c.Discover(ctx)
	if err != nil {
		return err
	}
	return separateGroup(ctx, discovery, target, c.Unjoin)
}

func separateGroup(ctx context.Context, discovery []Device, target string, unjoin func(context.Context, string) error) error {
	var groupID string
	for _, d := range discovery {
		if d.ID == target {
			groupID = d.GroupID
			break
		}
	}
	if groupID == "" {
		return fmt.Errorf("Sonos room %s is unavailable", target)
	}
	members := devicesInGroup(discovery, groupID)
	if len(members) < 2 {
		return nil
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	for _, d := range members {
		if err := unjoin(ctx, d.Address); err != nil {
			return fmt.Errorf("separate %s: %w", d.Name, err)
		}
	}
	return nil
}

func setGroup(ctx context.Context, devices []Device, rooms []string, playback string, join func(context.Context, string, string) error, unjoin func(context.Context, string) error) error {
	if len(rooms) == 0 || !contains(rooms, playback) {
		return errors.New("Sonos group needs a checked playback room")
	}
	selected := make(map[string]bool, len(rooms))
	for _, id := range rooms {
		selected[id] = true
	}
	byID := make(map[string]Device, len(devices))
	for _, d := range devices {
		byID[d.ID] = d
	}
	for id := range selected {
		d, ok := byID[id]
		if !ok || d.Address == "" || bondedSatellite(d) {
			return fmt.Errorf("Sonos room %s is unavailable or not a logical room", id)
		}
		if d.GroupID == "" || d.Coordinator == "" || len(d.Members) == 0 {
			return fmt.Errorf("Sonos room %s has incomplete group information", id)
		}
		leader, ok := byID[d.Coordinator]
		if !ok || leader.GroupID != d.GroupID || !contains(leader.Members, id) {
			return fmt.Errorf("Sonos room %s has stale group information", id)
		}
		for _, memberID := range leader.Members {
			member, ok := byID[memberID]
			if !ok || member.GroupID != d.GroupID {
				return fmt.Errorf("Sonos group for %s has incomplete membership", id)
			}
		}
	}
	groups := make(map[string][]string)
	for _, d := range devices {
		if d.ID == d.Coordinator && intersects(d.Members, selected) {
			for _, member := range d.Members {
				if room, ok := byID[member]; ok && !bondedSatellite(room) {
					groups[d.GroupID] = append(groups[d.GroupID], member)
				}
			}
		}
	}
	for groupID := range groups {
		sort.Strings(groups[groupID])
		if len(groups[groupID]) == len(selected) && intersects(groups[groupID], selected) {
			allSelected := true
			for _, id := range groups[groupID] {
				allSelected = allSelected && selected[id]
			}
			if allSelected && len(groups) == 1 {
				return nil
			}
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		room := byID[id]
		if len(groups[room.GroupID]) < 2 {
			continue
		}
		if err := unjoin(ctx, room.Address); err != nil {
			return fmt.Errorf("separate %s: %w", id, err)
		}
	}
	for _, id := range ids {
		if id == playback {
			continue
		}
		if err := join(ctx, byID[id].Address, byID[playback].Address); err != nil {
			return fmt.Errorf("join %s with %s: %w", id, playback, err)
		}
	}
	return nil
}

func devicesInGroup(devices []Device, groupID string) []Device {
	var members []Device
	for _, d := range devices {
		if d.GroupID == groupID && !bondedSatellite(d) {
			members = append(members, d)
		}
	}
	return members
}

func intersects(ids []string, selected map[string]bool) bool {
	for _, id := range ids {
		if selected[id] {
			return true
		}
	}
	return false
}

func bondedSatellite(d Device) bool {
	return len(d.Bonded) > 0 && contains(d.Bonded, d.ID)
}

func (c *Client) group(ctx context.Context, target, room string, join bool) error {
	if err := validateAddress(target); err != nil {
		return err
	}
	if join {
		if err := validateAddress(room); err != nil {
			return err
		}
		ds, e := c.Household(ctx, target)
		if e != nil {
			return e
		}
		var unit Device
		for _, d := range ds {
			if d.Address == target {
				unit = d
				break
			}
		}
		if unit.ID == "" {
			return fmt.Errorf("target speaker missing from Sonos topology")
		}
		if bondedSatellite(unit) {
			return fmt.Errorf("cannot group bonded Sonos unit %s", target)
		}
		roomDevice, e := c.coordinator(ctx, room)
		if e != nil {
			return e
		}
		roomID := roomDevice.ID
		if roomID == "" {
			roomID = roomDevice.Address
		}
		_, e = c.action(ctx, target, avTransportPath, avTransport, "SetAVTransportURI", map[string]string{"InstanceID": "0", "CurrentURI": "x-rincon:" + roomID, "CurrentURIMetaData": ""})
		return e
	}
	ds, e := c.Household(ctx, target)
	if e != nil {
		return e
	}
	for _, d := range ds {
		if d.Address == target && bondedSatellite(d) {
			return fmt.Errorf("cannot ungroup bonded Sonos unit %s", target)
		}
	}
	_, e = c.action(ctx, target, avTransportPath, avTransport, "BecomeCoordinatorOfStandaloneGroup", map[string]string{"InstanceID": "0"})
	return e
}
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (c *Client) playShare(ctx context.Context, address, coordinatorID, uri string) error {
	position, _, _, err := c.enqueueSpotify(ctx, address, uri)
	if err != nil {
		return err
	}
	_, err = c.action(ctx, address, avTransportPath, avTransport, "SetAVTransportURI", map[string]string{"InstanceID": "0", "CurrentURI": "x-rincon-queue:" + coordinatorID + "#0", "CurrentURIMetaData": ""})
	if err != nil {
		return err
	}
	_, err = c.action(ctx, address, avTransportPath, avTransport, "Seek", map[string]string{"InstanceID": "0", "Unit": "TRACK_NR", "Target": strconv.Itoa(position)})
	if err != nil {
		return err
	}
	_, err = c.action(ctx, address, avTransportPath, avTransport, "Play", map[string]string{"InstanceID": "0", "Speed": "1"})
	return err
}

func (c *Client) enqueueSpotify(ctx context.Context, address, uri string) (int, string, string, error) {
	spec, err := spotifyShare(uri)
	if err != nil {
		return 0, "", "", err
	}
	var response map[string]string
	for _, service := range []string{"2311", "3079"} {
		spec.service = service
		metadata := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:r="urn:schemas-rinconnetworks-com:metadata-1-0/" xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"><item id="` + xmlEscape(spec.key+spec.encoded) + `" parentID="-1" restricted="true"><dc:title></dc:title><upnp:class>` + spec.class + `</upnp:class><desc id="cdudn" nameSpace="urn:schemas-rinconnetworks-com:metadata-1-0/">SA_RINCON` + service + `_X_#Svc` + service + `-0-Token</desc></item></DIDL-Lite>`
		response, err = c.action(ctx, address, avTransportPath, avTransport, "AddURIToQueue", map[string]string{"InstanceID": "0", "EnqueuedURI": spec.prefix + spec.encoded, "EnqueuedURIMetaData": metadata, "DesiredFirstTrackNumberEnqueued": "0", "EnqueueAsNext": "0"})
		if err == nil {
			break
		}
		var fault *UPnPFault
		if !errors.As(err, &fault) || service == "3079" {
			return 0, "", "", err
		}
	}
	position, err := strconv.Atoi(response["FirstTrackNumberEnqueued"])
	if err != nil || position < 1 {
		return 0, response["NumTracksAdded"], response["NewQueueLength"], fmt.Errorf("invalid Sonos enqueue position %q", response["FirstTrackNumberEnqueued"])
	}
	return position, response["NumTracksAdded"], response["NewQueueLength"], nil
}

type UPnPFault struct {
	Action            string
	HTTPStatus        int
	Code, Description string
}

func (e *UPnPFault) Error() string {
	return fmt.Sprintf("Sonos %s failed (HTTP %d): %s %s", e.Action, e.HTTPStatus, e.Code, e.Description)
}

type shareSpec struct{ prefix, key, class, service, encoded string }

func spotifyShare(uri string) (shareSpec, error) {
	p := strings.Split(uri, ":")
	if len(p) != 3 || p[0] != "spotify" || !strings.Contains("|playlist|album|track|episode|show|", "|"+p[1]+"|") || len(p[2]) == 0 {
		return shareSpec{}, fmt.Errorf("invalid Spotify URI")
	}
	for _, r := range p[2] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return shareSpec{}, fmt.Errorf("invalid Spotify URI")
		}
	}
	s := shareSpec{encoded: strings.ReplaceAll(uri, ":", "%3a"), service: "2311"}
	switch p[1] {
	case "playlist", "show":
		s.prefix = "x-rincon-cpcontainer:1006206c"
		s.key = "1006206c"
		s.class = "object.container.playlistContainer"
	case "album":
		s.prefix = "x-rincon-cpcontainer:1004206c"
		s.key = "00040000"
		s.class = "object.container.album.musicAlbum"
	case "episode", "track":
		s.key = "00032020"
		s.class = "object.item.audioItem.musicTrack"
	}
	return s, nil
}
func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (c *Client) action(ctx context.Context, address, path, service, action string, args map[string]string) (map[string]string, error) {
	argumentOrder, ok := sonosActionArguments[service+"#"+action]
	if !ok || len(argumentOrder) != len(args) {
		return nil, fmt.Errorf("unsupported Sonos action arguments for %s", action)
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + service + `">`)
	for _, k := range argumentOrder {
		value, exists := args[k]
		if !exists {
			return nil, fmt.Errorf("missing Sonos %s argument %s", action, k)
		}
		body.WriteString("<" + k + ">" + xmlEscape(value) + "</" + k + ">")
	}
	body.WriteString("</u:" + action + "></s:Body></s:Envelope>")
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+":1400"+path, strings.NewReader(body.String()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
	req.Header.Set("SOAPACTION", `"`+service+"#"+action+`"`)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, e := c.httpClient().Do(req)
	if e != nil {
		return nil, fmt.Errorf("Sonos %s: %w", action, e)
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if e != nil {
		return nil, e
	}
	var env struct {
		Body struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"Body"`
	}
	if e = xml.Unmarshal(data, &env); e != nil {
		return nil, fmt.Errorf("Sonos %s response: %w", action, e)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var fault struct {
			Code        string `xml:"detail>UPnPError>errorCode"`
			Description string `xml:"detail>UPnPError>errorDescription"`
		}
		_ = xml.Unmarshal(env.Body.Inner, &fault)
		if fault.Code == "" {
			return nil, fmt.Errorf("Sonos %s failed with HTTP %d without a UPnP fault", action, resp.StatusCode)
		}
		return nil, &UPnPFault{Action: action, HTTPStatus: resp.StatusCode, Code: fault.Code, Description: fault.Description}
	}
	result := make(map[string]string)
	d := xml.NewDecoder(bytes.NewReader(env.Body.Inner))
	depth := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch token := tok.(type) {
		case xml.StartElement:
			if depth == 1 {
				var value string
				if e = d.DecodeElement(&value, &token); e != nil {
					return nil, e
				}
				result[token.Name.Local] = value
			} else {
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return result, nil
}
