package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// A repo: marketplace is a directory-source marketplace, and Claude Code keeps
// one global entry per marketplace name for the whole HOME. Registering it
// from an instance's own clone made that entry follow whichever instance
// registered last: instances loaded each other's plugin code, and destroying
// that instance left the entry naming a directory that no longer existed.
//
// So niwa keeps its own copy of each such repo at the workspace root, outside
// every instance, and registers that:
//
//	<workspace root>/.niwa/marketplaces/<repo>/
//	  repo.git/          bare clone; branches and tags fetched on every apply
//	  checkouts/<sha>/   one detached worktree per commit niwa has checked out
//	  current            symlink to checkouts/<sha>, replaced by rename
//
// The registered path runs through current, so it never moves. Moving to a new
// commit adds a new checkout beside the old one and then swaps the symlink, so
// a session reading the marketplace never sees a half-updated tree; the
// checkout it was reading stays on disk until the next swap.
const (
	managedMarketplacesDir = "marketplaces"
	managedRepoDir         = "repo.git"
	managedCheckoutsDir    = "checkouts"
	managedCurrentLink     = "current"
	managedLockFile        = ".lock"
)

// managedMarketplacesRoot is where the managed marketplace copies of the
// workspace at workspaceRoot live.
func managedMarketplacesRoot(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, StateDir, managedMarketplacesDir)
}

// managedMarketplaceCurrent is the stable path of the managed copy of repo:
// the current symlink, which every registration and settings file names.
func managedMarketplaceCurrent(workspaceRoot, repo string) string {
	return filepath.Join(managedMarketplacesRoot(workspaceRoot), repo, managedCurrentLink)
}

// managedMarketplaceIndex returns a copy of repoIndex in which every repo that
// has a managed marketplace copy maps to that copy's current checkout instead
// of the instance's clone. It is the index every marketplace consumer reads
// (settings, plugin skill delivery and the global registry reconcile), so they
// all name the same directory. A repo whose managed copy was never checked out
// keeps its instance clone: the settings still resolve, and the global entry is
// left alone (see reconcileMarketplaceRegistry).
func managedMarketplaceIndex(workspaceRoot string, repoIndex map[string]string) map[string]string {
	out := make(map[string]string, len(repoIndex))
	for name, dir := range repoIndex {
		out[name] = dir
		current := managedMarketplaceCurrent(workspaceRoot, name)
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			out[name] = current
		}
	}
	return out
}

// instanceMarketplaceIndex is managedMarketplaceIndex for an instance at
// instanceRoot. The managed copies live under the workspace root, which is the
// instance's parent only in a multi-instance workspace; in a single-instance
// layout the parent is some unrelated directory (see workspaceRootHoldsState),
// so the instance keeps its own clones there.
func instanceMarketplaceIndex(instanceRoot string, repoIndex map[string]string) map[string]string {
	workspaceRoot := filepath.Dir(instanceRoot)
	if !workspaceRootHoldsState(workspaceRoot) {
		return repoIndex
	}
	return managedMarketplaceIndex(workspaceRoot, repoIndex)
}

// repoMarketplaceRef splits a "repo:<name>/<path>" marketplace source into the
// repo name and the manifest path inside it. ok is false for any other source.
func repoMarketplaceRef(source string) (repo, manifest string, ok bool) {
	ref, found := strings.CutPrefix(source, repoRefPrefix)
	if !found {
		return "", "", false
	}
	repo, manifest, found = strings.Cut(ref, "/")
	if !found || repo == "" || manifest == "" {
		return "", "", false
	}
	return repo, manifest, true
}

// managedMarketplace is one repo niwa keeps a managed copy of.
type managedMarketplace struct {
	repo     string
	cloneURL string
	// ref is the configured track: "" for the remote's default branch,
	// otherwise a branch, tag or commit.
	ref string
	// manifests are the marketplace manifest paths inside the repo that a
	// checkout must carry before niwa switches to it.
	manifests []string
}

