package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/source"
)

// FetchClient is the interface EnsureConfigSnapshot consumes for
// GitHub fetches. Production wires this to *github.APIClient; tests
// substitute a fake.
type FetchClient interface {
	HeadCommit(ctx context.Context, owner, repo, ref, etag string) (oid, newETag string, statusCode int, err error)
	FetchTarball(ctx context.Context, owner, repo, ref, etag string) (body io.ReadCloser, newETag string, statusCode int, redirect *github.RenameRedirect, err error)
}

// EnsureConfigSnapshot maintains the snapshot at configDir. It is the
// new-model entry point that runs as a pre-step before apply's
// existing SyncConfigDir / CloneOrSyncOverlay calls.
//
// Three cases the function dispatches on:
//
//  1. Provenance marker present → snapshot exists, drift-check and
//     refresh if upstream commit oid changed.
//  2. `.git/` present, no marker → legacy working tree, perform PRD
//     R28 lazy conversion to a snapshot in place.
//  3. Neither → no-op (local-only workspace, no remote source to track).
//
// The function NEVER mutates the previous snapshot on failure: any
// extraction or fetch error leaves the existing configDir intact and
// returns the error so the caller can decide whether to surface it
// (configDir → hard) or silently skip (overlay → soft per R37).
//
// reporter receives any user-visible notes (one-time conversion
// notice, drift warning when network is unreachable, etc.).
//
// fetcher may be nil — in that case EnsureConfigSnapshot does
// nothing for cases 1 and 2 and returns nil. This lets unit tests
// that don't exercise the fetch path skip wiring a fake client.
func EnsureConfigSnapshot(ctx context.Context, configDir string, fetcher FetchClient, reporter *Reporter) error {
	if configDir == "" {
		return errors.New("EnsureConfigSnapshot: configDir is empty")
	}

	hasMarker := provenanceMarkerExists(configDir)
	hasGit := dotGitExists(configDir)

	switch {
	case hasMarker:
		// Case 1: snapshot. Drift-check + refresh.
		if fetcher == nil {
			return nil
		}
		return refreshSnapshot(ctx, configDir, fetcher, reporter)
	case hasGit:
		// Case 2: legacy working tree. R28 lazy conversion.
		if fetcher == nil {
			return nil
		}
		return lazyConvertWorkingTree(ctx, configDir, fetcher, reporter)
	default:
		// Case 3: nothing to maintain.
		return nil
	}
}

func provenanceMarkerExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ProvenanceFile))
	return err == nil
}

func dotGitExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// refreshSnapshot is the case-1 path: read marker, drift-check via
// HeadCommit, and re-fetch if changed.
func refreshSnapshot(ctx context.Context, configDir string, fetcher FetchClient, reporter *Reporter) error {
	prov, err := ReadProvenance(configDir)
	if err != nil {
		return fmt.Errorf("EnsureConfigSnapshot: %w", err)
	}

	src := source.Source{
		Host:    prov.Host,
		Owner:   prov.Owner,
		Repo:    prov.Repo,
		Subpath: prov.Subpath,
		Ref:     prov.Ref,
	}

	if !src.IsGitHub() {
		// Non-GitHub source: drift-check via git ls-remote, refresh via
		// git-clone fallback.
		return refreshSnapshotFallback(ctx, configDir, src, prov, reporter)
	}

	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	oid, _, status, err := fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, "")
	if err != nil {
		// Network error or similar: per PRD R21, continue with cached
		// snapshot and warn.
		if reporter != nil {
			reporter.Warn("could not refresh config snapshot for %s: %v; using cached snapshot fetched at %s",
				prov.SourceURL, err, prov.FetchedAt.Format(time.RFC3339))
		}
		return nil
	}
	if status == 304 || (oid != "" && oid == prov.ResolvedCommit) {
		// No drift. Update fetched_at so the marker reflects the
		// successful drift check.
		prov.FetchedAt = time.Now().UTC()
		if writeErr := WriteProvenance(configDir, prov); writeErr != nil {
			return fmt.Errorf("EnsureConfigSnapshot: refresh marker: %w", writeErr)
		}
		return nil
	}

	// Drift detected: fetch fresh, materialize, swap.
	return materializeAndSwap(ctx, configDir, src, prov.SourceURL, fetcher, reporter)
}

// lazyConvertWorkingTree is the case-2 path: discover the source URL
// from `git remote get-url origin`, fetch a fresh snapshot, and
// atomically replace the working tree.
func lazyConvertWorkingTree(ctx context.Context, configDir string, fetcher FetchClient, reporter *Reporter) error {
	originURL, err := readGitOrigin(configDir)
	if err != nil {
		// No origin remote means this is a local-only workspace that
		// happens to be tracked in git. Don't attempt conversion.
		return nil
	}

	src, err := parseRemoteURLToSource(originURL)
	if err != nil {
		// Couldn't parse the origin into a Source we know how to
		// fetch. Leave the working tree alone; legacy SyncConfigDir
		// will continue to handle it.
		return nil
	}

	if !src.IsGitHub() {
		// Non-GitHub working trees stay on the legacy git pull path
		// for now (per PRD scope: GitHub-first-class fast path; non-
		// GitHub uses the fallback which is what SyncConfigDir already
		// is for legacy working trees).
		return nil
	}

	if err := materializeAndSwap(ctx, configDir, src, src.String(), fetcher, reporter); err != nil {
		// Conversion failed; legacy SyncConfigDir runs as a safety net.
		return nil
	}

	if reporter != nil {
		reporter.Log("note: %s converted from working tree to snapshot. Manual edits inside this directory will no longer persist.", configDir)
	}
	return nil
}

