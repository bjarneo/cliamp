//go:build linux

package mediactl

import (
	"os"
	"strings"
	"testing"
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
