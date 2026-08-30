package radio

import (
	"strings"
	"testing"
)

// TestCatalogStationURLsAreConstrainedToHTTP is a security regression test.
//
// Radio Browser is a public, user-submitted directory: anyone can add a
// station with any url_resolved value. Those URLs become playlist.Track.Path,
// and playback dispatches on the path prefix. player.openSource routes
// anything starting with ssh:// to exec.Command("ssh", ...), so an unfiltered
// catalog URL lets a directory submitter choose a host the user connects to.
//
// A cliamp:// link makes that reachable in one click, because the link author
// picks the search query and therefore which station comes back first.
func TestCatalogStationURLsAreConstrainedToHTTP(t *testing.T) {
	hostile := []CatalogStation{
		{Name: "ssh station", URL: "ssh://attacker.example/x"},
		{Name: "file station", URL: "file:///etc/passwd"},
		{Name: "local path station", URL: "/etc/shadow"},
		{Name: "data station", URL: "data:audio/mp3,AAAA"},
		{Name: "good station", URL: "https://stream.example/live.mp3"},
		{Name: "good plain http station", URL: "http://stream.example/live.mp3"},
	}

	for _, setup := range []struct {
		name   string
		prefix string
		apply  func(p *Provider, stations []CatalogStation)
	}{
		{"catalog", "c", (*Provider).AppendCatalog},
		{"search results", "s", (*Provider).SetSearchResults},
	} {
		t.Run(setup.name, func(t *testing.T) {
			p := New()
			setup.apply(p, hostile)

			var kept []string
			for i := range hostile {
				tracks, err := p.Tracks(setup.prefix + ":" + itoa(i))
				if err != nil {
					continue
				}
				for _, track := range tracks {
					kept = append(kept, track.Path)
					if !strings.HasPrefix(track.Path, "http://") && !strings.HasPrefix(track.Path, "https://") {
						t.Errorf("station track path %q is not http or https; a directory submitter chose it", track.Path)
					}
				}
			}
			if len(kept) == 0 {
				t.Fatal("no station tracks were produced; the test is not exercising anything")
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
