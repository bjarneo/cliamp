package resolve

import "testing"

func TestYouTubeVideoID(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"youtube watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"music watch", "https://music.youtube.com/watch?v=abc123XYZ_-", "abc123XYZ_-"},
		{"mobile host", "https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"short link", "https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"watch with list", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"soundcloud", "https://soundcloud.com/artist/track", ""},
		{"local file", "/home/user/Music/song.mp3", ""},
		{"channel url", "https://www.youtube.com/@somechannel", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := YouTubeVideoID(tc.in); got != tc.want {
				t.Errorf("YouTubeVideoID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRadioMixURL(t *testing.T) {
	got, ok := RadioMixURL("https://music.youtube.com/watch?v=dQw4w9WgXcQ")
	if !ok {
		t.Fatal("RadioMixURL: ok = false, want true")
	}
	want := "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ"
	if got != want {
		t.Errorf("RadioMixURL = %q, want %q", got, want)
	}
	if _, ok := RadioMixURL("https://soundcloud.com/a/b"); ok {
		t.Error("RadioMixURL(soundcloud): ok = true, want false")
	}
}
