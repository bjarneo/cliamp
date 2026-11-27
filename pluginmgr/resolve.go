package pluginmgr

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// resolveSource parses a plugin source string into a download URL and plugin name.
//
// Supported formats:
//
//	user/repo             -> GitHub (HEAD)
//	user/repo@v1.0        -> GitHub (tag)
//	gitlab:user/repo      -> GitLab (HEAD)
//	gitlab:user/repo@v1.0 -> GitLab (tag)
//	codeberg:user/repo    -> Codeberg (main)
//	codeberg:user/repo@v1 -> Codeberg (tag)
//	https://example.com/plugin.lua -> raw URL
func resolveSource(source string) (urls []string, name string, err error) {
	// Raw URL.
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if strings.IndexFunc(source, unicode.IsControl) >= 0 {
			return nil, "", fmt.Errorf("invalid URL %q: contains control characters", printable(source))
		}
		u, err := url.Parse(source)
		if err != nil {
			return nil, "", fmt.Errorf("invalid URL: %w", err)
		}
		if u.Host == "" {
			return nil, "", fmt.Errorf("invalid URL %q: missing host", printable(source))
		}
		base := path.Base(u.Path)
		name = strings.TrimSuffix(base, ".lua")
		if name == "" || name == "." || name == ".." || name == "/" {
			return nil, "", fmt.Errorf("cannot derive a plugin name from URL %q; it should end in <name>.lua", printable(source))
		}
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
			return nil, "", fmt.Errorf("unknown forge prefix %q (supported: gitlab, codeberg)", printable(prefix))
		}
	}

	// Split repo@tag.
	ref := ""
	if r, tag, ok := strings.Cut(repo, "@"); ok {
		if tag == "" {
			return nil, "", fmt.Errorf("empty ref in source %q", printable(source))
		}
		repo = r
		ref = tag
	}

	// Validate user/repo format.
	parts := strings.SplitN(repo, "/", 3)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, "", fmt.Errorf("invalid source %q (expected user/repo)", printable(source))
	}
	owner, repoName := parts[0], parts[1]
	if err := validateForgeSegment("owner", owner); err != nil {
		return nil, "", err
	}
	if err := validateForgeSegment("repo", repoName); err != nil {
		return nil, "", err
	}
	if ref != "" {
		if err := validateForgeSegment("ref", ref); err != nil {
			return nil, "", err
		}
	}

	// Convention: repos are named cliamp-plugin-<name> with entry point <name>.lua.
	short := strings.TrimPrefix(repoName, "cliamp-plugin-")
	if err := validateForgeSegment("entrypoint", short); err != nil {
		return nil, "", err
	}

	u, err := buildForgeURL(forge, owner, repoName, ref, short)
	if err != nil {
		return nil, "", err
	}
	return []string{u}, short, nil
}

// validateForgeSegment rejects path traversal, query or fragment separators,
// whitespace, and control characters in a forge URL segment.
func validateForgeSegment(label, seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.Contains(seg, "..") {
		return fmt.Errorf("invalid %s %q", label, printable(seg))
	}
	for _, r := range seg {
		if r == '/' || r == '\\' || r == '?' || r == '#' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("invalid %s %q", label, printable(seg))
		}
	}
	return nil
}

func buildForgeURL(forge, owner, repo, ref, entrypoint string) (string, error) {
	escapedOwner := url.PathEscape(owner)
	escapedRepo := url.PathEscape(repo)
	escapedEntry := url.PathEscape(entrypoint)

	var base string
	switch forge {
	case "github":
		if ref == "" {
			ref = "HEAD"
		}
		base = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", escapedOwner, escapedRepo, url.PathEscape(ref))
	case "gitlab":
		if ref == "" {
			ref = "HEAD"
		}
		base = fmt.Sprintf("https://gitlab.com/%s/%s/-/raw/%s", escapedOwner, escapedRepo, url.PathEscape(ref))
	case "codeberg":
		if ref == "" {
			base = fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/main", escapedOwner, escapedRepo)
		} else {
			base = fmt.Sprintf("https://codeberg.org/%s/%s/raw/tag/%s", escapedOwner, escapedRepo, url.PathEscape(ref))
		}
	default:
		return "", fmt.Errorf("unsupported forge %q", forge)
	}

	return base + "/" + escapedEntry + ".lua", nil
}
