package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HookTarget is the repo a WorktreeCreate hook resolved to, and the slug left
// once a repo prefix was taken off the worktree name.
type HookTarget struct {
	InstanceRoot string
	Repo         string
	Slug         string
}

// ErrDelegationOptedOut is returned when the resolved instance was created
// with `niwa init --no-worktree-delegation`. The workspace root's settings are
// shared by every instance, so its hooks can reach an opted-out instance; the
// opt-out is honored here, at runtime.
var ErrDelegationOptedOut = errors.New("this instance opted out of worktree delegation (niwa init --no-worktree-delegation)")

// maxListedInstances caps how many instance names a refusal lists.
const maxListedInstances = 20

// HookAnchor returns the directory Claude Code launched the session in, from
// CLAUDE_PROJECT_DIR. When the variable is unset it returns the hook process's
// own working directory, which Claude Code chooses when it starts the hook.
// Either way the anchor comes from the harness, not from the hook payload's
// cwd, which follows the agent's `cd`; resolution anchored on it can't be
// steered by a model-chosen directory. It returns "" only when even the
// working directory can't be read.
//
// The working-directory fallback rests on an assumption about the harness:
// that Claude Code starts a hook in the session's launch directory, not in a
// directory the agent chose. If a harness ever runs hooks elsewhere without
// setting CLAUDE_PROJECT_DIR, this fallback stops protecting against a
// planted instance.
func HookAnchor() string {
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// ResolveHookInstance finds the instance a hook call acts on, anchored on the
// launch directory, and checks that target lies inside it.
//
// The instance is the nearest one at or above the anchor, which covers
// sessions launched at an instance root, in a repo, or in a niwa worktree.
// When the anchor is a workspace root (an ephemeral session that moved into
// its instance), it is the instance directly under the anchor that contains
// target. target is the payload's cwd for a create and the worktree path for
// a remove; both are model-influenced, so a target outside the instance found
// from the anchor is refused rather than followed.
//
// An empty anchor is refused: walking up from target alone would let a
// session plant an instance marker and steer the hook at it.
//
// It does not apply the delegation opt-out; callers that act on the instance
// check InstanceState.NoWorktreeDelegation themselves, as ResolveHookTarget
// and the WorktreeRemove path do.
func ResolveHookInstance(anchor, target string) (string, error) {
	if anchor == "" {
		return "", errors.New("cannot tell which directory this session was launched in")
	}
	canonTarget, err := canonicalize(target)
	if err != nil {
		return "", err
	}
	canonAnchor, err := canonicalize(anchor)
	if err != nil {
		return "", err
	}

	if instance, err := DiscoverInstance(canonAnchor); err == nil {
		canonInstance, err := canonicalize(instance)
		if err != nil {
			return "", err
		}
		if !pathHasPrefix(canonTarget, canonInstance) {
			return "", fmt.Errorf("%s is outside the instance %s this session was launched in", target, instance)
		}
		return instance, nil
	}

	if !isWorkspaceRoot(canonAnchor) {
		return "", fmt.Errorf("not inside a niwa instance: %s is neither an instance nor a workspace root", anchor)
	}
	instances, err := EnumerateInstances(canonAnchor)
	if err != nil {
		return "", err
	}
	for _, instance := range instances {
		canonInstance, err := canonicalize(instance)
		if err != nil {
			continue
		}
		if pathHasPrefix(canonTarget, canonInstance) {
			return instance, nil
		}
	}
	return "", notInsideInstanceError(instances)
}

// notInsideInstanceError names the instances a workspace-root session can
// move into, so the agent knows where to go.
func notInsideInstanceError(instances []string) error {
	if len(instances) == 0 {
		return errors.New("not inside a niwa instance, and this workspace has no instances")
	}
	names := make([]string, 0, len(instances))
	for _, instance := range instances {
		names = append(names, filepath.Base(instance))
	}
	sort.Strings(names)
	more := ""
	if len(names) > maxListedInstances {
		more = fmt.Sprintf(" (and %d more)", len(names)-maxListedInstances)
		names = names[:maxListedInstances]
	}
	return fmt.Errorf("not inside a niwa instance; cd into one of: %s%s", strings.Join(names, ", "), more)
}

// hookRepo is one repo of an instance, with its group.
type hookRepo struct {
	group string
	name  string
}

// enumerateHookRepos lists the repos cloned in instanceRoot with their groups.
func enumerateHookRepos(instanceRoot string) ([]hookRepo, error) {
	candidates, err := enumerateRepoCandidates(instanceRoot)
	if err != nil {
		return nil, err
	}
	repos := make([]hookRepo, 0, len(candidates))
	for _, c := range candidates {
		repos = append(repos, hookRepo{group: filepath.Base(filepath.Dir(c.path)), name: c.name})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].name < repos[j].name })
	return repos, nil
}

