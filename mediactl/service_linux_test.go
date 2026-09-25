//go:build linux

package mediactl

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestInstanceNameFollowsTheSpecification(t *testing.T) {
	got := instanceName(4242)
	want := "org.mpris.MediaPlayer2.cliamp.instance4242"
	if got != want {
		t.Fatalf("instanceName(4242) = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, busName+".") {
		t.Fatalf("%q is not below the well known name %q", got, busName)
	}
}

func TestInstanceNameIsUniquePerProcess(t *testing.T) {
	// Two cliamps on one bus have to ask for two different names, or the
	// second one is left without media controls again.
	if instanceName(os.Getpid()) == instanceName(os.Getpid()+1) {
		t.Fatal("two processes would ask for the same name")
	}
}

// fakeBus answers RequestName from a table and records what was asked for.
type fakeBus struct {
	replies map[string]dbus.RequestNameReply
	err     error
	asked   []string
}

func (f *fakeBus) RequestName(name string, _ dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
	f.asked = append(f.asked, name)
	if f.err != nil {
		return 0, f.err
	}
	reply, ok := f.replies[name]
	if !ok {
		return dbus.RequestNameReplyExists, nil
	}
	return reply, nil
}

func TestRequestNameTakesTheWellKnownNameWhenItIsFree(t *testing.T) {
	bus := &fakeBus{replies: map[string]dbus.RequestNameReply{
		busName: dbus.RequestNameReplyPrimaryOwner,
	}}
	got, err := requestName(bus)
	if err != nil {
		t.Fatalf("requestName: %v", err)
	}
	if got != busName {
		t.Fatalf("got %q, want %q", got, busName)
	}
	if len(bus.asked) != 1 {
		t.Fatalf("asked for %v, want only the well known name", bus.asked)
	}
}

func TestRequestNameFallsBackWhenAnotherCliampHoldsIt(t *testing.T) {
	mine := instanceName(os.Getpid())
	bus := &fakeBus{replies: map[string]dbus.RequestNameReply{
		busName: dbus.RequestNameReplyExists,
		mine:    dbus.RequestNameReplyPrimaryOwner,
	}}
	got, err := requestName(bus)
	if err != nil {
		t.Fatalf("requestName: %v", err)
	}
	if got != mine {
		t.Fatalf("got %q, want the instance name %q", got, mine)
	}
	want := []string{busName, mine}
	if len(bus.asked) != 2 || bus.asked[0] != want[0] || bus.asked[1] != want[1] {
		t.Fatalf("asked for %v, want %v in that order", bus.asked, want)
	}
}

func TestRequestNameGivesUpWhenNeitherNameIsFree(t *testing.T) {
	bus := &fakeBus{replies: map[string]dbus.RequestNameReply{}}
	if _, err := requestName(bus); err == nil {
		t.Fatal("both names taken should be an error")
	}
}

func TestRequestNameReportsABusError(t *testing.T) {
	boom := errors.New("no bus")
	bus := &fakeBus{err: boom}
	_, err := requestName(bus)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want it to wrap %v", err, boom)
	}
	if len(bus.asked) != 1 {
		t.Fatalf("asked for %v, want it to stop at the first error", bus.asked)
	}
}

// The concrete connection has to keep satisfying the interface the fallback
// takes, or New would stop compiling for a reason a test should catch first.
var _ nameClaimer = (*dbus.Conn)(nil)
