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
// only sync strategy for config dirs in the workspace-config-sources
// model — the legacy git-pull primitives have been retired.
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
		// Case 1: snapshot. Drift-check + refresh. The inner function
		// decides whether nil fetcher is acceptable based on whether
		// the marker's source is GitHub (needs fetcher) or anything
		// else (uses git-clone fallback).
		return refreshSnapshot(ctx, configDir, fetcher, reporter)
	case hasGit:
		// Case 2: legacy working tree. R28 lazy conversion. Same
		// fetcher-may-be-nil semantics as case 1.
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
		// Non-GitHub source: no per-host cheap drift check in v1, so we
		// always re-materialize from a shallow clone. The old snapshot
		// stays put if the clone fails.
		return materializeAndSwap(ctx, configDir, src, prov.SourceURL, fetcher, reporter)
	}

	if fetcher == nil {
		// GitHub source but no fetcher available (test fixture or
		// network-unreachable apply). Keep the cached snapshot and
		// don't error.
		return nil
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
		// fetch. Leave the working tree alone — there's no other sync
		// path now; user can fix the remote and rerun apply.
		return nil
	}

	if !src.IsGitHub() && fetcher == nil {
		// Non-GitHub source can run the fallback even without a fetcher,
		// but if we have neither a GitHub source nor a fetcher there's
		// nothing usable. Bail without converting.
		return nil
	}
	if src.IsGitHub() && fetcher == nil {
		// Need a fetch client for the GitHub path. Leave the working
		// tree alone; user can rerun apply after wiring GH_TOKEN.
		return nil
	}

	if err := materializeAndSwap(ctx, configDir, src, src.String(), fetcher, reporter); err != nil {
		// Conversion failed; leave the working tree alone so the next
		// apply can retry. No other safety net to fall through to now
		// that SyncConfigDir is gone.
		return nil
	}

	if reporter != nil {
		reporter.Log("note: %s converted from working tree to snapshot. Manual edits inside this directory will no longer persist.", configDir)
	}
	return nil
}

// MaterializeFromSource stages a fresh snapshot at configDir from src
// and promotes it atomically. Used by init (first-time clone via
// `--from`) and `niwa config set global` (first-time personal overlay
// clone) where no marker or working tree exists yet.
//
// sourceURL is the canonical user-facing string the caller wants in
// the provenance marker (and that the registry will store). Pass the
// user's original `--from` value or registry slug verbatim — Source
// methods like String() don't round-trip for raw URL inputs (file://,
// https://, git@). Empty sourceURL falls back to src.String() for
// callers who only have a typed source (e.g. drift refresh after the
// marker is already in sync).
//
// fetcher may be nil for non-GitHub sources (the fallback path only
// needs `git`). GitHub sources require a non-nil fetcher.
func MaterializeFromSource(ctx context.Context, src source.Source, sourceURL, configDir string, fetcher FetchClient, reporter *Reporter) error {
	if sourceURL == "" {
		sourceURL = src.String()
	}
	return materializeAndSwap(ctx, configDir, src, sourceURL, fetcher, reporter)
}

