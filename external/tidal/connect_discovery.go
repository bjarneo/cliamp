package tidal

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/player"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

const connectDevicePrefix = "tidal:"
const connectService = "_tidalconnect._tcp.local."

type connectDiscovery struct {
	names     map[string]string
	hosts     map[string]string
	ports     map[string]uint16
	addresses map[string]string
}

func newConnectDiscovery() *connectDiscovery {
	return &connectDiscovery{map[string]string{}, map[string]string{}, map[string]uint16{}, map[string]string{}}
}
func (d *connectDiscovery) consume(packet []byte) {
	var msg dnsmessage.Message
	if msg.Unpack(packet) != nil {
		return
	}
	records := append(append(msg.Answers, msg.Authorities...), msg.Additionals...)
	for _, r := range records {
		name := r.Header.Name.String()
		switch data := r.Body.(type) {
		case *dnsmessage.PTRResource:
			if name == connectService && d.names[data.PTR.String()] == "" {
				d.names[data.PTR.String()] = strings.TrimSuffix(data.PTR.String(), "."+connectService)
			}
		case *dnsmessage.SRVResource:
			if strings.HasSuffix(name, "."+connectService) {
				d.hosts[name] = data.Target.String()
				d.ports[name] = data.Port
			}
		case *dnsmessage.TXTResource:
			if strings.HasSuffix(name, "."+connectService) {
				for _, txt := range data.TXT {
					if fn, ok := strings.CutPrefix(txt, "fn="); ok {
						d.names[name] = fn
					}
				}
			}
		case *dnsmessage.AResource:
			d.addresses[name] = net.IP(data.A[:]).String()
		}
	}
}
func (d *connectDiscovery) devices() []player.AudioDevice {
	var out []player.AudioDevice
	for service, host := range d.hosts {
		addr := d.addresses[host]
		if addr == "" || d.ports[service] == 0 {
			continue
		}
		name := d.names[service]
		if name == "" {
			name = strings.TrimSuffix(service, "."+connectService)
		}
		out = append(out, player.AudioDevice{Name: connectDevicePrefix + net.JoinHostPort(addr, strconv.Itoa(int(d.ports[service]))), Description: "Tidal Connect: " + name})
	}
	slices.SortFunc(out, func(a, b player.AudioDevice) int { return strings.Compare(a.Description, b.Description) })
	return out
}

// discoverConnect sends a bounded mDNS query requesting unicast replies. It
// uses each multicast-capable IPv4 interface; no external discovery process.
func discoverConnect(ctx context.Context) ([]player.AudioDevice, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, fmt.Errorf("tidal connect: discovery: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	name, _ := dnsmessage.NewName(connectService)
	msg := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.Class(0x8001)}}}
	packet, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	multicast := ipv4.NewPacketConn(conn)
	_ = multicast.SetMulticastTTL(255)
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	sent := false
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if multicast.SetMulticastInterface(&iface) != nil {
			continue
		}
		if _, err = conn.WriteToUDP(packet, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}); err == nil {
			sent = true
		}
	}
	if !sent {
		return nil, nil
	}
	deadline := time.Now().Add(2 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetReadDeadline(deadline)
	d := newConnectDiscovery()
	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			break
		}
		d.consume(buf[:n])
	}
	return d.devices(), nil
}
