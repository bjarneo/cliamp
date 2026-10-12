package main

import (
	"net"
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/mass"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/sendspin"
	"github.com/bjarneo/cliamp/ui/model"
)

const defaultSendspinPort = 8927

// startSendspin registers cliamp as a Sendspin player with the Music
// Assistant server, so Music Assistant rows play through cliamp's own audio
// engine. It returns at once: the player connects, and reconnects, in the
// background. The returned func disconnects it.
func startSendspin(p *player.Player, m *model.Model, ma *mass.Provider, cfg config.MassConfig) func() {
	if ma == nil {
		return func() {}
	}
	u, err := url.Parse(ma.URL())
	if err != nil || u.Hostname() == "" {
		applog.Warn("sendspin: cannot take a host from the Music Assistant URL %q", ma.URL())
		return func() {}
	}
	port := cfg.SendspinPort
	if port == 0 {
		port = defaultSendspinPort
	}
	dir, err := appdir.Dir()
	if err != nil {
		applog.Warn("sendspin: %v", err)
		return func() {}
	}
	r, err := sendspin.Start(sendspin.Config{
		Addr:            net.JoinHostPort(u.Hostname(), strconv.Itoa(port)),
		Name:            cfg.PlayerName,
		ClientIDPath:    filepath.Join(dir, "sendspin_id"),
		SoftwareVersion: appmeta.Version(),
		// A stream started from Music Assistant itself (its app, Home
		// Assistant, a group leader) plays when cliamp is idle. Local
		// playback keeps the speaker.
		OnStream: func() {
			if p.IsPlaying() {
				return
			}
			go func() {
				if err := p.PlayAt(sendspin.URI, 0, 0); err != nil {
					applog.Warn("sendspin: start output: %v", err)
				}
			}()
		},
	})
	if err != nil {
		applog.Warn("sendspin: %v", err)
		return func() {}
	}
	p.RegisterStreamerFactory("sendspin:", r.NewStreamer)
	m.SetMusicAssistant(ma, r)
	return func() { _ = r.Close() }
}
