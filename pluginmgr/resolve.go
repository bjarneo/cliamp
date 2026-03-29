package pluginmgr

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// resolveSource parses a plugin source string into a download URL and plugin name.
//
// Supported formats:
//
//	user/repo             → GitHub (HEAD)
//	user/repo@v1.0        → GitHub (tag)
//	gitlab:user/repo      → GitLab (HEAD)
//	gitlab:user/repo@v1.0 → GitLab (tag)
//	codeberg:user/repo    → Codeberg (main)
//	codeberg:user/repo@v1 → Codeberg (tag)
//	https://example.com/plugin.lua → raw URL
func resolveSource(source string) (urls []string, name string, err error) {
	// Raw URL.
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, err := url.Parse(source)
		if err != nil {
			return nil, "", fmt.Errorf("invalid URL: %w", err)
		}
		base := path.Base(u.Path)
		name = strings.TrimSuffix(base, ".lua")
		return []string{source}, name, nil
	}

	// Detect forge prefix.
	forge := "github"
	repo := source
	if prefix, rest, ok := strings.Cut(source, ":"); ok {
		switch prefix {
		case "gitlab", "codeberg":
			forge = prefix
			repo = rest
		default:
			return nil, "", fmt.Errorf("unknown forge prefix %q (supported: gitlab, codeberg)", prefix)
		}
	}

	// Split repo@tag.
	ref := ""
	if r, tag, ok := strings.Cut(repo, "@"); ok {
		repo = r
		ref = tag
	}

	// Validate user/repo format.
	parts := strings.SplitN(repo, "/", 3)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, "", fmt.Errorf("invalid source %q (expected user/repo)", source)
	}
	name = parts[1]

	// Convention: repos are named cliamp-plugin-<name> with entry point <name>.lua.
	short := strings.TrimPrefix(name, "cliamp-plugin-")
	u, err := buildForgeURL(forge, repo, ref, short)
	if err != nil {
		return nil, "", err
	}
	return []string{u}, short, nil
}

func buildForgeURL(forge, repo, ref, entrypoint string) (string, error) {
	var base string
	switch forge {
	case "github":
		if ref == "" {
			ref = "HEAD"
		}
		base = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", repo, ref)
	case "gitlab":
		if ref == "" {
			ref = "HEAD"
		}
		base = fmt.Sprintf("https://gitlab.com/%s/-/raw/%s", repo, ref)
	case "codeberg":
		if ref == "" {
			base = fmt.Sprintf("https://codeberg.org/%s/raw/branch/main", repo)
		} else {
			base = fmt.Sprintf("https://codeberg.org/%s/raw/tag/%s", repo, ref)
		}
	default:
		return "", fmt.Errorf("unsupported forge %q", forge)
	}

	return base + "/" + entrypoint + ".lua", nil
}