// ResolveHookTarget resolves a WorktreeCreate hook to a repo and slug.
//
// It finds the instance with ResolveHookInstance, then picks the repo in this
// order, stopping at the first that applies:
//
//  1. a repo prefix in name: `<group>/<repo>/<slug>` or `<repo>/<slug>`,
//     matched exactly against the repos cloned in the instance (the on-disk
//     scan, with each repo's group taken from its parent directory). A
//     prefix only ever selects from that set; it is never used as a path.
//  2. a prefix naming a repo the instance's state lists (the configured set
//     as of the last apply) but that has no clone on disk is refused as a
//     missing clone, rather than falling through to the rules below.
//  3. the repo whose clone contains cwd.
//  4. the only repo, when the instance has exactly one.
//
// Otherwise it refuses with the instance's repos and an example name. An
// explicit prefix wins over cwd because it is what the agent asked for at the
// moment of the call.
func ResolveHookTarget(anchor, cwd, name string) (HookTarget, error) {
	instanceRoot, err := ResolveHookInstance(anchor, cwd)
	if err != nil {
		return HookTarget{}, err
	}
	if state, err := LoadState(instanceRoot); err == nil && state.NoWorktreeDelegation {
		return HookTarget{}, ErrDelegationOptedOut
	}
	repos, err := enumerateHookRepos(instanceRoot)
	if err != nil {
		return HookTarget{}, err
	}

	target := HookTarget{InstanceRoot: instanceRoot, Slug: name}
	segs := strings.Split(name, "/")

	if len(segs) >= 3 {
		for _, r := range repos {
			if segs[0] == r.group && segs[1] == r.name {
				return withSlug(target, r.name, segs[2:], repos)
			}
		}
	}
	if len(segs) >= 2 {
		for _, r := range repos {
			if segs[0] == r.name {
				return withSlug(target, r.name, segs[1:], repos)
			}
		}
		// A prefix naming a repo the instance lists but hasn't cloned gets its
		// own message, for either form. The instance state's repo list is the
		// configured set as of the last apply; the hook has no config loaded.
		candidates := []string{segs[0]}
		if len(segs) >= 3 {
			candidates = append(candidates, segs[1])
		}
		for _, candidate := range candidates {
			if missingClone(instanceRoot, candidate) {
				return HookTarget{}, fmt.Errorf("repo %q is configured in this instance but its clone is missing; run niwa apply", candidate)
			}
		}
	}

	if repo, err := ResolveRepoNameFromCwd(instanceRoot, cwd); err == nil {
		target.Repo = repo
		return target, nil
	}
	if len(repos) == 1 {
		target.Repo = repos[0].name
		return target, nil
	}
	return HookTarget{}, noRepoSelectedError(repos)
}

// withSlug finishes a prefix match. An empty slug (`niwa/`) is refused, since
// it names a repo but no worktree.
func withSlug(target HookTarget, repo string, rest []string, repos []hookRepo) (HookTarget, error) {
	slug := strings.Join(rest, "/")
	if strings.TrimSpace(slug) == "" {
		return HookTarget{}, noRepoSelectedError(repos)
	}
	target.Repo = repo
	target.Slug = slug
	return target, nil
}

// missingClone reports whether name is a repo the instance's state lists but
// whose clone isn't on disk.
func missingClone(instanceRoot, name string) bool {
	state, err := LoadState(instanceRoot)
	if err != nil {
		return false
	}
	_, listed := state.Repos[name]
	return listed
}

// noRepoSelectedError is the refusal for a request that names no repo the
// resolver can use. It lists the repos and shows the form that works.
func noRepoSelectedError(repos []hookRepo) error {
	if len(repos) == 0 {
		return errors.New("no repo selected: this instance has no cloned repos")
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.name)
	}
	return fmt.Errorf("no repo selected; name the worktree <repo>/<slug> or <group>/<repo>/<slug>, for example %s/<slug>. Repos in this instance: %s",
		repos[0].name, strings.Join(names, ", "))
}

// HookScope is what the worktree guard needs to know about where a session
// is: the instance it acts in and that instance's repos and worktree
// directory, or, for a workspace-root session that hasn't moved into an
// instance, the instances it could move into.
type HookScope struct {
	InstanceRoot string
	// WorktreesDir is the instance's .niwa/worktrees, symlink-resolved so it
	// compares equal to a symlink-resolved target.
	WorktreesDir string
	Repos        []string // sorted
	Instances    []string // sorted directory names; set only when InstanceRoot is ""
	OptedOut     bool
}

// ResolveHookScope resolves the guard's view of a session, anchored on the
// launch directory as ResolveHookInstance is. When the anchor is a workspace
// root and cwd is in none of its instances, it returns a scope with no
// instance and the workspace's instance names. Any other failure is returned
// as an error, which the guard treats as "let the call through".
func ResolveHookScope(anchor, cwd string) (HookScope, error) {
	instanceRoot, err := ResolveHookInstance(anchor, cwd)
	if err != nil {
		canonAnchor, cerr := canonicalize(anchor)
		if cerr != nil || !isWorkspaceRoot(canonAnchor) {
			return HookScope{}, err
		}
		instances, ierr := EnumerateInstances(canonAnchor)
		if ierr != nil {
			return HookScope{}, err
		}
		names := make([]string, 0, len(instances))
		for _, instance := range instances {
			names = append(names, filepath.Base(instance))
		}
		sort.Strings(names)
		return HookScope{Instances: names}, nil
	}

	scope := HookScope{InstanceRoot: instanceRoot}
	if state, err := LoadState(instanceRoot); err == nil && state.NoWorktreeDelegation {
		scope.OptedOut = true
		return scope, nil
	}
	repos, err := enumerateHookRepos(instanceRoot)
	if err != nil {
		return HookScope{}, err
	}
	for _, r := range repos {
		scope.Repos = append(scope.Repos, r.name)
	}
	// The worktrees directory may not exist yet; resolve the instance root
	// (which does) and join, so the comparison still works on hosts where
	// the temp or home directory sits behind a symlink.
	if canonRoot, err := canonicalize(instanceRoot); err == nil {
		scope.WorktreesDir = filepath.Join(canonRoot, StateDir, "worktrees")
	}
	return scope, nil
}
