package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

type remoteTestEngine struct {
	playbackFakeEngine
	remote         bool
	selected, path string
	streamErr      error
	volumeKnown    bool
}

func (e *remoteTestEngine) RemoteOutput() bool { return e.remote }
func (e *remoteTestEngine) OutputDevices(context.Context) ([]player.AudioDevice, error) {
	return []player.AudioDevice{{Name: "tidal:receiver:2019", Description: "Receiver", Active: e.remote}}, nil
}
func (e *remoteTestEngine) SwitchOutput(_ context.Context, name, path string) error {
	e.selected = name
	e.path = path
	e.remote = name != "local"
	return nil
}
func (e *remoteTestEngine) StreamErr() error  { return e.streamErr }
func (e *remoteTestEngine) VolumeKnown() bool { return !e.remote || e.volumeKnown }

func TestRemoteOutputSkipsLocalPreloadAndReconnect(t *testing.T) {
	engine := &remoteTestEngine{remote: true, streamErr: errors.New("receiver lost")}
	engine.playing = true
	engine.duration = time.Minute
	m := newHeadlessModel(t, engine, nil, playlist.Track{Path: "tidal://track/42", Stream: true}, playlist.Track{Path: "tidal://track/43", Stream: true})
	m.beginPlaybackTrack(playlist.Track{Path: "tidal://track/42", Stream: true})
	if _, ok := m.preloadTarget(); ok {
		t.Fatal("remote output armed local preload")
	}
	m.tickStreamHealth(time.Now())
	if !m.reconnect.at.IsZero() || m.err == nil {
		t.Fatal("remote disconnect should surface error without restarting playback")
	}
}
func TestRemoteDeviceSelectionUsesCurrentTrackAndDoesNotPersist(t *testing.T) {
	engine := &remoteTestEngine{}
	saver := &recordingSaver{}
	m := newHeadlessModel(t, engine, nil, playlist.Track{Path: "tidal://track/42", Stream: true})
	m.configSaver = saver
	m.beginPlaybackTrack(playlist.Track{Path: "tidal://track/42", Stream: true})
	engine.playing = true
	response := runV2(t, &m, "device", ipc.Request{Name: "tidal:receiver:2019"})
	if !response.OK || engine.selected != "tidal:receiver:2019" || engine.path != "tidal://track/42" {
		t.Fatalf("response=%+v selected=%s path=%s", response, engine.selected, engine.path)
	}
	if _, ok := saver.saved["audio_device"]; ok {
		t.Fatal("remote target persisted as native audio device")
	}
	response = runV2(t, &m, "device", ipc.Request{Name: "local"})
	if !response.OK || engine.remote {
		t.Fatal("return to local failed")
	}
	if _, ok := saver.saved["audio_device"]; ok {
		t.Fatal("local alias persisted as native audio device")
	}
}
func TestRemoteEffectsUnavailable(t *testing.T) {
	engine := &remoteTestEngine{remote: true}
	m := newHeadlessModel(t, engine, nil)
	for _, op := range []string{"speed", "speed.adjust", "mono", "eq", "vis"} {
		result := runV2(t, &m, op, ipc.Request{Value: 1, Name: "toggle"})
		if result.OK {
			t.Fatalf("%s accepted on remote output", op)
		}
	}
	m.setSpeed(2)
	m.setCustomEQBand(0, 6)
	if engine.Speed() != 1 || engine.eqBands[0] != 0 {
		t.Fatal("remote effect modified saved local settings")
	}
}

func TestRemoteVolumeAdjustmentWaitsForReceiver(t *testing.T) {
	engine := &remoteTestEngine{remote: true}
	engine.volume = engine.VolumeMin()
	m := newHeadlessModel(t, engine, nil)
	m.adjustVolume(1)
	response := runV2(t, &m, "volume.adjust", ipc.Request{Value: 1})
	if !response.OK || engine.Volume() != engine.VolumeMin() {
		t.Fatal("relative adjustment used an unknown volume")
	}
	response = runV2(t, &m, "volume", ipc.Request{Value: -12})
	if !response.OK || engine.Volume() != -12 {
		t.Fatal("absolute volume should work without a receiver report")
	}
	engine.volumeKnown = true
	response = runV2(t, &m, "volume.adjust", ipc.Request{Value: 1})
	if !response.OK || engine.Volume() != -11 {
		t.Fatal("relative adjustment ignored a known volume")
	}
	engine.remote, engine.volumeKnown = false, false
	m.adjustVolume(1)
	if engine.Volume() != -10 {
		t.Fatal("local volume adjustment was blocked")
	}
}
