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
			name:    "wrong scheme",
			input:   "http://myhost/path",
			wantErr: true,
		},
		{
			name:    "missing path",
			input:   "ssh://myhost",
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
			want:   []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "myhost"},
		},
		{
			name:   "with port",
			parsed: Parsed{Host: "user@myhost", Port: "2222", Path: "/music"},
			want:   []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "-p", "2222", "user@myhost"},
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
