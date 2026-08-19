package player

import "testing"

func TestParseChannelLayout(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		want    channelLayout
		wantErr bool
	}{
		{"empty is unconfigured", "", channelLayout{}, false},
		{"whitespace-only is unconfigured", "   ", channelLayout{}, false},
		{"plain stereo", "0,1", channelLayout{Configured: true, Left: 0, Right: 1}, false},
		{"reversed pair", "1,0", channelLayout{Configured: true, Left: 1, Right: 0}, false},
		{"a later pair on a multichannel device", "2,3", channelLayout{Configured: true, Left: 2, Right: 3}, false},
		{"spaces around values", " 0 , 1 ", channelLayout{Configured: true, Left: 0, Right: 1}, false},
		{"missing comma", "01", channelLayout{}, true},
		{"non-numeric left", "a,1", channelLayout{}, true},
		{"non-numeric right", "0,b", channelLayout{}, true},
		{"negative left", "-1,1", channelLayout{}, true},
		{"negative right", "0,-1", channelLayout{}, true},
		{"same channel twice", "1,1", channelLayout{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChannelLayout(tt.s)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseChannelLayout(%q) error = %v, wantErr %v", tt.s, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseChannelLayout(%q) = %+v, want %+v", tt.s, got, tt.want)
			}
		})
	}
}

func TestChannelLayoutTotal(t *testing.T) {
	tests := []struct {
		layout channelLayout
		want   int
	}{
		{channelLayout{Configured: true, Left: 0, Right: 1}, 2},
		{channelLayout{Configured: true, Left: 1, Right: 0}, 2},
		{channelLayout{Configured: true, Left: 2, Right: 3}, 4},
		{channelLayout{Configured: true, Left: 0, Right: 5}, 6},
	}
	for _, tt := range tests {
		if got := tt.layout.total(); got != tt.want {
			t.Errorf("%+v.total() = %d, want %d", tt.layout, got, tt.want)
		}
	}
}

func TestValidateChannels(t *testing.T) {
	if err := ValidateChannels("0,1"); err != nil {
		t.Errorf("ValidateChannels(\"0,1\") = %v, want nil", err)
	}
	if err := ValidateChannels(""); err != nil {
		t.Errorf("ValidateChannels(\"\") = %v, want nil", err)
	}
	if err := ValidateChannels("nope"); err == nil {
		t.Error("ValidateChannels(\"nope\") = nil, want an error")
	}
}
