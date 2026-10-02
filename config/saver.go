package config

import (
	"strconv"
	"strings"
)

// SaveFunc wraps the package-level savers as methods, satisfying the
// ui/model.ConfigSaver interface. Tests stub that interface, so ui/model
// calls these methods and not the package functions.
type SaveFunc struct{}

// SaveString delegates to the package-level config.SaveString.
func (SaveFunc) SaveString(key, value string) error {
	return SaveString(key, value)
}

// SaveBool delegates to the package-level config.SaveBool.
func (SaveFunc) SaveBool(key string, value bool) error {
	return SaveBool(key, value)
}

// SaveFloat delegates to the package-level config.SaveFloat.
func (SaveFunc) SaveFloat(key string, value float64, prec int) error {
	return SaveFloat(key, value, prec)
}

// SaveFloats delegates to the package-level config.SaveFloats.
func (SaveFunc) SaveFloats(key string, values []float64) error {
	return SaveFloats(key, values)
}

// SaveString saves a top-level string key. It quotes value with QuoteString,
// so Load reads the same text back. value must be a single line.
func SaveString(key, value string) error {
	return save(key, QuoteString(value))
}

// SaveBool saves a top-level bool key as true or false.
func SaveBool(key string, value bool) error {
	return save(key, strconv.FormatBool(value))
}

// SaveFloat saves a top-level number key with prec digits after the decimal
// point. A prec of -1 writes the fewest digits that Load reads back as value.
func SaveFloat(key string, value float64, prec int) error {
	return save(key, strconv.FormatFloat(value, 'f', prec, 64))
}

// SaveFloats saves a top-level list of numbers, such as the eq bands, as
// [a, b, c]. Each number has the fewest digits that Load reads back.
func SaveFloats(key string, values []float64) error {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return save(key, "["+strings.Join(parts, ", ")+"]")
}
