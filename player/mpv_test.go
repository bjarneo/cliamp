package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMPVCommandArgsBitPerfect(t *testing.T) {
	args := mpvCommandArgs(MPVOptions{
		AudioDevice: "alsa/hw:CARD=Generic,DEV=0",
		BitPerfect:  true,
	}, "/run/user/1000/cliamp/mpv/ipc.sock")
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--idle=yes",
		"--no-config",
		"--input-ipc-server=/run/user/1000/cliamp/mpv/ipc.sock",
		"--audio-device=alsa/hw:CARD=Generic,DEV=0",
		"--volume=100",
		"--volume-max=100",
		"--speed=1.0",
		"--af=",
		"--replaygain=no",
		"--audio-pitch-correction=no",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	for _, forbidden := range []string{"--audio-samplerate", "--audio-format", "S16_LE", "S24_LE"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("args force %q: %v", forbidden, args)
		}
	}
}

func TestMPVIPCMatchesResponsesByRequestID(t *testing.T) {
	client, server := net.Pipe()
	events := make(chan mpvMessage, 1)
	ipc := newMPVIPC(client, time.Second, func(message mpvMessage) { events <- message })
	defer ipc.fail(errMPVClosed)
	defer server.Close()

	type commandResult struct {
		name string
		data string
		err  error
	}
	results := make(chan commandResult, 2)
	go func() {
		data, err := ipc.command([]any{"first"})
		results <- commandResult{name: "first", data: string(data), err: err}
	}()

	scanner := bufio.NewScanner(server)
	if !scanner.Scan() {
		t.Fatal("first request not received")
	}
	var first mpvMessage
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	go func() {
		data, err := ipc.command([]any{"second"})
		results <- commandResult{name: "second", data: string(data), err: err}
	}()
	if !scanner.Scan() {
		t.Fatal("second request not received")
	}
	var second mpvMessage
	if err := json.Unmarshal(scanner.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if first.RequestID == nil || second.RequestID == nil || *first.RequestID == *second.RequestID {
		t.Fatalf("request IDs = %v, %v; want distinct integers", first.RequestID, second.RequestID)
	}
	if first.Command[0] != "first" || second.Command[0] != "second" {
		t.Fatalf("commands = %v, %v", first.Command, second.Command)
	}

	encoder := json.NewEncoder(server)
	if err := encoder.Encode(mpvMessage{Event: "property-change", Name: "pause", Data: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(mpvMessage{RequestID: second.RequestID, Error: "success", Data: json.RawMessage(`"two"`)}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(mpvMessage{RequestID: first.RequestID, Error: "success", Data: json.RawMessage(`"one"`)}); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("%s command: %v", result.name, result.err)
		}
		got[result.name] = result.data
	}
	if got["first"] != `"one"` || got["second"] != `"two"` {
		t.Fatalf("responses = %#v", got)
	}
	select {
	case event := <-events:
		if event.Name != "pause" || string(event.Data) != "true" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("asynchronous event not dispatched")
	}
}

func TestMPVIPCRequestTimeoutRemovesPending(t *testing.T) {
	client, server := net.Pipe()
	ipc := newMPVIPC(client, 20*time.Millisecond, nil)
	defer ipc.fail(errMPVClosed)
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ipc.command([]any{"unanswered"})
		done <- err
	}()
	if !bufio.NewScanner(server).Scan() {
		t.Fatal("request not received")
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("command error = %v", err)
	}
	ipc.mu.Lock()
	pending := len(ipc.pending)
	ipc.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending requests after timeout = %d", pending)
	}
}

func TestMPVEventStateTransitions(t *testing.T) {
	b := &MPVBackend{speed: 1, volumeMinDB: -50}
	b.handleIPCMessage(mpvMessage{Event: "file-loaded"})
	if !b.IsPlaying() || b.IsPaused() || b.Drained() {
		t.Fatalf("file-loaded state: playing=%v paused=%v drained=%v", b.IsPlaying(), b.IsPaused(), b.Drained())
	}
	b.handleProperty("duration", json.RawMessage(`120.5`))
	b.handleProperty("time-pos", json.RawMessage(`30.25`))
	b.handleProperty("audio-params", json.RawMessage(`{"format":"s32p","samplerate":48000,"hr-channels":"stereo"}`))
	b.handleProperty("audio-out-params", json.RawMessage(`{"format":"s32","samplerate":48000,"hr-channels":"stereo"}`))
	b.handleIPCMessage(mpvMessage{Event: "end-file", Reason: "eof"})
	if !b.Drained() || b.Position() != b.Duration() {
		t.Fatalf("EOF state: drained=%v position=%v duration=%v", b.Drained(), b.Position(), b.Duration())
	}
	status := b.BackendStatus()
	if status.Source.Format != "s32p" || status.Output.Format != "s32" || status.Output.SampleRate != 48000 {
		t.Fatalf("backend status = %+v", status)
	}
}

