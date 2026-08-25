//go:build !linux

package player

import (
	"errors"
	"strings"
)

func acquireAudioReservation(name string) (reservationHandle, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	return nil, errors.New("audio reservation is supported only on Linux")
}
