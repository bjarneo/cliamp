package playlist

// WithPlaybackContext returns owned tracks with their complete source list and
// per-entry index attached. The shared snapshot is immutable and runtime-only.
func WithPlaybackContext(tracks []Track) []Track {
	context := cloneTracks(tracks)
	for i := range context {
		// Never retain previous contexts in a snapshot, including nested ones.
		context[i].playbackContext = nil
		context[i].playbackContextIndex = 0
	}
	cloned := cloneTracks(context)
	for i := range cloned {
		cloned[i].playbackContext = context
		cloned[i].playbackContextIndex = i
	}
	return cloned
}

// PlaybackContext returns an owned copy of the source list and this entry's
// index, or nil and -1 if no source context was attached.
func (t Track) PlaybackContext() ([]Track, int) {
	if len(t.playbackContext) == 0 {
		return nil, -1
	}
	return cloneTracks(t.playbackContext), t.playbackContextIndex
}
