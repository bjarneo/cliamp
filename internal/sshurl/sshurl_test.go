package sshurl

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHost string
		wantPort string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "basic",
			input:    "ssh://myhost/path/to/music",
			wantHost: "myhost",
			wantPort: "",
			wantPath: "/path/to/music",
		},
		{
			name:     "with user",
			input:    "ssh://user@myhost/path/to/music",
			wantHost: "user@myhost",
			wantPort: "",
			wantPath: "/path/to/music",
		},
		{
			name:     "with port",
			input:    "ssh://myhost:2222/path/to/music",
			wantHost: "myhost",
			wantPort: "2222",
			wantPath: "/path/to/music",
		},
		{
			name:     "with user and port",
			input:    "ssh://user@myhost:2222/path/to/music",
			wantHost: "user@myhost",
			wantPort: "2222",
			wantPath: "/path/to/music",
		},
		{
			name:     "ipv6 host",
			input:    "ssh://[::1]:2222/path/to/music",
			wantHost: "::1",
			wantPort: "2222",
			wantPath: "/path/to/music",
		},
		{
			name:    "empty host",
			input:   "ssh:///path/to/music",
			wantErr: true,
		},
		{
			name:    "empty host with user",
			input:   "ssh://user@/path/to/music",
			wantErr: true,
		},
		{
			name:    "empty username",
			input:   "ssh://@myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "wrong scheme",
			input:   "http://myhost/path",
			wantErr: true,
		},
		{
			name:    "missing path",
			input:   "ssh://myhost",
			wantErr: true,
		},
		{
			name:    "host with ssh option injection is rejected",
			input:   "ssh://-oProxyCommand=command/path/to/music",
			wantErr: true,
		},
		{
			name:    "leading dash hostname after user is rejected",
			input:   "ssh://user@-myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "leading dash username is rejected",
			input:   "ssh://-user@myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "host containing equals is rejected",
			input:   "ssh://ho=st/path/to/music",
			wantErr: true,
		},
		{
			name:    "username with shell metachar is rejected",
			input:   "ssh://user%3Bid@myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "username with whitespace is rejected",
			input:   "ssh://a%20b@myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "username with control char is rejected",
			input:   "ssh://a%0Ab@myhost/path/to/music",
			wantErr: true,
		},
		{
			name:    "hostname with shell metachar is rejected",
			input:   "ssh://my$host/path/to/music",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if parsed.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", parsed.Host, tt.wantHost)
			}
			if parsed.Port != tt.wantPort {
				t.Errorf("Port = %q, want %q", parsed.Port, tt.wantPort)
			}
			if parsed.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", parsed.Path, tt.wantPath)
			}
		})
	}
}

func TestSSHArgs(t *testing.T) {
	tests := []struct {
		name   string
		parsed Parsed
		want   []string
	}{
		{
			name:   "no port",
			parsed: Parsed{Host: "myhost", Port: "", Path: "/music"},
			want:   []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "--", "myhost"},
		},
		{
			name:   "with port",
			parsed: Parsed{Host: "user@myhost", Port: "2222", Path: "/music"},
			want:   []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "-p", "2222", "--", "user@myhost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.parsed.SSHArgs()
			if len(got) != len(tt.want) {
				t.Fatalf("SSHArgs() = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SSHArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
