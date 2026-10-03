package netdiag

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

// dialErr builds the error chain http.Client.Do produces for a failed dial:
// *url.Error -> *net.OpError -> *os.SyscallError -> errno.
func dialErr(ip string, errno syscall.Errno) error {
	return &url.Error{
		Op:  "Get",
		URL: "http://" + ip + ":32400/library/sections",
		Err: &net.OpError{
			Op:   "dial",
			Net:  "tcp",
			Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 32400},
			Err:  os.NewSyscallError("connect", errno),
		},
	}
}

// wrappedDialErr builds the chain of an httpclient.Streaming request.
// dialWithDecision and the transport dial function each wrap the
// *net.OpError before http.Client.Do wraps it. A NewAPI client has no such
// wrapper and gives the chain of dialErr.
func wrappedDialErr(ip string, errno syscall.Errno) error {
	u := dialErr(ip, errno).(*url.Error)
	addr := net.JoinHostPort(ip, "32400")
	u.Err = fmt.Errorf("dial %s: %w", addr, fmt.Errorf("dial %s directly: %w", addr, u.Err))
	return u
}

func TestExplain(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		err      error
		wantHint bool
	}{
		{
			name:     "darwin private ip ehostunreach gets hint",
			goos:     "darwin",
			err:      dialErr("192.168.7.238", syscall.EHOSTUNREACH),
			wantHint: true,
		},
		{
			name:     "darwin private ip ehostunreach behind a dial wrapper gets hint",
			goos:     "darwin",
			err:      wrappedDialErr("192.168.7.238", syscall.EHOSTUNREACH),
			wantHint: true,
		},
		{
			name:     "darwin link-local ip ehostunreach gets hint",
			goos:     "darwin",
			err:      dialErr("169.254.10.5", syscall.EHOSTUNREACH),
			wantHint: true,
		},
		{
			name:     "linux private ip ehostunreach unchanged",
			goos:     "linux",
			err:      dialErr("192.168.7.238", syscall.EHOSTUNREACH),
			wantHint: false,
		},
		{
			name:     "darwin public ip ehostunreach unchanged",
			goos:     "darwin",
			err:      dialErr("93.184.216.34", syscall.EHOSTUNREACH),
			wantHint: false,
		},
		{
			name:     "darwin loopback ehostunreach unchanged",
			goos:     "darwin",
			err:      dialErr("127.0.0.1", syscall.EHOSTUNREACH),
			wantHint: false,
		},
		{
			name:     "darwin private ip connection refused unchanged",
			goos:     "darwin",
			err:      dialErr("192.168.7.238", syscall.ECONNREFUSED),
			wantHint: false,
		},
		{
			name:     "darwin non-dial error unchanged",
			goos:     "darwin",
			err:      errors.New("no route to host"),
			wantHint: false,
		},
		{
			name:     "nil error stays nil",
			goos:     "darwin",
			err:      nil,
			wantHint: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explain(tt.goos, tt.err)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("explain(nil) = %v, want nil", got)
				}
				return
			}
			hasHint := strings.Contains(got.Error(), "Local Network")
			if hasHint != tt.wantHint {
				t.Errorf("explain() hint = %v, want %v (err: %v)", hasHint, tt.wantHint, got)
			}
			if !errors.Is(got, tt.err) && got.Error() != tt.err.Error() {
				t.Errorf("explain() lost original error: %v", got)
			}
		})
	}
}