// materializeAndSwap stages a fresh snapshot at <configDir>.next/,
// writes the provenance marker, and atomically swaps it into place.
// Used by both the refresh and lazy-conversion paths.
func materializeAndSwap(ctx context.Context, configDir string, src source.Source, sourceURL string, fetcher FetchClient, reporter *Reporter) error {
	parent := filepath.Dir(configDir)
	staging := configDir + ".next"

	// Idempotent cleanup of stale staging.
	if err := safeRemoveAll(staging); err != nil {
		return fmt.Errorf("EnsureConfigSnapshot: preflight cleanup: %w", err)
	}

	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("EnsureConfigSnapshot: create staging: %w", err)
	}

	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}

	body, _, status, redirect, err := fetcher.FetchTarball(ctx, src.Owner, src.Repo, ref, "")
	if err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: fetch %s: %w", sourceURL, err)
	}
	if body != nil {
		defer body.Close()
	}
	if status == 304 {
		// Shouldn't happen on an etag-less request; treat as no-op.
		_ = safeRemoveAll(staging)
		return nil
	}
	if status != 200 {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: fetch %s returned %d", sourceURL, status)
	}

	if err := github.ExtractSubpath(body, src.Subpath, staging); err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: extract: %w", err)
	}

	// Resolve commit oid for the marker. Best effort: if the HeadCommit
	// call fails we still want to swap with what we have.
	oid := ""
	if commitOID, _, _, headErr := fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, ""); headErr == nil {
		oid = commitOID
	}

	// Surface rename redirect via reporter for one-time visibility.
	if redirect != nil && reporter != nil {
		reporter.Log("note: source repo renamed from %s/%s to %s/%s; update your registry to avoid future redirects",
			redirect.OldOwner, redirect.OldRepo, redirect.NewOwner, redirect.NewRepo)
	}

	prov := Provenance{
		SourceURL:      sourceURL,
		Host:           src.Host,
		Owner:          src.Owner,
		Repo:           src.Repo,
		Subpath:        src.Subpath,
		Ref:            src.Ref,
		ResolvedCommit: oid,
		FetchedAt:      time.Now().UTC(),
		FetchMechanism: FetchMechanismGitHubTarball,
	}
	if oid == "" {
		// Marker requires resolved_commit; substitute a placeholder so
		// the write succeeds. Drift detection will refresh on the next
		// apply when HeadCommit succeeds.
		prov.ResolvedCommit = "unknown"
	}
	if err := WriteProvenance(staging, prov); err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: write marker: %w", err)
	}

	// Make sure the parent dir exists for the swap.
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("EnsureConfigSnapshot: ensure parent: %w", err)
	}

	if err := SwapSnapshotAtomic(configDir, staging); err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: %w", err)
	}
	return nil
}

// refreshSnapshotFallback handles the non-GitHub drift check and
// re-fetch via git-clone-and-copy. Currently a placeholder that
// no-ops; non-GitHub fallback is a v1.x candidate per PRD scope.
func refreshSnapshotFallback(ctx context.Context, configDir string, src source.Source, prov Provenance, reporter *Reporter) error {
	_ = ctx
	_ = configDir
	_ = src
	_ = prov
	_ = reporter
	return nil
}

func readGitOrigin(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// parseRemoteURLToSource recognizes https://, git@, and other git
// URL shapes and produces a Source with empty Subpath/Ref. Used by
// the lazy-conversion path to derive the new-model identity from a
// legacy working tree's origin URL.
func parseRemoteURLToSource(remote string) (source.Source, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return source.Source{}, errors.New("empty remote")
	}

	// SSH form: git@host:owner/repo[.git]
	if strings.HasPrefix(remote, "git@") {
		colon := strings.IndexByte(remote, ':')
		if colon < 0 {
			return source.Source{}, fmt.Errorf("malformed SSH remote %q", remote)
		}
		host := strings.TrimPrefix(remote[:colon], "git@")
		path := strings.TrimSuffix(remote[colon+1:], ".git")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return source.Source{}, fmt.Errorf("malformed SSH path %q", path)
		}
		s := source.Source{Owner: parts[0], Repo: parts[1]}
		if host != "github.com" {
			s.Host = host
		}
		return s, nil
	}

	// HTTPS / http / git:// form.
	u, err := url.Parse(remote)
	if err != nil {
		return source.Source{}, fmt.Errorf("parse remote URL %q: %w", remote, err)
	}
	if u.Host == "" {
		return source.Source{}, fmt.Errorf("remote URL %q has no host", remote)
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		return source.Source{}, fmt.Errorf("remote URL %q has no owner/repo", remote)
	}
	s := source.Source{Owner: parts[0], Repo: parts[1]}
	if u.Host != "github.com" {
		s.Host = u.Host
	}
	return s, nil
}
