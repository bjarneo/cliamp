// player/reserve_linux.go — temporary exclusive access to a bit-perfect ALSA
// device via the freedesktop D-Bus audio device reservation protocol.
//
// A sound server (PipeWire/WirePlumber, historically PulseAudio) normally
// holds a real hardware device open continuously as its own driver node, so
// a direct ALSA open of the same device fails with "busy" even though the
// server has nothing playing through it. The reservation protocol lets an
// app ask the current holder to yield the device for the duration of its own
// playback, then hands it back automatically on release — the Linux
// equivalent of macOS Core Audio's exclusive/hog mode, without permanently
// removing the device from the sound server's control.
//
// Protocol shape (verified live against WirePlumber for this feature):
// bus name "org.freedesktop.ReserveDevice1.Audio{cardIndex}", object path
// "/org/freedesktop/ReserveDevice1/Audio{cardIndex}", interface
// "org.freedesktop.ReserveDevice1", method RequestRelease(i priority) -> b.
// This is entirely best-effort: acquiring a reservation can fail for
// ordinary reasons (no session bus, no reservation-aware sound server, the
// holder declines), and every failure just falls through to the existing
// direct-open attempt and its existing fallback chain.

//go:build linux && cgo

package player

/*
#cgo pkg-config: alsa
#include <alsa/asoundlib.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"

	"github.com/bjarneo/cliamp/applog"
)

const (
	reserveBusNameFmt     = "org.freedesktop.ReserveDevice1.Audio%d"
	reserveObjectPathFmt  = "/org/freedesktop/ReserveDevice1/Audio%d"
	reserveInterface      = "org.freedesktop.ReserveDevice1"
	reservePriority       = int32(10) // comfortably above WirePlumber's own (-20)
	reserveReleaseTimeout = 2 * time.Second
	reserveRetryBudget    = 2 * time.Second
	reserveRetryInterval  = 100 * time.Millisecond
)

// parseALSACardToken extracts the card portion of an ALSA device string
// ("hw:2,0" -> "2", "hw:K6,0" -> "K6", "hw:CARD=PCH,DEV=0" -> "PCH"). It
// returns ok=false for anything that isn't a direct hw:/plughw: device.
func parseALSACardToken(device string) (token string, ok bool) {
	rest, found := strings.CutPrefix(device, "hw:")
	if !found {
		rest, found = strings.CutPrefix(device, "plughw:")
	}
	if !found {
		return "", false
	}
	field, _, _ := strings.Cut(rest, ",")
	field = strings.TrimPrefix(field, "CARD=")
	if field == "" {
		return "", false
	}
	return field, true
}

// reserveBusName and reserveObjectPath format the well-known name and object
// path for a card's reservation, per the protocol confirmed above.
func reserveBusName(cardIdx int) string { return fmt.Sprintf(reserveBusNameFmt, cardIdx) }

func reserveObjectPath(cardIdx int) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf(reserveObjectPathFmt, cardIdx))
}

// alsaCardIndex resolves a device string's card portion to a numeric ALSA
// card index. A numeric token ("hw:2,0") is used directly; a name token
// ("hw:K6,0") is resolved via ALSA's own card enumeration.
func alsaCardIndex(device string) (idx int, ok bool) {
	token, found := parseALSACardToken(device)
	if !found {
		return 0, false
	}
	if n, err := strconv.Atoi(token); err == nil {
		return n, true
	}

	cToken := C.CString(token)
	defer C.free(unsafe.Pointer(cToken))
	n := C.snd_card_get_index(cToken)
	if n < 0 {
		return 0, false
	}
	return int(n), true
}

// deviceReservation holds a temporarily-acquired reservation. A nil pointer
// is a valid, inert value so callers never need a separate "did we get one"
// check before releasing.
type deviceReservation struct {
	conn *dbus.Conn
	name string
}

// reserveHandler implements org.freedesktop.ReserveDevice1's RequestRelease
// method so a higher-priority application (or the sound server acting on its
// behalf) can ask cliamp to give the device back — the same thing
// acquireDeviceReservation itself does to whatever held the name before it.
// Without this exported, a reservation is a one-way lock for the rest of the
// process's life instead of the temporary, preemptable hold the protocol
// describes.
type reserveHandler struct {
	// onPreempt gives up the device and reports success. Only called for a
	// request from a strictly higher priority than ours (see RequestRelease);
	// cliamp's own reservePriority (10) is already "comfortably above
	// WirePlumber's own (-20)", so declining a lower-or-equal request keeps a
	// well-behaved competing client from bumping cliamp for no reason.
	onPreempt func(requestPriority int32) bool
}

func (h *reserveHandler) RequestRelease(priority int32) (bool, *dbus.Error) {
	if priority <= reservePriority {
		return false, nil
	}
	return h.onPreempt(priority), nil
}

// acquireDeviceReservation asks whatever currently holds cardIdx's
// reservation name to yield it, and returns a handle once acquired. onPreempt
// is called later, on its own goroutine (godbus dispatches every incoming
// method call via `go conn.handleCall(msg)`, so this never blocks the
// connection's read loop), if a higher-priority application asks for the
// device back the same way. It is best-effort throughout: any failure (no
// session bus, no cooperating holder, timeout) is an ordinary, expected
// outcome and is returned as a plain error for the caller to log and fall
// through, never as something fatal.
func acquireDeviceReservation(cardIdx int, onPreempt func(requestPriority int32) bool) (*deviceReservation, error) {
	name := reserveBusName(cardIdx)
	path := reserveObjectPath(cardIdx)

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("reserve: session bus: %w", err)
	}

	// Exported before RequestName below so there is no window where cliamp
	// holds the name but would fail to answer a RequestRelease sent the
	// moment it does.
	if err := conn.Export(&reserveHandler{onPreempt: onPreempt}, path, reserveInterface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reserve: export %s: %w", reserveInterface, err)
	}

	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reserve: request name: %w", err)
	}
	if reply == dbus.RequestNameReplyPrimaryOwner {
		return &deviceReservation{conn: conn, name: name}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), reserveReleaseTimeout)
	defer cancel()
	obj := conn.Object(name, reserveObjectPath(cardIdx))
	var released bool
	if err := obj.CallWithContext(ctx, reserveInterface+".RequestRelease", 0, reservePriority).Store(&released); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reserve: request release: %w", err)
	}
	if !released {
		conn.Close()
		return nil, fmt.Errorf("reserve: %s declined to release", name)
	}

	deadline := time.Now().Add(reserveRetryBudget)
	for {
		reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
		if err == nil && reply == dbus.RequestNameReplyPrimaryOwner {
			return &deviceReservation{conn: conn, name: name}, nil
		}
		if time.Now().After(deadline) {
			conn.Close()
			return nil, fmt.Errorf("reserve: %s did not become free in time", name)
		}
		time.Sleep(reserveRetryInterval)
	}
}

// release drops the reservation, if any. It is nil-safe so it can always be
// called unconditionally on cleanup.
func (r *deviceReservation) release() {
	if r == nil {
		return
	}
	if _, err := r.conn.ReleaseName(r.name); err != nil {
		applog.Debug("reserve: release %s: %v", r.name, err)
	}
	r.conn.Close()
}
