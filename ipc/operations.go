package ipc

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
)

const (
	maxOperationBatchSize = 1_000
	maxOperationLimit     = 10_000
)

// Operation describes a runtime capability exposed through V2. Validation is
// deliberately protocol-only; the model or daemon supplies the behavior.
type Operation struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Async       bool     `json:"async,omitempty"`
	Parameters  []string `json:"parameters,omitempty"`
}

// OperationRegistry centralizes V2 operation metadata and parameter checks.
type OperationRegistry struct {
	mu         sync.RWMutex
	operations map[string]Operation
}

// NewOperationRegistry creates a registry from operation metadata.
func NewOperationRegistry(operations ...Operation) *OperationRegistry {
	registry := &OperationRegistry{operations: make(map[string]Operation, len(operations))}
	for _, operation := range operations {
		registry.Register(operation)
	}
	return registry
}

// Register adds or replaces one operation. Invalid names are ignored because
// registry construction is local program configuration, not client input.
func (r *OperationRegistry) Register(operation Operation) {
	name := strings.TrimSpace(operation.Name)
	if name == "" {
		return
	}
	operation.Name = name
	operation.Parameters = append([]string(nil), operation.Parameters...)
	r.mu.Lock()
	r.operations[name] = operation
	r.mu.Unlock()
}

// Unregister removes local capabilities that are unavailable in a particular
// runtime, such as TUI-only appearance controls in daemon mode.
func (r *OperationRegistry) Unregister(names ...string) {
	r.mu.Lock()
	for _, name := range names {
		delete(r.operations, strings.TrimSpace(name))
	}
	r.mu.Unlock()
}

// Lookup returns a copy of the metadata for name.
func (r *OperationRegistry) Lookup(name string) (Operation, bool) {
	r.mu.RLock()
	operation, ok := r.operations[name]
	r.mu.RUnlock()
	if !ok {
		return Operation{}, false
	}
	operation.Parameters = append([]string(nil), operation.Parameters...)
	return operation, true
}

// Operations returns capability metadata in deterministic name order.
func (r *OperationRegistry) Operations() []Operation {
	r.mu.RLock()
	operations := make([]Operation, 0, len(r.operations))
	for _, operation := range r.operations {
		operation.Parameters = append([]string(nil), operation.Parameters...)
		operations = append(operations, operation)
	}
	r.mu.RUnlock()
	sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
	return operations
}

// Validate checks that operation is registered and that common numeric, index,
// and batch parameter forms are well-formed.
func (r *OperationRegistry) Validate(operation string, params json.RawMessage) *V2Error {
	if _, ok := r.Lookup(operation); !ok {
		return v2Error(V2ErrorCodeUnknownOperation, V2MessageUnknownOperation)
	}
	return validateOperationParams(params)
}