func TestMPVUnexpectedExitState(t *testing.T) {
	b := &MPVBackend{playing: true, paused: true}
	b.recordUnexpectedExit(errors.New("exit status 1"), "fatal audio error")
	if b.IsPlaying() || b.IsPaused() {
		t.Fatalf("unexpected exit left playback active: playing=%v paused=%v", b.IsPlaying(), b.IsPaused())
	}
	if err := b.StreamErr(); err == nil || !strings.Contains(err.Error(), "fatal audio error") {
		t.Fatalf("StreamErr() = %v", err)
	}
}

func TestMPVRuntimeSocketCleanup(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	dir, socket, err := newMPVRuntimeSocket()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(dir)) != xdg {
		t.Fatalf("runtime dir = %q, want under %q/cliamp", dir, xdg)
	}
	if err := os.WriteFile(socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &MPVBackend{runtimeDir: dir}
	b.Close()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime dir after Close: %v", err)
	}
}

func TestMPVFeatureErrors(t *testing.T) {
	b := &MPVBackend{bitPerfect: true}
	for _, feature := range []Feature{FeatureEQ, FeatureMono, FeatureVisualizer, FeatureVolume, FeatureSpeed} {
		if err := b.FeatureError(feature); err == nil {
			t.Errorf("FeatureError(%s) = nil", feature)
		}
	}
	b.bitPerfect = false
	if err := b.FeatureError(FeatureVolume); err != nil {
		t.Errorf("normal MPV volume support: %v", err)
	}
}

func TestMPVVolumeConversions(t *testing.T) {
	for _, db := range []float64{-50, -12, -3, 0, 6} {
		got := mpvPercentToDB(dbToMPVPercent(db), -90)
		if math.Abs(got-db) > 1e-9 {
			t.Errorf("round trip %.2f dB = %.12f", db, got)
		}
	}
}

func TestMPVMissingExecutable(t *testing.T) {
	_, err := NewMPVBackend(MPVOptions{Executable: "cliamp-mpv-does-not-exist"})
	if err == nil || !strings.Contains(err.Error(), "was not found in PATH") {
		t.Fatalf("NewMPVBackend missing executable error = %v", err)
	}
}

func TestMPVIntegration(t *testing.T) {
	if os.Getenv("CLIAMP_TEST_MPV") == "" {
		t.Skip("set CLIAMP_TEST_MPV=1 to run against an installed mpv")
	}
	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	wrapper := filepath.Join(t.TempDir(), "mpv-null-audio")
	script := fmt.Sprintf("#!/bin/sh\nexec %q --ao=null \"$@\"\n", mpvPath)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := NewMPVBackend(MPVOptions{Executable: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Play("../cliamp_whips_terminal_ass.mp3", 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.Duration() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if b.Duration() == 0 {
		t.Fatalf("MPV did not report file duration: %v", b.StreamErr())
	}
	b.TogglePause()
	if !b.IsPaused() {
		t.Fatal("pause state not updated")
	}
	b.TogglePause()
	if b.IsPaused() {
		t.Fatal("resume state not updated")
	}
	if err := b.Seek(time.Second); err != nil {
		t.Fatal(err)
	}
	b.Stop()
	if b.IsPlaying() {
		t.Fatal("stop left playback active")
	}

	bitPerfect, err := NewMPVBackend(MPVOptions{
		Executable:  wrapper,
		AudioDevice: "alsa/hw:CARD=Generic,DEV=0",
		BitPerfect:  true,
	})
	if err != nil {
		t.Fatalf("bit-perfect MPV startup: %v", err)
	}
	if status := bitPerfect.BackendStatus(); status.Device != "alsa/hw:CARD=Generic,DEV=0" || !status.BitPerfectMode || !status.DSPDisabled {
		t.Fatalf("bit-perfect status = %+v", status)
	}
	bitPerfect.Close()
}
