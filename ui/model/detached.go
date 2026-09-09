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

// Detached reports whether a client terminal is attached to this session.
func (m Model) Detached() bool {
	return m.detached
}
