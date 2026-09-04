package player

import "github.com/gopxl/beep/v2"

// Speaker abstracts the audio output backend so cliamp can swap the
// implementation: gopxl/beep/v2/speaker (ALSA/CoreAudio/WinMM) on regular
// Linux/macOS/Windows, or jfreymuth/pulse on Android/Termux.
//
// The interface mirrors the subset of gopxl/beep/v2/speaker's surface that
// cliamp actually uses. Lock/Unlock protect the underlying mixer from
// concurrent mutation while an audio callback may be reading from it; the
// implementation is not reentrant.
type Speaker interface {
	Init(sampleRate beep.SampleRate, bufferSize int) error
	Play(s ...beep.Streamer)
	Clear()
	Close()
	Lock()
	Unlock()
	Suspend() error
	Resume() error
}

// backend is populated by init() in either speaker_beep.go (default) or
// speaker_termux.go (build tag termux).
var backend Speaker

// SpeakerInit initializes the audio output backend. Must be called once
// before any other Speaker* call. sampleRate is in Hz, bufferSize is in
// samples (matches gopxl/beep/v2/speaker.Init semantics).
func SpeakerInit(sampleRate beep.SampleRate, bufferSize int) error {
	return backend.Init(sampleRate, bufferSize)
}

// SpeakerPlay registers Streamers for playback. On the first call the backend
// begins driving the audio callback goroutine; subsequent calls add to the
// mixer without restarting it.
func SpeakerPlay(s ...beep.Streamer) { backend.Play(s...) }

// SpeakerClear removes all currently playing Streamers from the mixer.
func SpeakerClear() { backend.Clear() }

// SpeakerClose stops audio playback and releases backend resources when supported.
func SpeakerClose() { backend.Close() }

// SpeakerLock locks the backend's mixer against concurrent reads/writes.
// Pair with SpeakerUnlock. Not reentrant.
func SpeakerLock() { backend.Lock() }

// SpeakerUnlock releases the lock taken by SpeakerLock.
func SpeakerUnlock() { backend.Unlock() }

// SpeakerSuspend pauses audio output without dropping the connection.
// Safe to call before any Play; implementations may treat it as a no-op
// until playback has started.
func SpeakerSuspend() error { return backend.Suspend() }

// SpeakerResume restarts audio output after a SpeakerSuspend.
func SpeakerResume() error { return backend.Resume() }
