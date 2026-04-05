// Package applog provides a thread-safe in-app log buffer for messages that
// would otherwise be written to stderr (which corrupts the TUI).
package applog

import (
	"fmt"
	"sync"
	"time"
)

// Entry is a single log message with a timestamp.
type Entry struct {
	Text string
	At   time.Time
}

const maxEntries = 4

var (
	mu      sync.Mutex
	entries []Entry
)

// Printf writes a formatted log message to the buffer.
func Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	mu.Lock()
	entries = append(entries, Entry{Text: msg, At: time.Now()})
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	mu.Unlock()
}

// Drain returns all buffered entries and clears the buffer.
func Drain() []Entry {
	mu.Lock()
	if len(entries) == 0 {
		mu.Unlock()
		return nil
	}
	out := entries
	entries = nil
	mu.Unlock()
	return out
}
