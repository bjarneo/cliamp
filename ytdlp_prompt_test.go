package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The yt-dlp install prompt installs after a bare Enter, y or yes in an
// interactive start. A daemon or a start with stdin from a pipe or a file
// never asks. EOF, as from /dev/null, skips the install. A skip at the prompt
// says that the YouTube providers are disabled.
func TestOfferYTDLPInstall(t *testing.T) {
	const skipped = "Skipped. YouTube providers are disabled."
	for _, tc := range []struct {
		name        string
		interactive bool
		input       string
		want        bool
	}{
		{name: "enter", interactive: true, input: "\n", want: true},
		{name: "enter with carriage return", interactive: true, input: "\r\n", want: true},
		{name: "answer y", interactive: true, input: "y\n", want: true},
		{name: "answer yes", interactive: true, input: "yes\n", want: true},
		{name: "answer Yes with spaces", interactive: true, input: " Yes \r\n", want: true},
		{name: "answer Y", interactive: true, input: "Y\n", want: true},
		{name: "answer n", interactive: true, input: "n\n"},
		{name: "answer no", interactive: true, input: "no\n"},
		{name: "eof", interactive: true, input: ""},
		{name: "text without newline", interactive: true, input: "y"},
		{name: "not interactive", input: "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := offerYTDLPInstall(tc.interactive, strings.NewReader(tc.input), &out); got != tc.want {
				t.Fatalf("offerYTDLPInstall() = %v, want %v", got, tc.want)
			}
			if asked := out.Len() > 0; asked != tc.interactive {
				t.Fatalf("prompt written = %v, want %v", asked, tc.interactive)
			}
			wantSkipped := tc.interactive && !tc.want
			if got := strings.Contains(out.String(), skipped); got != wantSkipped {
				t.Fatalf("output %q: skip line = %v, want %v", out.String(), got, wantSkipped)
			}
		})
	}
}

// Only a character device on stdin makes the start interactive. The null
// device is a character device too, so cliamp asks. The EOF that follows
// skips the install.
func TestIsCharDevice(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pipe, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	w.Close()

	for _, tc := range []struct {
		name string
		f    *os.File
		want bool
	}{
		{name: "null device", f: null, want: true},
		{name: "regular file", f: file},
		{name: "pipe", f: pipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interactive := isCharDevice(tc.f)
			if interactive != tc.want {
				t.Fatalf("isCharDevice() = %v, want %v", interactive, tc.want)
			}
			var out bytes.Buffer
			if offerYTDLPInstall(interactive, tc.f, &out) {
				t.Fatalf("offerYTDLPInstall() = true at EOF, output %q", out.String())
			}
		})
	}
}
