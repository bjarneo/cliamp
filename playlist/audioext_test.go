package playlist

import (
	"slices"
	"testing"
)

func TestIsAudioFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/music/song.mp3", true},
		{"/music/SONG.FLAC", true},
		{"relative/track.ogg", true},
		{"track.wav", true},
		{"/podcasts/episode.m4b", true},
		{"stream.aacp", true},
		{"/music/clip.webm", true},
		{"/music/a.b/song.opus", true},
		{"/music/cover.jpg", false},
		{"/music/playlist.m3u", false},
		{"/music/playlist.pls", false},
		{"/music/noext", false},
		{"/music/song.mp3/", false},
		{".mp3", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsAudioFile(tt.path); got != tt.want {
				t.Errorf("IsAudioFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestAudioExtensions(t *testing.T) {
	got := AudioExtensions()
	if !slices.IsSorted(got) {
		t.Fatalf("AudioExtensions() = %v, want sorted", got)
	}
	for _, ext := range got {
		if !IsAudioFile("track" + ext) {
			t.Errorf("IsAudioFile(%q) = false for a listed extension", "track"+ext)
		}
	}
	if !slices.Contains(got, ".aacp") {
		t.Errorf("AudioExtensions() = %v, want .aacp", got)
	}

	// The result is a copy, so a caller cannot change the set.
	got[0] = ".txt"
	if IsAudioFile("notes.txt") {
		t.Fatal("a change to the returned slice changed the audio set")
	}
	if again := AudioExtensions(); again[0] == ".txt" {
		t.Fatalf("AudioExtensions() = %v after a caller edit, want a fresh copy", again)
	}
}
