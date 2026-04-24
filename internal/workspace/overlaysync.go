package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/source"
)

// EnsureOverlaySnapshot maintains a snapshot at dir for the overlay
// identified by urlSlug. wasFreshClone is true when we attempted a
// first-time materialization (no marker, no .git/), so callers can
// distinguish "overlay repo doesn't exist — silent skip" (wasFreshClone)
// from "previously-cloned overlay failed to refresh — hard error"
// (existing snapshot, fetch failed).
//
// urlSlug accepts the same shapes as init's --from: org/repo,
// host/owner/repo, full HTTPS URL, or SSH URL. Internally normalized
// via source.Parse so the snapshot writer can dispatch on host.
//
// fetcher may be nil for non-GitHub overlays (the fallback uses git).
// GitHub overlays without a fetcher result in a no-op + nil — the
// caller's existing-snapshot retains until a fetch client is wired.
func EnsureOverlaySnapshot(ctx context.Context, urlSlug, dir string, fetcher FetchClient, reporter *Reporter) (wasFreshClone bool, err error) {
	src, parseErr := parseOverlaySlug(urlSlug)
	if parseErr != nil {
		return true, fmt.Errorf("overlay: parse %q: %w", urlSlug, parseErr)
	}

	hasMarker := provenanceMarkerExists(dir)
	hasGit := dotGitExists(dir)

	switch {
	case hasMarker:
		// Existing snapshot: refresh via the standard pipeline.
		return false, EnsureConfigSnapshot(ctx, dir, fetcher, reporter)
	case hasGit:
		// Legacy working tree: R28 lazy conversion via the standard pipeline.
		return false, EnsureConfigSnapshot(ctx, dir, fetcher, reporter)
	default:
		// Fresh materialization. Make sure the parent dir exists; the
		// snapshot writer creates dir itself via the atomic swap.
		// Pass urlSlug verbatim as the marker's source_url so the
		// URL-change gate in apply matches what the registry stores.
		if mkErr := os.MkdirAll(filepath.Dir(dir), 0o755); mkErr != nil {
			return true, fmt.Errorf("overlay: ensure parent: %w", mkErr)
		}
		return true, MaterializeFromSource(ctx, src, urlSlug, dir, fetcher, reporter)
	}
}

// ParseSourceURL accepts the historical clone-input shapes (org/repo
// shorthand, full HTTPS URL, SSH URL, file:// for local fakes) and
// converts them into a source.Source. Used by init's --from path and
// the overlay snapshot writer.
func ParseSourceURL(slug string) (source.Source, error) {
	return parseOverlaySlug(slug)
}

// parseOverlaySlug is the implementation; ParseSourceURL is the
// public alias that documents non-overlay callers.
func parseOverlaySlug(slug string) (source.Source, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return source.Source{}, fmt.Errorf("empty slug")
	}

	// file:// raw URL: store verbatim in Host so the fallback path
	// can clone it without trying to synthesize https://. Synthesize
	// Owner/Repo from the trailing path components so the provenance
	// marker carries a stable identity (the marker schema requires
	// non-empty owner and repo).
	if strings.HasPrefix(slug, "file://") {
		owner, repo := splitLocalPath(strings.TrimPrefix(slug, "file://"))
		return source.Source{Host: slug, Owner: owner, Repo: repo}, nil
	}

	// Bare local path with no scheme: same treatment as file://.
	if strings.HasPrefix(slug, "/") && !strings.Contains(slug, ":") {
		owner, repo := splitLocalPath(slug)
		return source.Source{Host: "file://" + slug, Owner: owner, Repo: repo}, nil
	}

	// org/repo shorthand passes through to source.Parse directly.
	if !strings.Contains(slug, "://") && !strings.HasPrefix(slug, "git@") {
		return source.Parse(slug)
	}

	// SSH form: git@host:owner/repo[.git]
	if strings.HasPrefix(slug, "git@") {
		colon := strings.IndexByte(slug, ':')
		if colon < 0 {
			return source.Source{}, fmt.Errorf("malformed SSH URL %q", slug)
		}
		host := strings.TrimPrefix(slug[:colon], "git@")
		ownerRepo := strings.TrimSuffix(slug[colon+1:], ".git")
		parts := strings.SplitN(ownerRepo, "/", 2)
		if len(parts) != 2 {
			return source.Source{}, fmt.Errorf("malformed SSH path %q", ownerRepo)
		}
		s := source.Source{Owner: parts[0], Repo: parts[1]}
		if host != "github.com" {
			s.Host = host
		}
		return s, nil
	}

	// HTTPS / http / git:// form. Strip the scheme + host + .git
	// suffix and feed the owner/repo (plus optional host) to source.Parse.
	withoutScheme := slug
	for _, scheme := range []string{"https://", "http://", "git://"} {
		if strings.HasPrefix(withoutScheme, scheme) {
			withoutScheme = strings.TrimPrefix(withoutScheme, scheme)
			break
		}
	}
	withoutScheme = strings.TrimSuffix(withoutScheme, ".git")
	parts := strings.SplitN(withoutScheme, "/", 3)
	if len(parts) < 3 {
		return source.Source{}, fmt.Errorf("URL %q missing owner/repo", slug)
	}
	host, owner, repo := parts[0], parts[1], parts[2]
	s := source.Source{Owner: owner, Repo: repo}
	if host != "github.com" {
		s.Host = host
	}
	return s, nil
}

// splitLocalPath returns synthetic (owner, repo) values for a local
// filesystem path, used when the source is a bare URL or path with
// no inherent owner/repo. The trailing path component (sans .git)
// becomes the repo; its parent becomes the owner. Single-segment
// paths get owner="local". Always returns non-empty values so the
// provenance marker schema is satisfied.
func splitLocalPath(p string) (owner, repo string) {
	p = strings.TrimRight(p, "/")
	repo = strings.TrimSuffix(filepath.Base(p), ".git")
	if repo == "" || repo == "/" || repo == "." {
		repo = "local"
	}
	parent := filepath.Base(filepath.Dir(p))
	if parent == "" || parent == "/" || parent == "." {
		owner = "local"
	} else {
		owner = parent
	}
	return owner, repo
}

// HeadSHA returns the current HEAD commit SHA recorded for the
// snapshot at dir. Reads the provenance marker; falls back to
// `git -C dir rev-parse HEAD` when the dir is still a legacy working
// tree (covers the apply window between Issue 4 and the Issue 5 lazy
// migration).
func HeadSHA(dir string) (string, error) {
	if provenanceMarkerExists(dir) {
		prov, err := ReadProvenance(dir)
		if err != nil {
			return "", fmt.Errorf("reading provenance: %w", err)
		}
		return prov.ResolvedCommit, nil
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
