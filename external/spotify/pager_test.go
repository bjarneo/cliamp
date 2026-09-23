package spotify

import "github.com/bjarneo/cliamp/playlist"

func trackPaths(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, t := range tracks {
		paths[i] = t.Path
	}
	return paths
}
