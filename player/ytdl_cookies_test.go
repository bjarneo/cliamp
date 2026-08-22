package player

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

func TestAppendYTDLCookieArgsSelectsBrowserByURLHost(t *testing.T) {
	ytdlcookies.SetForHost("mixcloud.com", "firefox")
	ytdlcookies.SetForHost("music.163.com", "chrome")
	t.Cleanup(func() {
		ytdlcookies.SetForHost("mixcloud.com", "")
		ytdlcookies.SetForHost("music.163.com", "")
	})

	tests := []struct {
		url  string
		want []string
	}{
		{
			url:  "https://www.mixcloud.com/creator/show/",
			want: []string{"yt-dlp", "--cookies-from-browser", "firefox"},
		},
		{
			url:  "https://music.163.com/#/song?id=1",
			want: []string{"yt-dlp", "--cookies-from-browser", "chrome"},
		},
		{
			url:  "https://example.com/track",
			want: []string{"yt-dlp"},
		},
	}

	for _, test := range tests {
		got := appendYTDLCookieArgs([]string{"yt-dlp"}, test.url)
		if !slices.Equal(got, test.want) {
			t.Errorf("appendYTDLCookieArgs(%q) = %v, want %v", test.url, got, test.want)
		}
	}
}
