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

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/source"
)

// driftCheckBackoff is the wait schedule used by headCommitWithRetry.
// Its length determines the number of retries (e.g., len()==3 means
// up to 3 retries on top of the initial attempt for 4 attempts total).
// Tests override this slice to skip real waits.
var driftCheckBackoff = []time.Duration{
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
}

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
	_, _, err := EnsureConfigSnapshotWithStatus(ctx, configDir, config.TeamConfigMarkerSet(), fetcher, reporter)
	return err
}

// EnsureConfigSnapshotWithStatus is the same as EnsureConfigSnapshot but
// returns whether a legacy working-tree-to-snapshot conversion happened
// during this call (PRD R28) and which rank the snapshot now uses
// (1 = rank-1 layout, 2 = deprecated rank-2 layout, 0 = no rank
// resolution performed e.g. refresh or no source). Callers emit the
// one-time conversion notice (converted) and the rank-2 deprecation
// notice (rank == 2) from the returned values; simpler callers use
// the EnsureConfigSnapshot wrapper.
//
// markers tells the probe pipeline which marker set identifies the
// rank-1 location and the rank-2 root file when src.Subpath is empty.
// Pass config.TeamConfigMarkerSet() for the workspace's team config,
// config.OverlayMarkerSet() for the auto-discovered overlay.
func EnsureConfigSnapshotWithStatus(ctx context.Context, configDir string, markers config.MarkerSet, fetcher FetchClient, reporter *Reporter) (converted bool, rank int, err error) {
	if configDir == "" {
		return false, 0, errors.New("EnsureConfigSnapshot: configDir is empty")
	}

	hasMarker := provenanceMarkerExists(configDir)
	hasGit := dotGitExists(configDir)

	switch {
	case hasMarker:
		// Case 1: snapshot. Drift-check + refresh. The inner function
		// decides whether nil fetcher is acceptable based on whether
		// the marker's source is GitHub (needs fetcher) or anything
		// else (uses git-clone fallback).
		refreshRank, refreshErr := refreshSnapshot(ctx, configDir, markers, fetcher, reporter)
		return false, refreshRank, refreshErr
	case hasGit:
		// Case 2: legacy working tree. R28 lazy conversion. Same
		// fetcher-may-be-nil semantics as case 1.
		return lazyConvertWorkingTree(ctx, configDir, markers, fetcher, reporter)
	default:
		// Case 3: nothing to maintain.
		return false, 0, nil
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
//
// Refresh does NOT re-probe: the previously-resolved subpath flows
// verbatim into src.Subpath, so materializeAndSwap takes the
// explicit-subpath bypass. If the source repo's layout changes (e.g.,
// maintainer migrates from rank-2 to rank-1), the user must use
// `niwa apply --force` (Issue 7) to re-discover.
//
// Returns the rank reflected by the refreshed snapshot (derived from
// the provenance subpath: empty → 2, non-empty → 1) so callers can
// emit the one-time deprecation notice.
func refreshSnapshot(ctx context.Context, configDir string, markers config.MarkerSet, fetcher FetchClient, reporter *Reporter) (int, error) {
	prov, err := ReadProvenance(configDir)
	if err != nil {
		return 0, fmt.Errorf("EnsureConfigSnapshot: %w", err)
	}

	src := source.Source{
		Host:    prov.Host,
		Owner:   prov.Owner,
		Repo:    prov.Repo,
		Subpath: prov.Subpath,
		Ref:     prov.Ref,
	}

	// The provenance marker schema has no explicit rank field. Subpath
	// presence is the canonical signal: rank-1 materialization records
	// ".niwa" (resolved subpath), rank-2 records "". Future additions
	// of a rank field would supersede this derivation.
	priorRank := 1
	if prov.Subpath == "" {
		priorRank = 2
	}

	if !src.IsGitHub() {
		// Non-GitHub source: no per-host cheap drift check in v1, so we
		// always re-materialize from a shallow clone. The old snapshot
		// stays put if the clone fails.
		_, err := materializeAndSwap(ctx, configDir, src, prov.SourceURL, markers, fetcher, reporter)
		return priorRank, err
	}

	if fetcher == nil {
		// GitHub source but no fetcher available (test fixture or
		// network-unreachable apply). Keep the cached snapshot and
		// don't error.
		return priorRank, nil
	}

	oid, status, err := headCommitWithRetry(ctx, fetcher, src, reporter)
	if err != nil {
		// Network error or similar: per PRD R21, continue with cached
		// snapshot and warn.
		if reporter != nil {
			reporter.Warn("could not refresh config snapshot for %s: %v; using cached snapshot fetched at %s",
				prov.SourceURL, err, prov.FetchedAt.Format(time.RFC3339))
		}
		return priorRank, nil
	}
	if status == 304 || (oid != "" && oid == prov.ResolvedCommit) {
		// No drift. Update fetched_at so the marker reflects the
		// successful drift check.
		prov.FetchedAt = time.Now().UTC()
		if writeErr := WriteProvenance(configDir, prov); writeErr != nil {
			return 0, fmt.Errorf("EnsureConfigSnapshot: refresh marker: %w", writeErr)
		}
		return priorRank, nil
	}

	// Drift detected: fetch fresh, materialize, swap.
	_, err = materializeAndSwap(ctx, configDir, src, prov.SourceURL, markers, fetcher, reporter)
	return priorRank, err
}

// isTransientDriftError classifies a HeadCommit error as transient
// (worth retrying) or permanent. Transient covers transport failures
// (status == 0), the GitHub 5xx subset known to recover quickly, and
// 429 rate-limit responses (which clear after the documented backoff
// window and show up in normal use under burst conditions). Other
// non-2xx responses (401/403/404/500/etc.) are treated as permanent
// and surface to the caller on the first attempt.
func isTransientDriftError(err error, status int) bool {
	if err == nil {
		return false
	}
	switch status {
	case 0, 429, 502, 503, 504:
		return true
	}
	return false
}

// headCommitWithRetry wraps fetcher.HeadCommit with a short retry loop
// over driftCheckBackoff. Permanent failures and successes return on
// the first attempt; transient failures retry up to len(driftCheckBackoff)
// times, emitting a replaceable Reporter.Status note between attempts so
// the user sees why apply is briefly stalled. The final error returned
// matches the most recent attempt and feeds the existing warn-and-cache
// fallback unchanged. An empty src.Ref is treated as "HEAD", matching
// the GitHub API default.
func headCommitWithRetry(ctx context.Context, fetcher FetchClient, src source.Source, reporter *Reporter) (oid string, status int, err error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	attempts := len(driftCheckBackoff) + 1
	for i := 0; i < attempts; i++ {
		oid, _, status, err = fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, "")
		if !isTransientDriftError(err, status) || i == attempts-1 {
			return oid, status, err
		}
		if reporter != nil {
			reporter.Status(fmt.Sprintf("retrying drift check for %s/%s (retry %d of %d)...",
				src.Owner, src.Repo, i+1, attempts-1))
		}
		select {
		case <-time.After(driftCheckBackoff[i]):
		case <-ctx.Done():
			return oid, status, ctx.Err()
		}
	}
	return oid, status, err
}

// lazyConvertWorkingTree is the case-2 path: discover the source URL
// from `git remote get-url origin`, fetch a fresh snapshot, and
// atomically replace the working tree.
//
// Returns converted=true when the conversion actually happened. The
// caller is responsible for emitting the PRD R28 one-time `note:`
// (this function does not call reporter.Log directly because emission
// must be gated on DisclosedNotices to fire once per workspace, not
// once per apply).
func lazyConvertWorkingTree(ctx context.Context, configDir string, markers config.MarkerSet, fetcher FetchClient, reporter *Reporter) (converted bool, rank int, err error) {
	originURL, originErr := readGitOrigin(configDir)
	if originErr != nil {
		// No origin remote means this is a local-only workspace that
		// happens to be tracked in git. Don't attempt conversion.
		return false, 0, nil
	}

	src, parseErr := parseRemoteURLToSource(originURL)
	if parseErr != nil {
		// Couldn't parse the origin into a Source we know how to
		// fetch. Leave the working tree alone — there's no other sync
		// path now; user can fix the remote and rerun apply.
		return false, 0, nil
	}

	if !src.IsGitHub() && fetcher == nil {
		// Non-GitHub source can run the fallback even without a fetcher,
		// but if we have neither a GitHub source nor a fetcher there's
		// nothing usable. Bail without converting.
		return false, 0, nil
	}
	if src.IsGitHub() && fetcher == nil {
		// Need a fetch client for the GitHub path. Leave the working
		// tree alone; user can rerun apply after wiring GH_TOKEN.
		return false, 0, nil
	}

	// Pass originURL verbatim as the marker's source_url so the
	// URL-change gate matches what the registry stores. src.String()
	// would re-render synthesized owner/repo (e.g. for file:// hosts)
	// in a non-roundtripping form.
	resolvedRank, swapErr := materializeAndSwap(ctx, configDir, src, originURL, markers, fetcher, reporter)
	if swapErr != nil {
		// Conversion failed; leave the working tree alone so the next
		// apply can retry. No other safety net to fall through to now
		// that SyncConfigDir is gone.
		return false, 0, nil
	}

	return true, resolvedRank, nil
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
func MaterializeFromSource(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher FetchClient, reporter *Reporter) (rank int, err error) {
	if sourceURL == "" {
		sourceURL = src.String()
	}
	return materializeAndSwap(ctx, configDir, src, sourceURL, markers, fetcher, reporter)
}

// materializeAndSwap stages a fresh snapshot at <configDir>.next/,
// writes the provenance marker, and atomically swaps it into place.
// Used by the refresh path, the lazy-conversion path, and the public
// MaterializeFromSource entry point.
//
// Four entry conditions, two dispatch axes (host x subpath):
//
//  1. GitHub + empty src.Subpath: tarball fetch, probe headers
//     against markers, then extract resolved subpath. rank reflects
//     probe result (1 or 2).
//  2. GitHub + explicit src.Subpath: tarball fetch, extract verbatim
//     under src.Subpath. rank=1 by convention (explicit subpaths
//     never trigger rank-2 deprecation notice).
//  3. Non-GitHub + empty src.Subpath: shallow clone, probe filesystem
//     against markers, then copy resolved subpath. rank reflects
//     probe result (1 or 2).
//  4. Non-GitHub + explicit src.Subpath: shallow clone, copy
//     src.Subpath verbatim. rank=1 by convention.
//
// markers selects the marker set the probe pass uses (cases 1 and
// 3). Pass config.TeamConfigMarkerSet() for the workspace's team
// config; config.OverlayMarkerSet() for the auto-discovered overlay.
// Ignored in cases 2 and 4.
//
// Returns rank int: 1 for rank-1 layout, 2 for rank-2 (deprecated
// whole-repo) layout. The caller emits the rank-2 deprecation
// notice via the workspace disclosure helper (Issue 5). 0 only on
// error.
func materializeAndSwap(ctx context.Context, configDir string, src source.Source, sourceURL string, markers config.MarkerSet, fetcher FetchClient, reporter *Reporter) (rank int, err error) {
	parent := filepath.Dir(configDir)
	staging := configDir + ".next"

	// Idempotent cleanup of stale staging.
	if err := safeRemoveAll(staging); err != nil {
		return 0, fmt.Errorf("EnsureConfigSnapshot: preflight cleanup: %w", err)
	}

	if err := os.MkdirAll(staging, 0o755); err != nil {
		return 0, fmt.Errorf("EnsureConfigSnapshot: create staging: %w", err)
	}

	var (
		oid             string
		mechanism       string
		redirectNotice  *github.RenameRedirect
		resolvedSubpath string
	)

	if src.IsGitHub() {
		if fetcher == nil {
			_ = safeRemoveAll(staging)
			return 0, fmt.Errorf("EnsureConfigSnapshot: GitHub source %s requires a fetch client", sourceURL)
		}
		resolvedOID, redirect, probedSubpath, probedRank, ghErr := materializeFromGitHub(ctx, src, sourceURL, staging, markers, fetcher)
		if ghErr != nil {
			_ = safeRemoveAll(staging)
			return 0, ghErr
		}
		oid = resolvedOID
		redirectNotice = redirect
		mechanism = FetchMechanismGitHubTarball
		resolvedSubpath = probedSubpath
		rank = probedRank
	} else {
		resolvedOID, probedSubpath, probedRank, fbErr := materializeFromFallback(ctx, src, staging, markers)
		if fbErr != nil {
			_ = safeRemoveAll(staging)
			return 0, fmt.Errorf("EnsureConfigSnapshot: %w", fbErr)
		}
		oid = resolvedOID
		mechanism = FetchMechanismGitClone
		resolvedSubpath = probedSubpath
		rank = probedRank
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
		Subpath:        resolvedSubpath,
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
		return rank, fmt.Errorf("EnsureConfigSnapshot: write marker: %w", err)
	}

	// Make sure the parent dir exists for the swap.
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return rank, fmt.Errorf("EnsureConfigSnapshot: ensure parent: %w", err)
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
		return rank, fmt.Errorf("EnsureConfigSnapshot: preserve instance state: %w", err)
	}

	if err := SwapSnapshotAtomic(configDir, staging); err != nil {
		_ = safeRemoveAll(staging)
		return rank, fmt.Errorf("EnsureConfigSnapshot: %w", err)
	}
	return rank, nil
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
// of materializeAndSwap for GitHub sources. When src.Subpath is empty
// the function runs the probe pipeline (ProbeAndExtractSubpath) to
// resolve rank-1 vs rank-2 layout against the supplied marker set.
// When src.Subpath is non-empty the explicit-subpath bypass uses
// ExtractSubpath directly and returns the input subpath verbatim
// with rank=1 (explicit subpaths never fire the rank-2 deprecation
// notice — see materializeAndSwap doc case 2).
//
// Returns the resolved commit oid (best-effort), any rename-redirect
// observed, the resolved subpath (relative to repo root), and the
// resulting rank.
func materializeFromGitHub(ctx context.Context, src source.Source, sourceURL, staging string, markers config.MarkerSet, fetcher FetchClient) (string, *github.RenameRedirect, string, int, error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}

	body, _, status, redirect, err := fetcher.FetchTarball(ctx, src.Owner, src.Repo, ref, "")
	if err != nil {
		return "", nil, "", 0, fmt.Errorf("EnsureConfigSnapshot: fetch %s: %w", sourceURL, err)
	}
	if body != nil {
		defer body.Close()
	}
	if status == 304 {
		// Shouldn't happen on an etag-less request; treat as a no-op
		// by leaving the staging dir empty. Caller will fail on empty
		// WriteProvenance if it reaches that point, but the swap
		// won't promote.
		return "", nil, "", 0, fmt.Errorf("EnsureConfigSnapshot: fetch %s returned 304 unexpectedly", sourceURL)
	}
	if status != 200 {
		return "", nil, "", 0, fmt.Errorf("EnsureConfigSnapshot: fetch %s returned %d", sourceURL, status)
	}

	var (
		resolvedSubpath string
		rank            int
	)
	if src.Subpath == "" {
		// Discovery mode: probe the tarball, decide rank, then extract.
		sp, r, _, probeErr := github.ProbeAndExtractSubpath(body, markers, config.RankDecider, staging)
		if probeErr != nil {
			return "", nil, "", 0, fmt.Errorf("EnsureConfigSnapshot: probe: %w", probeErr)
		}
		resolvedSubpath = sp
		rank = r
	} else {
		// Explicit-subpath bypass: skip probe, extract verbatim.
		if err := github.ExtractSubpath(body, src.Subpath, staging); err != nil {
			return "", nil, "", 0, fmt.Errorf("EnsureConfigSnapshot: extract: %w", err)
		}
		resolvedSubpath = src.Subpath
		rank = 1
	}

	// Resolve commit oid for the marker. Best effort: if the HeadCommit
	// call fails we still want to swap with what we have.
	oid := ""
	if commitOID, _, _, headErr := fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, ""); headErr == nil {
		oid = commitOID
	}

	return oid, redirect, resolvedSubpath, rank, nil
}

// materializeFromFallback handles the non-GitHub branch. When
// src.Subpath is empty it runs ProbeAndFetchSubpath (probe-aware
// clone + selective copy). When non-empty it calls
// FetchSubpathViaGitClone verbatim. Returns oid, resolved subpath,
// rank.
func materializeFromFallback(ctx context.Context, src source.Source, staging string, markers config.MarkerSet) (string, string, int, error) {
	if src.Subpath == "" {
		sp, rank, _, oid, err := ProbeAndFetchSubpath(ctx, src, markers, config.RankDecider, staging)
		if err != nil {
			return "", "", 0, err
		}
		return oid, sp, rank, nil
	}
	oid, err := FetchSubpathViaGitClone(ctx, src, staging)
	if err != nil {
		return "", "", 0, err
	}
	return oid, src.Subpath, 1, nil
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

	// file:// URL: defer to ParseSourceURL which understands the scheme
	// and synthesizes Owner/Repo from the path. Used by lazy conversion
	// of legacy working trees that were cloned from a local bare repo
	// (test fixtures, intentional offline workflows).
	if strings.HasPrefix(remote, "file://") {
		return ParseSourceURL(remote)
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
