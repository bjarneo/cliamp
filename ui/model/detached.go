package model

// SetDetachedMsg switches the model between attached and detached rendering.
// A detached session has no client terminal reading its output: it keeps
// playing, keeps serving IPC, and keeps its playlist bookkeeping, but stops
// paying for a visualizer nobody can see.
type SetDetachedMsg struct {
	Detached bool
}

// SetDetached marks the model as rendering into a virtual terminal with no
// client attached. Callers that host the session use it before the program
// starts; later changes arrive as SetDetachedMsg.
func (m *Model) SetDetached(detached bool) {
	m.detached = detached
}

// Detached reports whether this session has no client terminal attached.
func (m Model) Detached() bool {
	return m.detached
}

// SetSessionDetach makes the quit key hand the terminal back instead of
// stopping the player. Only a session host sets it; a cliamp that owns its
// terminal has nowhere to detach to, so there the quit key still quits.
func (m *Model) SetSessionDetach(detach func()) {
	m.sessionDetach = detach
}
