package pluginmgr

import (
	"strings"
	"testing"
)

func TestResolveSourceGitHub(t *testing.T) {
	urls, name, err := resolveSource("user/cliamp-plugin-cool")
	if err != nil {
		t.Fatal(err)
	}
	if name != "cool" {
		t.Fatalf("name = %q, want %q", name, "cool")
	}
	want := "https://raw.githubusercontent.com/user/cliamp-plugin-cool/HEAD/cool.lua"
	if len(urls) != 1 || urls[0] != want {
		t.Fatalf("urls = %v, want [%s]", urls, want)
	}
}

func TestResolveSourceGitHubWithTag(t *testing.T) {
	urls, name, err := resolveSource("user/cliamp-plugin-cool@v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if name != "cool" {
		t.Fatalf("name = %q, want %q", name, "cool")
	}
	want := "https://raw.githubusercontent.com/user/cliamp-plugin-cool/v1.0/cool.lua"
	if urls[0] != want {
		t.Fatalf("url = %q, want %q", urls[0], want)
	}
}

func TestResolveSourceGitLab(t *testing.T) {
	urls, name, err := resolveSource("gitlab:user/cliamp-plugin-vis")
	if err != nil {
		t.Fatal(err)
	}
	if name != "vis" {
		t.Fatalf("name = %q, want %q", name, "vis")
	}
	want := "https://gitlab.com/user/cliamp-plugin-vis/-/raw/HEAD/vis.lua"
	if urls[0] != want {
		t.Fatalf("url = %q, want %q", urls[0], want)
	}
}

func TestResolveSourceCodeberg(t *testing.T) {
	urls, name, err := resolveSource("codeberg:user/cliamp-plugin-eq")
	if err != nil {
		t.Fatal(err)
	}
	if name != "eq" {
		t.Fatalf("name = %q, want %q", name, "eq")
	}
	want := "https://codeberg.org/user/cliamp-plugin-eq/raw/branch/main/eq.lua"
	if urls[0] != want {
		t.Fatalf("url = %q, want %q", urls[0], want)
	}
}

func TestResolveSourceCodebergWithTag(t *testing.T) {
	urls, _, err := resolveSource("codeberg:user/cliamp-plugin-eq@v2")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://codeberg.org/user/cliamp-plugin-eq/raw/tag/v2/eq.lua"
	if urls[0] != want {
		t.Fatalf("url = %q, want %q", urls[0], want)
	}
}

func TestResolveSourceRawURL(t *testing.T) {
	urls, name, err := resolveSource("https://example.com/my-plugin.lua")
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-plugin" {
		t.Fatalf("name = %q, want %q", name, "my-plugin")
	}
	if urls[0] != "https://example.com/my-plugin.lua" {
		t.Fatalf("url = %q", urls[0])
	}
}

func TestResolveSourceInvalid(t *testing.T) {
	tests := []string{
		"",
		"justname",
		"too/many/parts",
		"/nope",
		"bitbucket:user/repo",
		"user/repo@../other",
		"user/repo@a/b",
		"user/repo@main?x=1",
		"user/repo@main#frag",
		"user/repo@",
		"user/repo@a b",
		"user/repo@v1\x1b[31m",
		"../repo",
		"user/..",
		"user/cliamp-plugin-",
		"user/cliamp-plugin-..",
		"user?x/repo",
		"user#x/repo",
		"user/repo\\evil",
		"https://example.com/\x1b[2J.lua",
		"https:///missing-host.lua",
	}

	for _, src := range tests {
		_, _, err := resolveSource(src)
		if err == nil {
			t.Errorf("resolveSource(%q) should return error", src)
		} else if strings.Contains(err.Error(), "%!w(<nil>)") {
			t.Errorf("resolveSource(%q) formatted nil error: %v", src, err)
		}
	}
}

func TestResolveSourceEscapesPathSegments(t *testing.T) {
	urls, name, err := resolveSource("user/repo@v1%2f2")
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if name != "repo" {
		t.Fatalf("name = %q, want repo", name)
	}
	want := "https://raw.githubusercontent.com/user/repo/v1%252f2/repo.lua"
	if len(urls) != 1 || urls[0] != want {
		t.Fatalf("urls = %v, want [%s]", urls, want)
	}
}

func TestResolveSourceNonPrefixedRepo(t *testing.T) {
	urls, name, err := resolveSource("user/my-equalizer")
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-equalizer" {
		t.Fatalf("name = %q, want %q", name, "my-equalizer")
	}
	want := "https://raw.githubusercontent.com/user/my-equalizer/HEAD/my-equalizer.lua"
	if urls[0] != want {
		t.Fatalf("url = %q, want %q", urls[0], want)
	}
}