// managedMarketplaces collects the repo: marketplaces of cfg whose repo this
// instance classified, one entry per repo. A repo named by two marketplaces
// with different tracks keeps the first track and reports the second.
func managedMarketplaces(cfg *config.WorkspaceConfig, classified []ClassifiedRepo) ([]managedMarketplace, []string) {
	urls := make(map[string]string, len(classified))
	for _, cr := range classified {
		urls[cr.Repo.Name] = RepoCloneURL(cfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL)
	}

	var warnings []string
	byRepo := map[string]*managedMarketplace{}
	var order []string
	for _, mc := range cfg.Claude.Marketplaces {
		repo, manifest, ok := repoMarketplaceRef(mc.Source)
		if !ok {
			continue
		}
		url, ok := urls[repo]
		if !ok || url == "" {
			continue
		}
		m, seen := byRepo[repo]
		if !seen {
			m = &managedMarketplace{repo: repo, cloneURL: url, ref: mc.Track}
			byRepo[repo] = m
			order = append(order, repo)
		} else if m.ref != mc.Track {
			warnings = append(warnings, fmt.Sprintf(
				"marketplace %q: repo %q is already tracked at %q by another marketplace; keeping that",
				mc.Source, repo, displayRef(m.ref)))
		}
		m.manifests = append(m.manifests, manifest)
	}

	out := make([]managedMarketplace, 0, len(order))
	for _, repo := range order {
		out = append(out, *byRepo[repo])
	}
	return out, warnings
}

func displayRef(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return ref
}

// syncManagedMarketplaces brings the managed copy of every repo: marketplace
// to its configured ref. It holds the workspace's marketplace lock for the
// whole sync, so two applies running at once (parallel dispatches, say) take
// turns rather than fetching into one repository together.
//
// Every failure is a warning, never an error: the managed copy keeps the last
// commit it checked out, and the settings and the global entry keep naming it.
// A repo that was never checked out has no current link, so nothing is
// registered from it until a later apply succeeds.
func syncManagedMarketplaces(ctx context.Context, workspaceRoot string, marketplaces []managedMarketplace) []string {
	if len(marketplaces) == 0 {
		return nil
	}
	root := managedMarketplacesRoot(workspaceRoot)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return []string{fmt.Sprintf("could not create the managed marketplace directory %s: %v", root, err)}
	}
	unlock, err := acquireFileLock(filepath.Join(root, managedLockFile), "updating the managed marketplace copies")
	if err != nil {
		return []string{fmt.Sprintf("managed marketplace copies not updated: %v", err)}
	}
	defer unlock()

	var warnings []string
	for _, m := range marketplaces {
		if err := syncManagedMarketplace(ctx, root, m); err != nil {
			kept := "it is not registered until an apply can check it out"
			if _, statErr := os.Stat(managedMarketplaceCurrent(workspaceRoot, m.repo)); statErr == nil {
				kept = "keeping the copy already checked out"
			}
			warnings = append(warnings, fmt.Sprintf(
				"could not update the managed copy of marketplace repo %q to %s: %v; %s",
				m.repo, displayRef(m.ref), err, kept))
		}
	}
	return warnings
}

// syncManagedMarketplace fetches one repo's managed copy, checks out the
// configured ref beside the current checkout and swaps current to it. Nothing
// that is visible through current changes until the swap.
func syncManagedMarketplace(ctx context.Context, root string, m managedMarketplace) error {
	dir := filepath.Join(root, m.repo)
	bare := filepath.Join(dir, managedRepoDir)

	if err := fetchManagedRepo(ctx, bare, m.cloneURL); err != nil {
		return err
	}
	sha, err := resolveManagedRef(ctx, bare, m.ref)
	if err != nil {
		return err
	}

	current := filepath.Join(dir, managedCurrentLink)
	rel := filepath.Join(managedCheckoutsDir, sha)
	previous, _ := os.Readlink(current)
	if previous == rel {
		if _, err := os.Stat(current); err == nil {
			return nil
		}
		// current names this commit but its checkout is gone; add it again.
	}

	target := filepath.Join(dir, rel)
	if err := addManagedCheckout(ctx, bare, target, sha); err != nil {
		return err
	}
	for _, manifest := range m.manifests {
		if _, err := os.Stat(filepath.Join(target, manifest)); err != nil {
			removeManagedCheckout(ctx, bare, target)
			return fmt.Errorf("commit %s has no marketplace manifest at %s", shortSHA(sha), manifest)
		}
	}
	if err := swapSymlink(current, rel); err != nil {
		return err
	}

	// Keep the checkout just replaced: a session may still be reading it.
	// Anything older has had a full apply cycle to drain.
	keep := map[string]bool{sha: true, filepath.Base(previous): true}
	entries, _ := os.ReadDir(filepath.Join(dir, managedCheckoutsDir))
	for _, e := range entries {
		if !keep[e.Name()] {
			removeManagedCheckout(ctx, bare, filepath.Join(dir, managedCheckoutsDir, e.Name()))
		}
	}
	return nil
}

