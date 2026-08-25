package model

import "github.com/bjarneo/cliamp/player"

func (m *Model) requirePlayerFeature(feature player.Feature) bool {
	if err := player.FeatureError(m.player, feature); err != nil {
		m.status.Warning(err.Error(), statusTTLDefault)
		return false
	}
	return true
}
