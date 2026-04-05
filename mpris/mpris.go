//go:build linux

// Package mpris exposes an MPRIS2 D-Bus service so that Linux desktop
// environments, hardware media keys, and tools like playerctl can
// control Cliamp.
package mpris

import (
	"fmt"
	"math"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"cliamp/internal/control"
)

// Shared message types (aliases so existing code using mpris.NextMsg still works).
type (
	PlayPauseMsg = control.ToggleMsg
	NextMsg      = control.NextMsg
	PrevMsg      = control.PrevMsg
	StopMsg      = control.StopMsg
)

// MPRIS-specific message types.
type (
	QuitMsg        struct{}
	SeekMsg        struct{ Offset int64 }   // microseconds (relative)
	SetPositionMsg struct{ Position int64 } // microseconds (absolute)
	SetVolumeMsg   struct{ Volume float64 } // linear 0.0–1.0
	InitMsg        struct{ Svc *Service }
)

// TrackInfo carries metadata for the currently playing track.
type TrackInfo struct {
	Title       string
	Artist      string
	Album       string
	Genre       string
	TrackNumber int
	URL         string
	Length      int64 // microseconds
}

// Service manages the MPRIS2 D-Bus presence.
type Service struct {
	conn  *dbus.Conn
	props *prop.Properties
	send  func(interface{})
	mu    sync.Mutex

	// Cached values from the last Update call.  We compare against
	// these instead of reading back from the prop library, because
	// D-Bus clients can write Volume between ticks, and reading the
	// prop value would cause Update to overwrite the D-Bus change
	// before the event loop handles it.
	lastStatus  string
	lastTrack   TrackInfo
	lastVol     float64
	lastCanSeek bool
}

// introspection XML for the two MPRIS interfaces.
const introspectXML = `
<node>
  <interface name="org.mpris.MediaPlayer2">
    <method name="Raise"/>
    <method name="Quit"/>
  </interface>
  <interface name="org.mpris.MediaPlayer2.Player">
    <method name="Next"/>
    <method name="Previous"/>
    <method name="Pause"/>
    <method name="PlayPause"/>
    <method name="Stop"/>
    <method name="Play"/>
    <method name="Seek"><arg direction="in" type="x"/></method>
    <method name="SetPosition"><arg direction="in" type="o"/><arg direction="in" type="x"/></method>
    <signal name="Seeked"><arg type="x"/></signal>
  </interface>
` + introspect.IntrospectDataString + `</node>`

// root implements org.mpris.MediaPlayer2 methods.
type root struct{ svc *Service }

func (r root) Raise() *dbus.Error { return nil }
func (r root) Quit() *dbus.Error {
	r.svc.send(QuitMsg{})
	return nil
}

// playerIface implements org.mpris.MediaPlayer2.Player methods.
type playerIface struct{ svc *Service }

func (p playerIface) Next() *dbus.Error {
	p.svc.send(NextMsg{})
	return nil
}

func (p playerIface) Previous() *dbus.Error {
	p.svc.send(PrevMsg{})
	return nil
}

func (p playerIface) Pause() *dbus.Error {
	p.svc.send(PlayPauseMsg{})
	return nil
}

func (p playerIface) PlayPause() *dbus.Error {
	p.svc.send(PlayPauseMsg{})
	return nil
}

func (p playerIface) Stop() *dbus.Error {
	p.svc.send(StopMsg{})
	return nil
}

func (p playerIface) Play() *dbus.Error {
	p.svc.send(PlayPauseMsg{})
	return nil
}

// DoSeek is exported to D-Bus as "Seek" via ExportWithMap (renamed to
// avoid go vet's stdmethods check which expects io.Seeker's signature).
func (p playerIface) DoSeek(offset int64) *dbus.Error {
	p.svc.send(SeekMsg{Offset: offset})
	return nil
}

func (p playerIface) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	p.svc.send(SetPositionMsg{Position: position})
	return nil
}

