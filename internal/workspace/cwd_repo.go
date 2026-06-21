package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// repoCandidate pairs a workspace repo name with its on-disk path.
type repoCandidate struct {
	name string
	path string
}

// enumerateRepoCandidates scans instanceRoot two levels deep for cloned repos
// and returns each repo's name paired with its absolute on-disk path. The scan
// uses the same source of truth as findRepoInWorkspace and the apply-time repo
// index: each immediate child of instanceRoot is a group directory, and each
// subdirectory inside a group that contains a .git entry is a repo at
// <instanceRoot>/<group>/<repo>.
//
// Filtering mirrors EnumerateRepos: the reserved control directories (".niwa",
// ".claude") and dot-prefixed entries are skipped, and names must pass
// ValidName. Unreadable group directories are skipped rather than failing the
// whole walk. Unlike EnumerateRepos this returns paths (not just names) and
// requires a .git entry, because the resolver needs to map a filesystem
// location back to a specific repo.
func enumerateRepoCandidates(instanceRoot string) ([]repoCandidate, error) {
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("reading instance root: %w", err)
	}

	var candidates []repoCandidate
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if name == StateDir || name == ".claude" {
			continue
		}
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		if !ValidName(name) {
			continue
		}

		groupDir := filepath.Join(instanceRoot, name)
		repos, err := os.ReadDir(groupDir)
		if err != nil {
			// Skip unreadable groups rather than failing the whole walk.
			continue
		}
		for _, r := range repos {
			rname := r.Name()
			if !r.IsDir() {
				continue
			}
			if len(rname) > 0 && rname[0] == '.' {
				continue
			}
			if !ValidName(rname) {
				continue
			}
			repoPath := filepath.Join(groupDir, rname)
			if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
				continue
			}
			candidates = append(candidates, repoCandidate{name: rname, path: repoPath})
		}
	}

	return candidates, nil
}

// canonicalize resolves symlinks and cleans a path so two paths that point at
// the same on-disk location compare equal. filepath.EvalSymlinks already
// implies Clean and returns an absolute path for an absolute input, but the
// path is made absolute first so a relative cwd is anchored before resolution,
// and Clean is reapplied defensively.
func canonicalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalizing %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

// pathHasPrefix reports whether canonical path child is at or below canonical
// path parent. Both arguments must already be canonicalized. The comparison is
// component-aware: "/a/b" is a prefix of "/a/b" and "/a/b/c" but NOT of
// "/a/bc", which a raw strings.HasPrefix would wrongly accept.
func pathHasPrefix(child, parent string) bool {
	if child == parent {
		return true
	}
	withSep := parent
	if !strings.HasSuffix(withSep, string(filepath.Separator)) {
		withSep += string(filepath.Separator)
	}
	return strings.HasPrefix(child, withSep)
}

// ResolveRepoNameFromCwd maps an absolute (or relative) cwd to the name of the
// workspace repo that owns it. It is the reverse of findRepoInWorkspace
// (name->path): given a location, it returns the owning repo name.
//
// instanceRoot is the workspace instance directory whose repos form the
// candidate set; it is the same directory findRepoInWorkspace scans and the
// apply-time repo index is built from. Callers that only have a cwd can derive
// it with ResolveRepoFromCwd, which discovers the instance root first.
//
// Both the incoming cwd and each candidate repo path are canonicalized with
// filepath.EvalSymlinks + filepath.Clean before comparison, so ".." components
// and symlinked paths cannot evade or spoof the workspace check. The repo whose
// canonical path is a prefix of the canonical cwd is returned; when several
// match (one repo path nested under another), the longest prefix wins.
//
// This is a security boundary: a cwd that does not resolve under any workspace
// repo is REJECTED with an error rather than returning a best-effort guess.
// The cwd originates from an untrusted hook payload, so an arbitrary location
// must never be accepted as a valid worktree origin.
func ResolveRepoNameFromCwd(instanceRoot, cwd string) (string, error) {
	canonCwd, err := canonicalize(cwd)
	if err != nil {
		return "", err
	}

	candidates, err := enumerateRepoCandidates(instanceRoot)
	if err != nil {
		return "", err
	}

	bestName := ""
	bestLen := -1
	for _, c := range candidates {
		canonRepo, err := canonicalize(c.path)
		if err != nil {
			// A candidate path that cannot be canonicalized (e.g. removed
			// between the scan and now) is skipped rather than aborting the
			// whole resolution.
			continue
		}
		if !pathHasPrefix(canonCwd, canonRepo) {
			continue
		}
		if len(canonRepo) > bestLen {
			bestLen = len(canonRepo)
			bestName = c.name
		}
	}

	if bestName == "" {
		return "", fmt.Errorf("cwd %q does not resolve under any workspace repo in %s", cwd, instanceRoot)
	}
	return bestName, nil
}

// ResolveRepoFromCwd discovers the workspace instance that owns cwd and then
// resolves cwd to a repo name within it. It is the convenience entry point for
// callers (such as the worktree from-hook subcommand) that hold only a cwd:
// DiscoverInstance walks up from cwd to the instance root, then
// ResolveRepoNameFromCwd applies the canonicalizing longest-prefix match.
//
// It returns the instance root alongside the repo name so callers can pass the
// instance root to the worktree creation core without re-discovering it. A cwd
// outside any niwa instance, or one that resolves outside every repo in the
// instance it sits in, is rejected.
func ResolveRepoFromCwd(cwd string) (instanceRoot, repoName string, err error) {
	instanceRoot, err = DiscoverInstance(cwd)
	if err != nil {
		return "", "", err
	}
	repoName, err = ResolveRepoNameFromCwd(instanceRoot, cwd)
	if err != nil {
		return "", "", err
	}
	return instanceRoot, repoName, nil
}