// materializeAndSwap stages a fresh snapshot at <configDir>.next/,
// writes the provenance marker, and atomically swaps it into place.
// Used by the refresh path, the lazy-conversion path, and the public
// MaterializeFromSource entry point.
//
// Dispatches on src.IsGitHub(): GitHub sources use the tarball +
// ExtractSubpath pipeline; everything else uses the git-clone
// fallback in fallback.go.
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

	var (
		oid            string
		mechanism      string
		redirectNotice *github.RenameRedirect
	)

	if src.IsGitHub() {
		if fetcher == nil {
			_ = safeRemoveAll(staging)
			return fmt.Errorf("EnsureConfigSnapshot: GitHub source %s requires a fetch client", sourceURL)
		}
		resolvedOID, redirect, err := materializeFromGitHub(ctx, src, sourceURL, staging, fetcher)
		if err != nil {
			_ = safeRemoveAll(staging)
			return err
		}
		oid = resolvedOID
		redirectNotice = redirect
		mechanism = FetchMechanismGitHubTarball
	} else {
		resolvedOID, err := FetchSubpathViaGitClone(ctx, src, staging)
		if err != nil {
			_ = safeRemoveAll(staging)
			return fmt.Errorf("EnsureConfigSnapshot: %w", err)
		}
		oid = resolvedOID
		mechanism = FetchMechanismGitClone
	}

	// Surface rename redirect via reporter for one-time visibility.
	if redirectNotice != nil && reporter != nil {
		reporter.Log("note: source repo renamed from %s/%s to %s/%s; update your registry to avoid future redirects",
			redirectNotice.OldOwner, redirectNotice.OldRepo, redirectNotice.NewOwner, redirectNotice.NewRepo)
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
		FetchMechanism: mechanism,
	}
	if oid == "" {
		// Marker requires resolved_commit; substitute a placeholder so
		// the write succeeds. Drift detection will refresh on the next
		// apply when oid resolution succeeds.
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

	// Preserve instance.json across the swap. The atomic swap rotates
	// the entire configDir, which would clobber niwa-managed per-instance
	// state if it weren't copied into staging first. This is the
	// intentional design (per the 2026-04-23 amendment to DESIGN
	// Decision 2): state lives in .niwa/ alongside config-source content,
	// and the snapshot writer carries it through the swap. Issue #74
	// (needs-design) tracks the longer-term refactor where niwa pulls
	// only files it knows about from upstream — at which point the
	// state-vs-source distinction at this seam becomes structurally
	// obvious — but v1 ships the simple carry-over.
	if err := preserveInstanceState(configDir, staging); err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: preserve instance state: %w", err)
	}

	if err := SwapSnapshotAtomic(configDir, staging); err != nil {
		_ = safeRemoveAll(staging)
		return fmt.Errorf("EnsureConfigSnapshot: %w", err)
	}
	return nil
}

// preserveInstanceState copies <configDir>/instance.json into staging
// when it exists, so the atomic swap doesn't clobber per-instance
// state. No-op when the file isn't present (fresh init, brand-new
// workspace). The closed set of niwa-local state files this helper
// carries currently has one member (StateFile). New niwa-local files
// added in the future extend this list explicitly, not via a naming
// pattern.
func preserveInstanceState(configDir, staging string) error {
	src := filepath.Join(configDir, StateFile)
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", src, err)
	}
	dst := filepath.Join(staging, StateFile)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// materializeFromGitHub handles the tarball fetch + extract portion
// of materializeAndSwap for GitHub sources. Returns the resolved
// commit oid (best-effort) and any rename-redirect observed.
func materializeFromGitHub(ctx context.Context, src source.Source, sourceURL, staging string, fetcher FetchClient) (string, *github.RenameRedirect, error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}

	body, _, status, redirect, err := fetcher.FetchTarball(ctx, src.Owner, src.Repo, ref, "")
	if err != nil {
		return "", nil, fmt.Errorf("EnsureConfigSnapshot: fetch %s: %w", sourceURL, err)
	}
	if body != nil {
		defer body.Close()
	}
	if status == 304 {
		// Shouldn't happen on an etag-less request; treat as a no-op
		// by leaving the staging dir empty. Caller will fail on empty
		// WriteProvenance if it reaches that point, but the swap
		// won't promote.
		return "", nil, fmt.Errorf("EnsureConfigSnapshot: fetch %s returned 304 unexpectedly", sourceURL)
	}
	if status != 200 {
		return "", nil, fmt.Errorf("EnsureConfigSnapshot: fetch %s returned %d", sourceURL, status)
	}

	if err := github.ExtractSubpath(body, src.Subpath, staging); err != nil {
		return "", nil, fmt.Errorf("EnsureConfigSnapshot: extract: %w", err)
	}

	// Resolve commit oid for the marker. Best effort: if the HeadCommit
	// call fails we still want to swap with what we have.
	oid := ""
	if commitOID, _, _, headErr := fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, ""); headErr == nil {
		oid = commitOID
	}

	return oid, redirect, nil
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