// New connects to the session D-Bus, claims the MPRIS bus name, and
// exports the two required interfaces. send is used to inject messages
// into the Bubbletea event loop (typically prog.Send).
func New(send func(interface{})) (*Service, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("mpris: session bus: %w", err)
	}

	reply, err := conn.RequestName("org.mpris.MediaPlayer2.cliamp",
		dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("mpris: name already taken")
	}

	svc := &Service{conn: conn, send: send}
	path := dbus.ObjectPath("/org/mpris/MediaPlayer2")

	// Export method handlers.
	if err := conn.Export(root{svc}, path, "org.mpris.MediaPlayer2"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: export root: %w", err)
	}
	if err := conn.ExportWithMap(playerIface{svc}, map[string]string{
		"DoSeek": "Seek",
	}, path, "org.mpris.MediaPlayer2.Player"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: export player: %w", err)
	}
	if err := conn.Export(introspect.Introspectable(introspectXML), path,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: export introspect: %w", err)
	}

	// Export properties for both interfaces.
	propsSpec := map[string]map[string]*prop.Prop{
		"org.mpris.MediaPlayer2": {
			"Identity":     {Value: "Cliamp", Writable: false, Emit: prop.EmitTrue},
			"CanQuit":      {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanRaise":     {Value: false, Writable: false, Emit: prop.EmitTrue},
			"HasTrackList": {Value: false, Writable: false, Emit: prop.EmitTrue},
		},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: "Stopped", Writable: false, Emit: prop.EmitTrue},
			"Metadata":       {Value: makeMetadata(TrackInfo{}), Writable: false, Emit: prop.EmitTrue},
			"Volume": {Value: 1.0, Writable: true, Emit: prop.EmitTrue, Callback: func(c *prop.Change) *dbus.Error {
				v, ok := c.Value.(float64)
				if !ok {
					return nil
				}
				if v < 0 {
					v = 0
				}
				if v > 1 {
					v = 1
				}
				// Must use a goroutine: this callback runs inside
				// Properties.Set which holds p.mut.  prog.Send blocks
				// on an unbuffered channel until the event loop reads,
				// but the event loop may be in Update→SetMust waiting
				// for p.mut — deadlock without the goroutine.
				go svc.send(SetVolumeMsg{Volume: v})
				return nil
			}},
			"Position":      {Value: int64(0), Writable: false, Emit: prop.EmitFalse},
			"Rate":          {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"MinimumRate":   {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"MaximumRate":   {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"CanControl":    {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPlay":       {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPause":      {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoNext":     {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoPrevious": {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanSeek":       {Value: true, Writable: false, Emit: prop.EmitTrue},
		},
	}

	props, err := prop.Export(conn, path, propsSpec)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: export props: %w", err)
	}
	svc.props = props

	return svc, nil
}

// Update refreshes MPRIS properties when playback state changes.
// status is "Playing", "Paused", or "Stopped". volumeDB is the
// current volume in decibels (range [-30, +6]). positionUs is
// the current playback position in microseconds. canSeek indicates
// whether the current track supports seeking.
//
// We use SetMust (not Set) because Set rejects writes on read-only
// properties and triggers callbacks on writable ones — both wrong
// for internal updates.
func (s *Service) Update(status string, track TrackInfo, volumeDB float64, positionUs int64, canSeek bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.props == nil {
		return
	}

	iface := "org.mpris.MediaPlayer2.Player"

	if status != s.lastStatus {
		s.props.SetMust(iface, "PlaybackStatus", status)
		s.lastStatus = status
	}

	if track != s.lastTrack {
		s.props.SetMust(iface, "Metadata", makeMetadata(track))
		s.lastTrack = track
	}

	vol := dbToLinear(volumeDB)
	if vol != s.lastVol {
		s.props.SetMust(iface, "Volume", vol)
		s.lastVol = vol
	}

	// Position uses EmitFalse — update silently (clients poll or use Seeked signal).
	s.props.SetMust(iface, "Position", positionUs)

	if canSeek != s.lastCanSeek {
		s.props.SetMust(iface, "CanSeek", canSeek)
		s.lastCanSeek = canSeek
	}
}

// EmitSeeked sends the org.mpris.MediaPlayer2.Player.Seeked signal
// with the absolute position in microseconds. Call after any seek
// operation (D-Bus or keyboard) so desktop widgets can snap to the
// new position.
func (s *Service) EmitSeeked(positionUs int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return
	}
	s.conn.Emit(
		dbus.ObjectPath("/org/mpris/MediaPlayer2"),
		"org.mpris.MediaPlayer2.Player.Seeked",
		positionUs,
	)
}

// Close releases the D-Bus connection.
func (s *Service) Close() {
	if s == nil {
		return
	}
	if s.conn != nil {
		s.conn.Close()
	}
}

// makeMetadata builds an MPRIS metadata map from TrackInfo.
func makeMetadata(t TrackInfo) map[string]dbus.Variant {
	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
	}
	if t.Title != "" {
		m["xesam:title"] = dbus.MakeVariant(t.Title)
	}
	if t.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{t.Artist})
	}
	if t.Album != "" {
		m["xesam:album"] = dbus.MakeVariant(t.Album)
	}
	if t.Genre != "" {
		m["xesam:genre"] = dbus.MakeVariant([]string{t.Genre})
	}
	if t.TrackNumber > 0 {
		m["xesam:trackNumber"] = dbus.MakeVariant(t.TrackNumber)
	}
	if t.URL != "" {
		m["xesam:url"] = dbus.MakeVariant(t.URL)
	}
	if t.Length > 0 {
		m["mpris:length"] = dbus.MakeVariant(t.Length)
	}
	return m
}

// dbToLinear converts a dB volume (range [-30, +6]) to a 0.0–1.0 linear scale.
func dbToLinear(db float64) float64 {
	// Map -30 dB → 0.0, 0 dB → ~0.83, +6 dB → 1.0
	if db <= -30 {
		return 0.0
	}
	if db >= 6 {
		return 1.0
	}
	return math.Pow(10, db/20) / math.Pow(10, 6.0/20)
}

// LinearToDb converts a 0.0–1.0 linear volume to dB (range [-30, +6]).
// This is the inverse of dbToLinear.
func LinearToDb(v float64) float64 {
	if v <= 0 {
		return -30
	}
	if v >= 1 {
		return 6
	}
	db := 20*math.Log10(v) + 6
	if db < -30 {
		return -30
	}
	return db
}
