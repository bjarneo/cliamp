package ytdlcookies

import (
	"slices"
	"testing"
)

func TestForURLSelectsCookiesByHost(t *testing.T) {
	SetForHost("mixcloud.com", "firefox")
	SetForHost("music.163.com", "chrome")
	t.Cleanup(func() {
		SetForHost("mixcloud.com", "")
		SetForHost("music.163.com", "")
	})

	tests := []struct {
		url  string
		want string
	}{
		{url: "https://www.mixcloud.com/creator/show/", want: "firefox"},
		{url: "https://music.163.com/#/song?id=1", want: "chrome"},
		{url: "https://example.com/track", want: ""},
	}
	for _, test := range tests {
		if got := ForURL(test.url); got != test.want {
			t.Errorf("ForURL(%q) = %q, want %q", test.url, got, test.want)
		}
	}
}

func TestForURLMapsYTDLSearchPrefixes(t *testing.T) {
	SetForHost("soundcloud.com", "firefox")
	SetForHost("youtube.com", "chrome")
	t.Cleanup(func() {
		SetForHost("soundcloud.com", "")
		SetForHost("youtube.com", "")
	})

	if got := ForURL("scsearch10:ambient"); got != "firefox" {
		t.Errorf("ForURL(scsearch) = %q, want firefox", got)
	}
	if got := ForURL("ytsearch10:ambient"); got != "chrome" {
		t.Errorf("ForURL(ytsearch) = %q, want chrome", got)
	}
}

func TestSetForHostEmptyBrowserRemovesSelection(t *testing.T) {
	SetForHost("mixcloud.com", "firefox")
	SetForHost("mixcloud.com", "")

	if got := ForURL("https://mixcloud.com/creator/show/"); got != "" {
		t.Errorf("ForURL() = %q after removal, want empty", got)
	}
}

func TestArgsPrefersCookiesFile(t *testing.T) {
	SetForHost("music.163.com", "chrome")
	SetForHost("mixcloud.com", "firefox")
	SetFileForHost("music.163.com", "/tmp/cookies.txt")
	t.Cleanup(func() {
		SetForHost("music.163.com", "")
		SetForHost("mixcloud.com", "")
		SetFileForHost("music.163.com", "")
	})

	tests := []struct {
		url  string
		want []string
	}{
		{url: "https://music.163.com/#/song?id=1", want: []string{"--cookies", "/tmp/cookies.txt"}},
		{url: "https://www.mixcloud.com/creator/show/", want: []string{"--cookies-from-browser", "firefox"}},
		{url: "https://example.com/track", want: nil},
	}
	for _, test := range tests {
		if got := Args(test.url); !slices.Equal(got, test.want) {
			t.Errorf("Args(%q) = %q, want %q", test.url, got, test.want)
		}
	}
}
