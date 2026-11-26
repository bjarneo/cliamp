package ytdlcookies

import (
	"os"
	"path/filepath"
	"runtime"
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
	if got := ForURL("https://on.soundcloud.com/QSWjV6ABJR5SICzGB3"); got != "firefox" {
		t.Errorf("ForURL(on.soundcloud.com) = %q, want firefox", got)
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
		{url: "https://www.mixcloud.com/creator/show/", want: []string{"--cookies-from-browser", "firefox"}},
		{url: "https://example.com/track", want: nil},
	}
	for _, test := range tests {
		got, cleanup, err := Args(test.url)
		cleanup()
		if err != nil || !slices.Equal(got, test.want) {
			t.Errorf("Args(%q) = %q, %v; want %q", test.url, got, err, test.want)
		}
	}
}

func TestArgsPassesPrivateCopyOfCookiesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte("cookie data"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetFileForHost("music.163.com", path)
	t.Cleanup(func() { SetFileForHost("music.163.com", "") })

	got, cleanup, err := Args("https://music.163.com/#/song?id=1")
	if err != nil {
		t.Fatalf("Args() error = %v", err)
	}
	if len(got) != 2 || got[0] != "--cookies" || got[1] == path {
		t.Fatalf("Args() = %q, want --cookies and a copy of %q", got, path)
	}
	if data, err := os.ReadFile(got[1]); err != nil || string(data) != "cookie data" {
		t.Fatalf("copy = %q, %v; want the configured contents", data, err)
	}
	cleanup()
	if _, err := os.Stat(got[1]); !os.IsNotExist(err) {
		t.Errorf("copy %q still exists after cleanup: %v", got[1], err)
	}
}

func TestCopyToTemp(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
		badTmp  bool
		wantErr bool
	}{
		{name: "copies netscape cookies", content: "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t0\tsid\tval\n"},
		{name: "copies empty file", content: ""},
		{name: "fails on missing source", missing: true, wantErr: true},
		{name: "fails on missing temp dir", content: "secret", badTmp: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "cookies.txt")
			if !tt.missing {
				if err := os.WriteFile(src, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.badTmp {
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
			}

			dst, err := copyToTemp(src)
			if (err != nil) != tt.wantErr {
				t.Fatalf("copyToTemp() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if dst != "" {
					t.Errorf("copyToTemp() returned path %q on error", dst)
				}
				return
			}
			defer os.Remove(dst)

			if dst == src {
				t.Errorf("copyToTemp() returned source path %q", dst)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", dst, err)
			}
			if string(got) != tt.content {
				t.Errorf("copy content = %q, want %q", got, tt.content)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(dst)
				if err != nil {
					t.Fatalf("Stat(%q): %v", dst, err)
				}
				if perm := info.Mode().Perm(); perm != 0o600 {
					t.Errorf("temp file perm = %o, want 0600", perm)
				}
			}
		})
	}
}