// fetchManagedRepo clones the bare repository on first use and fetches every
// branch and tag on later ones. Branches land under refs/heads/ directly, so a
// configured branch, tag or commit resolves the same way in either case.
func fetchManagedRepo(ctx context.Context, bare, url string) error {
	if _, err := os.Stat(filepath.Join(bare, "HEAD")); err != nil {
		// A bare directory with no HEAD is a clone that died part way;
		// start it again.
		if err := os.RemoveAll(bare); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
			return err
		}
		if _, err := managedGit(ctx, "", "clone", "--bare", "--quiet", url, bare); err != nil {
			return err
		}
		_, err := managedGit(ctx, bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
		return err
	}
	if _, err := managedGit(ctx, bare, "remote", "set-url", "origin", url); err != nil {
		return err
	}
	// --update-head-ok: in a bare repository HEAD names a branch, and fetch
	// refuses to move that branch without it.
	_, err := managedGit(ctx, bare, "fetch", "--quiet", "--prune", "--tags", "--update-head-ok", "origin")
	return err
}

// resolveManagedRef turns the configured ref into a commit. An empty ref asks
// the remote for its default branch, so a repository whose default branch
// changes is followed rather than pinned to the branch it had when niwa
// cloned it.
func resolveManagedRef(ctx context.Context, bare, ref string) (string, error) {
	if ref == "" {
		out, err := managedGit(ctx, bare, "ls-remote", "--symref", "origin", "HEAD")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(out, "\n") {
			target, ok := strings.CutPrefix(line, "ref: ")
			if !ok {
				continue
			}
			if branch, _, found := strings.Cut(target, "\t"); found {
				ref = branch
				break
			}
		}
		if ref == "" {
			return "", errors.New("the remote did not report a default branch")
		}
	}
	out, err := managedGit(ctx, bare, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("ref %q not found", ref)
	}
	return strings.TrimSpace(out), nil
}

// addManagedCheckout makes a detached worktree of sha at target. A target that
// already holds a clean checkout of sha is reused; anything else there,
// including a checkout an interrupted apply left part-written (missing files
// show as changes), is removed first.
func addManagedCheckout(ctx context.Context, bare, target, sha string) error {
	if head, err := managedGit(ctx, target, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(head) == sha {
		if status, err := managedGit(ctx, target, "status", "--porcelain", "--untracked-files=no"); err == nil && strings.TrimSpace(status) == "" {
			return nil
		}
	}
	removeManagedCheckout(ctx, bare, target)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, err := managedGit(ctx, bare, "worktree", "add", "--quiet", "--detach", target, sha); err != nil {
		removeManagedCheckout(ctx, bare, target)
		return err
	}
	return nil
}

// removeManagedCheckout deletes a checkout and drops its worktree record. It
// is best-effort: a leftover directory costs disk, never correctness, since
// nothing names it once current has moved on.
func removeManagedCheckout(ctx context.Context, bare, target string) {
	_ = os.RemoveAll(target)
	_, _ = managedGit(ctx, bare, "worktree", "prune")
}

// swapSymlink points link at target by creating a new link beside it and
// renaming it over the old one, which replaces it in one step: a reader
// resolves either the old checkout or the new one, never a missing path.
func swapSymlink(link, target string) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(link), "."+filepath.Base(link)+"-"+hex.EncodeToString(suffix[:]))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// managedGit runs git for the managed copies, in dir when it is set, and
// returns its standard output. Prompts are off: an apply that cannot fetch
// keeps the last checkout rather than waiting on a terminal nobody watches.
func managedGit(ctx context.Context, dir string, args ...string) (string, error) {
	verb := args[0]
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", verb, err, msg)
		}
		return "", fmt.Errorf("git %s: %w", verb, err)
	}
	return string(out), nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