// DefaultOperationRegistry returns the runtime capabilities provided by the
// V2 foundation. These names are behavior-free until a V2Dispatcher is wired.
func DefaultOperationRegistry() *OperationRegistry {
	operations := []Operation{
		{Name: "runtime.snapshot", Description: "read the complete runtime snapshot"},
		{Name: "runtime.status", Description: "read the runtime status"},
		{Name: "runtime.play", Description: "start playback"},
		{Name: "runtime.pause", Description: "pause playback"},
		{Name: "runtime.toggle", Description: "toggle playback"},
		{Name: "runtime.stop", Description: "stop playback"},
		{Name: "runtime.next", Description: "skip to the next track"},
		{Name: "runtime.prev", Description: "skip to the previous track"},
		{Name: "runtime.volume", Description: "change volume", Parameters: []string{"value"}},
		{Name: "runtime.seek", Description: "seek playback", Parameters: []string{"value"}},
		{Name: "runtime.speed", Description: "change playback speed", Parameters: []string{"value"}},
		{Name: "runtime.queue.list", Description: "list the live playlist", Parameters: []string{"offset", "limit"}},
		{Name: "runtime.queue.play", Description: "play a live playlist track", Parameters: []string{"index", "if_revision"}},
		{Name: "runtime.queue.enqueue", Description: "add a live track to play-next", Parameters: []string{"index", "if_revision"}},
		{Name: "runtime.queue.remove", Description: "remove a live playlist track", Parameters: []string{"index", "if_revision"}},
		{Name: "runtime.queue.move", Description: "move a live playlist track", Parameters: []string{"index", "to", "if_revision"}},
		{Name: "runtime.queue.clear", Description: "clear the live playlist", Parameters: []string{"if_revision"}},
		{Name: "runtime.library.search", Description: "search a provider", Parameters: []string{"provider", "query", "offset", "limit"}},
		{Name: "runtime.history", Description: "read playback history", Parameters: []string{"limit"}},
		{Name: "play", Description: "start or resume playback"},
		{Name: "pause", Description: "pause playback"},
		{Name: "toggle", Description: "toggle playback"},
		{Name: "stop", Description: "stop playback"},
		{Name: "next", Description: "skip to the next track"},
		{Name: "prev", Description: "skip to the previous track"},
		{Name: "volume", Description: "set volume", Parameters: []string{"value"}},
		{Name: "volume.adjust", Description: "adjust volume", Parameters: []string{"value"}},
		{Name: "seek", Description: "seek relative to the current position", Parameters: []string{"value"}},
		{Name: "seek.absolute", Description: "seek to an absolute position", Parameters: []string{"value"}},
		{Name: "speed", Description: "set playback speed", Parameters: []string{"value"}},
		{Name: "speed.adjust", Description: "adjust playback speed", Parameters: []string{"value"}},
		{Name: "shuffle", Description: "set shuffle mode", Parameters: []string{"name"}},
		{Name: "repeat", Description: "set repeat mode", Parameters: []string{"name"}},
		{Name: "mono", Description: "set mono output", Parameters: []string{"name"}},
		{Name: "eq", Description: "set an EQ preset or band", Parameters: []string{"name", "band", "value"}},
		{Name: "device", Description: "list or switch audio devices", Parameters: []string{"name"}},
		{Name: "theme", Description: "list or change themes", Parameters: []string{"name"}},
		{Name: "vis", Description: "list or change visualizers", Parameters: []string{"name"}},
		{Name: "load", Description: "load a local playlist", Async: true, Parameters: []string{"playlist"}},
		{Name: "queue", Description: "append one path to the live playlist", Parameters: []string{"path", "if_revision"}},
		{Name: "url.load", Description: "resolve and append a URL, optionally playing it", Async: true, Parameters: []string{"path", "play"}},
		{Name: "save", Description: "download the current track", Async: true},
		{Name: "queue.list", Description: "list the live playlist", Parameters: []string{"offset", "limit"}},
		{Name: "queue.play", Description: "play a live playlist track", Async: true, Parameters: []string{"index", "if_revision"}},
		{Name: "queue.enqueue", Description: "add a live track to play-next", Parameters: []string{"index", "if_revision"}},
		{Name: "queue.remove", Description: "remove a live playlist track", Parameters: []string{"index", "if_revision"}},
		{Name: "queue.move", Description: "move a live playlist track", Parameters: []string{"index", "to", "if_revision"}},
		{Name: "queue.clear", Description: "clear the live playlist", Parameters: []string{"if_revision"}},
		{Name: "track.play", Description: "play a supplied track", Async: true, Parameters: []string{"track", "if_revision"}},
		{Name: "track.queue", Description: "queue a supplied track next", Parameters: []string{"track", "if_revision"}},
		{Name: "playnext.list", Description: "list play-next entries", Parameters: []string{"offset", "limit"}},
		{Name: "playnext.remove", Description: "remove a play-next entry", Parameters: []string{"index", "if_revision"}},
		{Name: "playnext.move", Description: "move a play-next entry", Parameters: []string{"index", "to", "if_revision"}},
		{Name: "playnext.clear", Description: "clear play-next entries", Parameters: []string{"if_revision"}},
		{Name: "provider.list", Description: "list configured providers"},
		{Name: "provider.playlists", Description: "list provider playlists", Async: true, Parameters: []string{"provider", "offset", "limit"}},
		{Name: "provider.tracks", Description: "list provider tracks", Async: true, Parameters: []string{"provider", "playlist", "offset", "limit"}},
		{Name: "provider.load", Description: "load a provider playlist", Async: true, Parameters: []string{"provider", "playlist"}},
		{Name: "provider.search", Description: "search a provider", Async: true, Parameters: []string{"provider", "query", "offset", "limit"}},
		{Name: "provider.artists", Description: "list provider artists", Async: true, Parameters: []string{"provider", "offset", "limit"}},
		{Name: "provider.artist_albums", Description: "list an artist's albums", Async: true, Parameters: []string{"provider", "artist", "offset", "limit"}},
		{Name: "provider.albums", Description: "list provider albums", Async: true, Parameters: []string{"provider", "sort", "offset", "limit"}},
		{Name: "provider.album_tracks", Description: "list album tracks", Async: true, Parameters: []string{"provider", "album", "offset", "limit"}},
		{Name: "provider.load_album", Description: "load an album", Async: true, Parameters: []string{"provider", "album"}},
		{Name: "provider.favorite", Description: "toggle a provider favorite", Async: true, Parameters: []string{"provider", "playlist"}},
		{Name: "provider.catalog", Description: "load a provider catalog page", Async: true, Parameters: []string{"provider", "offset", "limit"}},
		{Name: "playlist.create", Description: "create a saved playlist", Async: true, Parameters: []string{"provider", "playlist"}},
		{Name: "playlist.rename", Description: "rename a saved playlist", Async: true, Parameters: []string{"provider", "playlist", "new_name"}},
		{Name: "playlist.delete", Description: "delete a saved playlist", Async: true, Parameters: []string{"provider", "playlist"}},
		{Name: "playlist.add", Description: "add a track to a saved playlist", Async: true, Parameters: []string{"provider", "playlist", "track"}},
		{Name: "playlist.add_many", Description: "add tracks to a saved playlist", Async: true, Parameters: []string{"provider", "playlist", "tracks"}},
		{Name: "playlist.replace", Description: "replace ordered saved playlist tracks", Async: true, Parameters: []string{"provider", "playlist", "tracks"}},
		{Name: "playlist.remove", Description: "remove a saved playlist track", Async: true, Parameters: []string{"provider", "playlist", "index"}},
		{Name: "playlist.bookmark", Description: "toggle a saved track bookmark", Async: true, Parameters: []string{"provider", "playlist", "track"}},
		{Name: "lyrics", Description: "fetch lyrics", Async: true},
		{Name: "history", Description: "read history", Async: true, Parameters: []string{"limit"}},
		{Name: "history.clear", Description: "clear history", Async: true},
		{Name: "plugin.call", Description: "invoke a plugin command", Async: true, Parameters: []string{"name", "sub", "args"}},
		{Name: "plugin.commands", Description: "list plugin commands"},
	}
	// Operations are submitted as jobs, including fast reads, so callers have a
	// single completion and cancellation model. Direct V2 methods are documented
	// separately from this operation registry.
	for i := range operations {
		if operations[i].Name != "runtime.snapshot" && operations[i].Name != "runtime.status" {
			operations[i].Async = true
		}
	}
	return NewOperationRegistry(operations...)
}

