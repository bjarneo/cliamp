package luaplugin

import "sync"

// Plugins call p:bind() from their top-level chunk, which runs during New().
// SetReservedKeys is necessarily called later, once a Manager exists, so the
// reserved-key guard in bind() saw an empty set and silently accepted bindings
// on keys the core owns — which then never fire, because the core consumes
// those keys before the plugin dispatcher is reached.
//
// Setting the defaults before New() closes that window, so a plugin author
// gets the warning at load time instead of a key that quietly does nothing.

var (
	defaultReservedMu   sync.RWMutex
	defaultReservedKeys map[string]bool
)

// SetDefaultReservedKeys records the core-reserved keys for Managers created
// afterwards. Call it before New(). SetReservedKeys still overrides this on an
// existing Manager.
func SetDefaultReservedKeys(keys map[string]bool) {
	defaultReservedMu.Lock()
	defer defaultReservedMu.Unlock()
	defaultReservedKeys = make(map[string]bool, len(keys))
	for k, v := range keys {
		defaultReservedKeys[k] = v
	}
}

// initialReservedKeys returns a copy of the defaults for a new Manager.
func initialReservedKeys() map[string]bool {
	defaultReservedMu.RLock()
	defer defaultReservedMu.RUnlock()
	if defaultReservedKeys == nil {
		return nil
	}
	out := make(map[string]bool, len(defaultReservedKeys))
	for k, v := range defaultReservedKeys {
		out[k] = v
	}
	return out
}
