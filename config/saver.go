package config

// SaveFunc wraps the package-level Save function as a method,
// satisfying the ui/model.ConfigSaver interface.
type SaveFunc struct{}

// Save delegates to the package-level config.Save.
func (SaveFunc) Save(key, value string) error {
	return Save(key, value)
}