func validateOperationParams(params json.RawMessage) *V2Error {
	if len(params) == 0 || bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		return nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return invalidV2Params()
	}
	for _, name := range []string{"value", "position", "offset_seconds", "speed", "volume", "gain"} {
		if raw, ok := values[name]; ok && !validFiniteNumber(raw) {
			return invalidV2Params()
		}
	}
	for _, name := range []string{"index", "to", "offset", "limit", "if_revision"} {
		raw, ok := values[name]
		if !ok {
			continue
		}
		value, ok := validNonNegativeInteger(raw)
		if !ok || (name == "limit" && (value == 0 || value > maxOperationLimit)) {
			return invalidV2Params()
		}
	}
	for _, name := range []string{"batch", "items", "indexes"} {
		raw, ok := values[name]
		if !ok {
			continue
		}
		if !validBatch(raw) || (name == "indexes" && !validIndexes(raw)) {
			return invalidV2Params()
		}
	}
	return nil
}

func validFiniteNumber(raw json.RawMessage) bool {
	var value float64
	if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	return true
}

func validNonNegativeInteger(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == '"' {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value json.Number
	if decoder.Decode(&value) != nil {
		return 0, false
	}
	if decoder.More() {
		return 0, false
	}
	parsed, err := value.Int64()
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func validBatch(raw json.RawMessage) bool {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > maxOperationBatchSize {
		return false
	}
	return true
}

func validIndexes(raw json.RawMessage) bool {
	var indexes []json.RawMessage
	if json.Unmarshal(raw, &indexes) != nil {
		return false
	}
	for _, index := range indexes {
		if _, ok := validNonNegativeInteger(index); !ok {
			return false
		}
	}
	return true
}
