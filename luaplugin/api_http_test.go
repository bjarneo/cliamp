package luaplugin

import (
	"slices"
	"testing"
)

func TestSSRFGuardAllowedPrivateAddrs(t *testing.T) {
	t.Cleanup(func() { allowedPrivateAddrs.Store(nil) })

	cases := []struct {
		address string
		allowed map[string]bool
		ok      bool
	}{
		{"1.1.1.1:443", nil, true},
		{"127.0.0.1:8085", nil, false},
		{"192.168.1.20:8096", nil, false},
		{"127.0.0.1:8085", map[string]bool{"127.0.0.1:8085": true}, true},
		// only that exact address and port
		{"127.0.0.1:8086", map[string]bool{"127.0.0.1:8085": true}, false},
		{"192.168.1.20:8096", map[string]bool{"127.0.0.1:8085": true}, false},
		{"[::1]:8085", map[string]bool{"[::1]:8085": true}, true},
	}
	for _, c := range cases {
		if c.allowed == nil {
			allowedPrivateAddrs.Store(nil)
		} else {
			allowedPrivateAddrs.Store(&c.allowed)
		}
		err := ssrfGuard("tcp", c.address, nil)
		if (err == nil) != c.ok {
			t.Errorf("ssrfGuard(%s) with %v: err = %v, want allowed = %v", c.address, c.allowed, err, c.ok)
		}
	}
}

func TestResolveAllowedPrivateAddrs(t *testing.T) {
	cfg := map[string]map[string]string{"": {
		"allowed_private_addresses": "127.0.0.1:8085, [::1]:9000, 10.0.0.2:08080, 0:0:0:0:0:0:0:1:7000, localhost:80, 10.0.0.1, ,bad, 10.0.0.3:notaport, 10.0.0.4:0, 10.0.0.5:70000",
	}}
	allowed, invalid := resolveAllowedPrivateAddrs(cfg)

	// a zero-padded port is stored the way the dialer reports it
	want := map[string]bool{"127.0.0.1:8085": true, "[::1]:9000": true, "10.0.0.2:8080": true}
	if len(allowed) != len(want) {
		t.Fatalf("allowed = %v, want %v", allowed, want)
	}
	for addr := range want {
		if !allowed[addr] {
			t.Errorf("allowed is missing %s: %v", addr, allowed)
		}
	}
	// a host name, a missing or out-of-range port and junk are reported, never allowed;
	// an unbracketed IPv6 address with a port is ambiguous and reported too
	wantInvalid := []string{"0:0:0:0:0:0:0:1:7000", "localhost:80", "10.0.0.1", "bad", "10.0.0.3:notaport", "10.0.0.4:0", "10.0.0.5:70000"}
	if !slices.Equal(invalid, wantInvalid) {
		t.Errorf("invalid = %v, want %v", invalid, wantInvalid)
	}

	if allowed, invalid := resolveAllowedPrivateAddrs(nil); len(allowed) != 0 || len(invalid) != 0 {
		t.Errorf("nil config: allowed = %v, invalid = %v, want both empty", allowed, invalid)
	}
}

func TestSSRFGuardSpecialRanges(t *testing.T) {
	t.Cleanup(func() { allowedPrivateAddrs.Store(nil) })
	allowedPrivateAddrs.Store(nil)

	tests := []struct {
		name    string
		address string
		ok      bool
	}{
		{"public IPv4", "1.1.1.1:443", true},
		{"public IPv6", "[2606:4700:4700::1111]:443", true},
		{"mapped public IPv4", "[::ffff:1.1.1.1]:443", true},
		{"CGNAT start", "100.64.0.1:80", false},
		{"CGNAT end", "100.127.255.254:443", false},
		{"mapped CGNAT", "[::ffff:100.64.0.1]:80", false},
		{"this network", "0.0.0.1:80", false},
		{"mapped this network", "[::ffff:0.0.0.1]:80", false},
		{"benchmarking", "198.18.0.1:80", false},
		{"benchmarking upper", "198.19.255.254:80", false},
		{"mapped benchmarking", "[::ffff:198.18.0.1]:80", false},
		{"reserved 240/4", "240.0.0.1:80", false},
		{"limited broadcast", "255.255.255.255:80", false},
		{"mapped reserved", "[::ffff:240.0.0.1]:80", false},
		{"NAT64 well-known", "[64:ff9b::1.2.3.4]:80", false},
		{"NAT64 local", "[64:ff9b:1::1]:80", false},
		{"deprecated IPv4-compatible", "[::1.2.3.4]:80", false},
		{"6to4", "[2002:0102:0304::1]:80", false},
		{"Teredo", "[2001:0000:4136:e378::1]:80", false},
		{"site-local fec0", "[fec0::1]:80", false},
		{"mapped loopback", "[::ffff:127.0.0.1]:80", false},
		{"mapped RFC1918", "[::ffff:10.0.0.1]:80", false},
		{"zoned link-local", "[fe80::1%eth0]:80", false},
		{"zoned public IPv6", "[2606:4700:4700::1111%eth0]:443", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ssrfGuard("tcp", tt.address, nil)
			if (err == nil) != tt.ok {
				t.Errorf("ssrfGuard(%q) err = %v, want ok = %v", tt.address, err, tt.ok)
			}
		})
	}

	// Mapped IPv4 matches an allowlisted IPv4 entry after Unmap.
	allowed := map[string]bool{"127.0.0.1:8085": true}
	allowedPrivateAddrs.Store(&allowed)
	if err := ssrfGuard("tcp", "[::ffff:127.0.0.1]:8085", nil); err != nil {
		t.Errorf("ssrfGuard mapped allowlisted IPv4 returned %v, want nil", err)
	}
}
