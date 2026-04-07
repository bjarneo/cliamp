//go:build !linux && (!darwin || !cgo)

package mediactl

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/internal/playback"
)

func Run(prog *tea.Program, svc *Service) (tea.Model, error) {
	return prog.Run()
}

type Service struct{}

func New(send func(tea.Msg)) (*Service, error) {
	return nil, nil
}

func (s *Service) Update(state playback.State) {}

func (s *Service) Seeked(position time.Duration) {}

func (s *Service) Close() {}
