//go:build !termux

package player

import (
	"github.com/gopxl/beep/v2"
	bspe "github.com/gopxl/beep/v2/speaker"
)

// beepSpeaker wraps gopxl/beep/v2/speaker so the default build of cliamp
// keeps using ALSA/CoreAudio/WinMM without any change in behavior.
type beepSpeaker struct{}

func (beepSpeaker) Init(sampleRate beep.SampleRate, bufferSize int) error {
	return bspe.Init(sampleRate, bufferSize)
}

func (beepSpeaker) Play(s ...beep.Streamer) { bspe.Play(s...) }
func (beepSpeaker) Clear()                  { bspe.Clear() }
func (beepSpeaker) Close()                  { bspe.Clear() }
func (beepSpeaker) Lock()                   { bspe.Lock() }
func (beepSpeaker) Unlock()                 { bspe.Unlock() }
func (beepSpeaker) Suspend() error          { return bspe.Suspend() }
func (beepSpeaker) Resume() error           { return bspe.Resume() }

func init() { backend = beepSpeaker{} }
