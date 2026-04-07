//go:build linux

package mediactl

import (
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"cliamp/internal/playback"
)

func Run(prog *tea.Program, svc *Service) (tea.Model, error) {
	return prog.Run()
}

type Service struct {
	conn  *dbus.Conn
	props *prop.Properties
	send  func(tea.Msg)
	mu    sync.Mutex

	lastStatus  playback.Status
	lastTrack   playback.Track
	lastVol     float64
	lastCanSeek bool
}

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

type root struct{ svc *Service }

func (r root) Raise() *dbus.Error { return nil }
func (r root) Quit() *dbus.Error {
	r.svc.send(playback.QuitMsg{})
	return nil
}

type playerIface struct{ svc *Service }

func (p playerIface) Next() *dbus.Error {
	p.svc.send(playback.NextMsg{})
	return nil
}

func (p playerIface) Previous() *dbus.Error {
	p.svc.send(playback.PrevMsg{})
	return nil
}

func (p playerIface) Pause() *dbus.Error {
	p.svc.send(playback.PauseMsg{})
	return nil
}

func (p playerIface) PlayPause() *dbus.Error {
	p.svc.send(playback.PlayPauseMsg{})
	return nil
}

func (p playerIface) Stop() *dbus.Error {
	p.svc.send(playback.StopMsg{})
	return nil
}

func (p playerIface) Play() *dbus.Error {
	p.svc.send(playback.PlayMsg{})
	return nil
}

func (p playerIface) DoSeek(offset int64) *dbus.Error {
	p.svc.send(playback.SeekMsg{Offset: time.Duration(offset) * time.Microsecond})
	return nil
}

func (p playerIface) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	p.svc.send(playback.SetPositionMsg{Position: time.Duration(position) * time.Microsecond})
	return nil
}

func New(send func(tea.Msg)) (*Service, error) {
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

	propsSpec := map[string]map[string]*prop.Prop{
		"org.mpris.MediaPlayer2": {
			"Identity":     {Value: "Cliamp", Writable: false, Emit: prop.EmitTrue},
			"CanQuit":      {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanRaise":     {Value: false, Writable: false, Emit: prop.EmitTrue},
			"HasTrackList": {Value: false, Writable: false, Emit: prop.EmitTrue},
		},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: string(playback.StatusStopped), Writable: false, Emit: prop.EmitTrue},
			"Metadata":       {Value: makeMetadata(playback.Track{}), Writable: false, Emit: prop.EmitTrue},
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
				go svc.send(playback.SetVolumeMsg{VolumeDB: linearToDb(v)})
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

func (s *Service) Update(state playback.State) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.props == nil {
		return
	}

	iface := "org.mpris.MediaPlayer2.Player"

	if state.Status != s.lastStatus {
		s.props.SetMust(iface, "PlaybackStatus", string(state.Status))
		s.lastStatus = state.Status
	}

	if state.Track != s.lastTrack {
		s.props.SetMust(iface, "Metadata", makeMetadata(state.Track))
		s.lastTrack = state.Track
	}

	vol := dbToLinear(state.VolumeDB)
	if vol != s.lastVol {
		s.props.SetMust(iface, "Volume", vol)
		s.lastVol = vol
	}

	s.props.SetMust(iface, "Position", state.Position.Microseconds())

	if state.Seekable != s.lastCanSeek {
		s.props.SetMust(iface, "CanSeek", state.Seekable)
		s.lastCanSeek = state.Seekable
	}
}

func (s *Service) Seeked(position time.Duration) {
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
		position.Microseconds(),
	)
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	if s.conn != nil {
		s.conn.Close()
	}
}
