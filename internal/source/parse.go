package source

import (
	"fmt"
	"strings"
	"unicode"
)

// Parse decodes a slug of the form `[host/]owner/repo[:subpath][@ref]`
// into a typed Source. It enforces the strict-parsing rules from PRD
// R3:
//
//   - Empty subpath after a colon is rejected.
//   - Malformed separator ordering (e.g., `@ref` before `:subpath`)
//     is rejected.
//   - Embedded whitespace anywhere in the slug is rejected.
//   - Multiple `:` separators are rejected.
//   - Multiple `@` separators are rejected.
//   - Empty owner or repo is rejected.
//
// The host segment is optional. It is detected by a `.` in the first
// segment of the slug (which is otherwise an org name; GitHub orgs
// cannot contain `.`). When omitted, Source.Host is left empty and
// callers should treat it as the DefaultHost.
func Parse(slug string) (Source, error) {
	if slug == "" {
		return Source{}, fmt.Errorf("source slug is empty")
	}
	for _, r := range slug {
		if unicode.IsSpace(r) {
			return Source{}, fmt.Errorf("source slug %q contains whitespace", slug)
		}
	}

	// Reject malformed separator ordering (R3b): `@ref` must come
	// after `:subpath`, so the first `@` cannot precede the first `:`.
	atIdx := strings.IndexByte(slug, '@')
	colonIdx := strings.IndexByte(slug, ':')
	if atIdx >= 0 && colonIdx >= 0 && atIdx < colonIdx {
		return Source{}, fmt.Errorf("source slug %q has separator in wrong position (`@ref` must come after `:subpath`)", slug)
	}

	// Split off ref via the rightmost-but-must-be-only `@`.
	rest := slug
	var ref string
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		if strings.Count(rest, "@") > 1 {
			return Source{}, fmt.Errorf("source slug %q contains multiple `@` separators", slug)
		}
		ref = rest[i+1:]
		rest = rest[:i]
		if ref == "" {
			return Source{}, fmt.Errorf("source slug %q has empty ref after `@`", slug)
		}
	}

	// Split off subpath via the only-allowed `:`.
	var subpath string
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		if strings.Count(rest, ":") > 1 {
			return Source{}, fmt.Errorf("source slug %q contains multiple `:` separators", slug)
		}
		subpath = rest[i+1:]
		rest = rest[:i]
		if subpath == "" {
			return Source{}, fmt.Errorf("source slug %q has empty subpath after `:`", slug)
		}
	}

	// `rest` is now `[host/]owner/repo`. Check for the malformed-
	// ordering case where `@` or `:` somehow ended up in `rest`
	// (shouldn't happen, but defensive).
	if strings.ContainsAny(rest, "@:") {
		return Source{}, fmt.Errorf("source slug %q has separator in wrong position", slug)
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("source slug %q must contain owner/repo", slug)
	}

	var host string
	if len(parts) == 3 && containsDot(parts[0]) {
		host = parts[0]
		parts = parts[1:]
	} else if len(parts) > 2 {
		return Source{}, fmt.Errorf("source slug %q has unexpected path segments", slug)
	}

	owner := parts[0]
	repo := parts[1]
	if owner == "" {
		return Source{}, fmt.Errorf("source slug %q has empty owner", slug)
	}
	if repo == "" {
		return Source{}, fmt.Errorf("source slug %q has empty repo", slug)
	}

	return Source{
		Host:    host,
		Owner:   owner,
		Repo:    repo,
		Subpath: subpath,
		Ref:     ref,
	}, nil
}

func containsDot(s string) bool {
	return strings.IndexByte(s, '.') >= 0
}
