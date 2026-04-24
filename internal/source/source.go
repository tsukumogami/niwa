// Package source defines the canonical typed representation of a
// niwa workspace config source and the parser for the slug grammar
// `[host/]owner/repo[:subpath][@ref]`.
//
// This is a leaf package: it imports nothing from the rest of niwa.
// All consumers (init, config-set, fetcher, registry, state, status,
// guardrail, reset) receive a typed Source rather than parsing slug
// strings ad hoc.
//
// The Source struct is the canonical five-tuple. Helper methods on
// Source (CloneURL, TarballURL, CommitsAPIURL, OverlayDerivedSource,
// DisplayRef) produce the strings each consumer needs without I/O.
package source

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultHost is the assumed host when a slug omits the host segment.
const DefaultHost = "github.com"

// Source is the canonical workspace-config-source identity. Empty
// Subpath means "run convention discovery at the repo root"; empty
// Ref means "the source repo's default branch."
type Source struct {
	Host    string // canonical host, e.g. "github.com"
	Owner   string // org or user
	Repo    string // repo name
	Subpath string // empty == discovery; otherwise a slash-separated path
	Ref     string // empty == default branch; otherwise tag/branch/sha
}

// String renders Source back to a slug. For whole-repo sources with
// no host override and no ref, the output round-trips exactly with
// the user's input shorthand (e.g., "org/repo").
func (s Source) String() string {
	var b strings.Builder
	if s.Host != "" && s.Host != DefaultHost {
		b.WriteString(s.Host)
		b.WriteByte('/')
	}
	b.WriteString(s.Owner)
	b.WriteByte('/')
	b.WriteString(s.Repo)
	if s.Subpath != "" {
		b.WriteByte(':')
		b.WriteString(s.Subpath)
	}
	if s.Ref != "" {
		b.WriteByte('@')
		b.WriteString(s.Ref)
	}
	return b.String()
}

// CloneURL returns the git clone URL for the source repo, suitable
// for `git clone`. Subpath and Ref are intentionally not encoded;
// they are applied after the clone by the caller.
//
// protocol must be "ssh" or "https" (empty defaults to "https").
// Hosts other than github.com only support https.
func (s Source) CloneURL(protocol string) (string, error) {
	host := s.Host
	if host == "" {
		host = DefaultHost
	}
	switch strings.ToLower(protocol) {
	case "ssh", "":
		if strings.ToLower(protocol) == "ssh" || host == DefaultHost {
			if host == DefaultHost && strings.ToLower(protocol) == "ssh" {
				return fmt.Sprintf("git@%s:%s/%s.git", host, s.Owner, s.Repo), nil
			}
		}
		return fmt.Sprintf("https://%s/%s/%s.git", host, s.Owner, s.Repo), nil
	case "https":
		return fmt.Sprintf("https://%s/%s/%s.git", host, s.Owner, s.Repo), nil
	default:
		return "", fmt.Errorf("unsupported clone protocol: %q", protocol)
	}
}

// TarballURL returns the GitHub REST tarball endpoint URL for the
// source ref, or empty string for non-GitHub hosts (which use the
// git-clone fallback path). The returned URL is constructed against
// `https://api.github.com`; callers needing a different base URL
// should construct it themselves.
func (s Source) TarballURL() string {
	if s.Host != "" && s.Host != DefaultHost {
		return ""
	}
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/tarball/%s",
		s.Owner, s.Repo, url.PathEscape(ref))
}

// CommitsAPIURL returns the GitHub REST commits endpoint URL for the
// SHA-only drift check. ref overrides Source.Ref when non-empty; this
// supports drift-checking against a different ref than the source
// stores. Returns empty for non-GitHub hosts.
func (s Source) CommitsAPIURL(ref string) string {
	if s.Host != "" && s.Host != DefaultHost {
		return ""
	}
	if ref == "" {
		ref = s.Ref
	}
	if ref == "" {
		ref = "HEAD"
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s",
		s.Owner, s.Repo, url.PathEscape(ref))
}

// OverlayDerivedSource implements PRD R35: the auto-discovered
// workspace overlay slug for this source. The overlay sits in the
// same org as the source; its repo name is the basename of the
// resolved config dir plus "-overlay" (whole-repo cases use the
// source repo name; subpath cases use the subpath's last segment).
// The overlay's own subpath is empty (the overlay is treated as a
// whole-repo source per PRD R35); its ref inherits from the source
// (matching the team config's tracking).
func (s Source) OverlayDerivedSource() Source {
	overlayRepo := s.Repo + "-overlay"
	if s.Subpath != "" {
		base := lastPathSegment(s.Subpath)
		if base != "" {
			overlayRepo = base + "-overlay"
		}
	}
	return Source{
		Host:  s.Host,
		Owner: s.Owner,
		Repo:  overlayRepo,
		Ref:   s.Ref,
	}
}

// DisplayRef returns the ref string suitable for human-readable
// output. Returns "(default branch)" when Ref is empty so `niwa
// status` can show the moving-target nature explicitly.
func (s Source) DisplayRef() string {
	if s.Ref == "" {
		return "(default branch)"
	}
	return s.Ref
}

// IsGitHub reports whether the source host is github.com (the
// implicit default). Used by callers to decide between the GitHub
// tarball fast path and the git-clone fallback.
func (s Source) IsGitHub() bool {
	return s.Host == "" || s.Host == DefaultHost
}

func lastPathSegment(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
